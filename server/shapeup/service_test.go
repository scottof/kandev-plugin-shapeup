package shapeup

import (
	"context"
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

func TestService_MarkShippedDoesNotArchive(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")

	require.NoError(t, f.svc.MarkShipped(ctx, "ws1", "t1"))
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
