package shapeup

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestService_StartCycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)

	c, err := f.svc.StartCycle(ctx, "ws1", "Cycle 1", 14)
	require.NoError(t, err)
	require.Equal(t, "c1", c.ID)

	_, err = f.svc.StartCycle(ctx, "ws1", "Again", 14)
	require.ErrorIs(t, err, ErrCycleActive)

	snap := f.snapshot(t)
	require.Equal(t, "Cycle 1", snap.Active.Name)
	require.Equal(t, 14*24*time.Hour, snap.TimeLeft)
	require.False(t, snap.Overdue)
}

func TestService_StartCycleAfterOverdueCycleRunsTheBreakerFirst(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()

	c, err := f.svc.StartCycle(ctx, "ws1", "Next", 7)
	require.NoError(t, err)
	require.Equal(t, "Next", c.Name)
	require.True(t, f.host.Archived("t1"))
	require.Len(t, f.snapshot(t).History, 1)
}

func TestService_StartCycleWhileTheBreakerIsBlocked(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.host.Granted = false
	f.pastDeadline()

	_, err := f.svc.StartCycle(ctx, "ws1", "Next", 7)
	require.ErrorIs(t, err, ErrCycleStuck)
	require.ErrorIs(t, f.svc.MarkShipped(ctx, "ws1", "t1"), ErrCycleOverdue)
}

func TestService_PlaceBet(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.host.AddTask("ws1", "wf1", "s1", "t1", "Pitch: search")
	f.host.AddTask("ws2", "wf2", "s2", "foreign", "Other workspace")
	f.host.AddTask("ws1", "wf1", "s1", "gone", "Archived")
	f.host.ArchiveDirectly("gone")

	require.ErrorIs(t, f.svc.PlaceBet(ctx, "ws1", "t1", AppetiteSmall), ErrNoActiveCycle)

	_, err := f.svc.StartCycle(ctx, "ws1", "Cycle", 14)
	require.NoError(t, err)
	require.ErrorIs(t, f.svc.PlaceBet(ctx, "ws1", "missing", AppetiteSmall), ErrTaskNotFound)
	require.ErrorIs(t, f.svc.PlaceBet(ctx, "ws1", "foreign", AppetiteSmall), ErrTaskNotFound)
	require.ErrorIs(t, f.svc.PlaceBet(ctx, "ws1", "gone", AppetiteSmall), ErrTaskNotFound)

	require.NoError(t, f.svc.PlaceBet(ctx, "ws1", "t1", AppetiteBig))
	require.ErrorIs(t, f.svc.PlaceBet(ctx, "ws1", "t1", AppetiteBig), ErrDuplicateBet)

	bet := f.snapshot(t).Active.Bet("t1")
	require.Equal(t, "Pitch: search", bet.TaskTitle)
	require.Equal(t, AppetiteBig, bet.Appetite)
}

func TestService_CutNowArchivesTheTask(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")

	require.NoError(t, f.svc.CutNow(ctx, "ws1", "t1"))
	require.True(t, f.host.Archived("t1"))
	require.Equal(t, BetCut, f.snapshot(t).Active.Bet("t1").Status)
	require.ErrorIs(t, f.svc.CutNow(ctx, "ws1", "t1"), ErrBetNotActive)
	require.ErrorIs(t, f.svc.CutNow(ctx, "ws1", "nope"), ErrBetNotFound)
}

func TestService_CutNowWithoutGrantKeepsTheBet(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.host.Granted = false

	err := f.svc.CutNow(ctx, "ws1", "t1")
	require.ErrorIs(t, err, ErrArchiveFailed)
	require.Contains(t, err.Error(), "Workspace access")
	require.Equal(t, BetActive, f.snapshot(t).Active.Bet("t1").Status)
	require.False(t, f.host.Archived("t1"))
}

// A "Cut now" whose reply timed out can still archive the task on the Host.
// The bet must not stay active on an archived task until the deadline.
func TestService_ArchivedTaskClosesItsActiveBet(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2", "t3")
	f.host.ArchiveDirectly("t1")

	bet := f.snapshot(t).Active.Bet("t1")
	require.Equal(t, BetCut, bet.Status)
	require.True(t, bet.ClosedAt.Equal(t0))
	require.Equal(t, BetCut, f.storedActive(t).Bet("t1").Status, "the reconciliation is saved")

	f.host.ArchiveDirectly("t2")
	require.ErrorIs(t, f.svc.MarkShipped(ctx, "ws1", "t2"), ErrBetNotActive)
	require.Equal(t, BetCut, f.storedActive(t).Bet("t2").Status, "saved even when the action fails")
	require.Equal(t, BetActive, f.storedActive(t).Bet("t3").Status)
	require.Zero(t, f.host.ArchiveCalls)
}

func TestService_MarkShippedDoesNotArchive(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")

	require.NoError(t, f.svc.MarkShipped(ctx, "ws1", " t1 "), "the task id is trimmed as in PlaceBet")
	require.False(t, f.host.Archived("t1"))
	require.Equal(t, BetShipped, f.snapshot(t).Active.Bet("t1").Status)
	require.ErrorIs(t, f.svc.MarkShipped(ctx, "ws2", "t1"), ErrNoActiveCycle)
}

func TestService_Candidates(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.host.AddStep("wf1", "s-raw", "Raw ideas", 0)
	f.host.AddStep("wf1", "s-bet", "Betting table", 2)
	f.host.AddTask("ws1", "wf1", "s-raw", "a", "Zeta idea")
	f.host.AddTask("ws1", "wf1", "s-bet", "b", "Shaped pitch")
	f.host.AddTask("ws1", "wf1", "s-raw", "c", "Alpha idea")
	f.host.AddTask("ws1", "wf1", "s-bet", "d", "Already bet")
	f.host.AddTask("ws1", "wf1", "s-bet", "e", "Archived")
	f.host.ArchiveDirectly("e")
	f.host.AddTask("ws2", "wf2", "x", "f", "Other workspace")

	_, err := f.svc.StartCycle(ctx, "ws1", "Cycle", 14)
	require.NoError(t, err)
	require.NoError(t, f.svc.PlaceBet(ctx, "ws1", "d", AppetiteSmall))

	got, err := f.svc.Candidates(ctx, "ws1")
	require.NoError(t, err)
	require.Equal(t, []Candidate{
		{TaskID: "b", Title: "Shaped pitch", StepName: "Betting table", AtBettingTable: true},
		{TaskID: "c", Title: "Alpha idea", StepName: "Raw ideas"},
		{TaskID: "a", Title: "Zeta idea", StepName: "Raw ideas"},
	}, got)
}

// The page polls state.get, so its reply must not grow with every cycle.
func TestService_SnapshotReturnsOnlyRecentHistory(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	store := NewStore(f.host)
	for i := range MaxHistory + 2 {
		start := t0.Add(time.Duration(i) * 24 * time.Hour)
		c, err := StartCycle(nil, fmt.Sprintf("old%02d", i), "Old", 1, start)
		require.NoError(t, err)
		require.NoError(t, c.Close(c.End))
		require.NoError(t, store.Archive(ctx, "ws1", c))
	}

	history := f.snapshot(t).History
	require.Len(t, history, MaxHistory)
	require.Equal(t, fmt.Sprintf("old%02d", MaxHistory+1), history[0].ID, "newest first")
	require.Equal(t, "old02", history[MaxHistory-1].ID)
}

// A failing breaker run must not take the page down: the page still shows
// the cycle, and the next run retries.
func TestService_SnapshotSurvivesABreakerFailure(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()
	f.host.StateErr = errors.New("state unavailable")

	snap, err := f.svc.Snapshot(ctx, "ws1")
	require.NoError(t, err)
	require.NotNil(t, snap.Active)
	require.True(t, snap.Overdue)

	f.host.StateErr = nil
	require.Nil(t, f.snapshot(t).Active, "the next run finishes the cut")
	require.Len(t, f.snapshot(t).History, 1)
}

// The workspace task list grows with every archived task, so each call reads
// it once and shares it between the breaker, the archived-task check and the
// action itself.
func TestService_ListsTasksOncePerCall(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2")
	f.host.AddTask("ws1", "wf1", "s1", "t3", "Third")

	calls := func(run func() error) int {
		t.Helper()
		before := f.host.TaskLists
		require.NoError(t, run())
		return f.host.TaskLists - before
	}
	require.Equal(t, 1, calls(func() error { return f.svc.PlaceBet(ctx, "ws1", "t3", AppetiteSmall) }), "place bet")
	require.Equal(t, 1, calls(func() error { return f.svc.CutNow(ctx, "ws1", "t1") }), "cut now")

	f.host.Granted = false
	f.pastDeadline()
	require.Equal(t, 1, calls(func() error {
		_, err := f.svc.Snapshot(ctx, "ws1")
		return err
	}), "snapshot of an overdue cycle")
}

func TestService_CutNowTrimsTheTaskID(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1")

	require.NoError(t, f.svc.CutNow(context.Background(), "ws1", " t1 "))
	require.True(t, f.host.Archived("t1"))
	require.Equal(t, BetCut, f.snapshot(t).Active.Bet("t1").Status)
}
