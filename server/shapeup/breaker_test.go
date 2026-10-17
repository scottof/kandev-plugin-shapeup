package shapeup

import (
	"context"
	"testing"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"

	"kandev-plugin-shapeup/server/internal/fakehost"
)

// clock is a settable time source for Service.
type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

type fixture struct {
	host  *fakehost.Host
	clock *clock
	svc   *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	host := fakehost.New()
	clk := &clock{now: t0}
	ids := 0
	svc := NewService(host.AsHost(), clk.Now, func() string {
		ids++
		return "c" + string(rune('0'+ids))
	})
	return &fixture{host: host, clock: clk, svc: svc}
}

// rebuild swaps the Host view, e.g. after toggling NoExact.
func (f *fixture) rebuild() {
	f.svc = NewService(f.host.AsHost(), f.clock.Now, func() string { return "cx" })
}

// cycleWithBets starts a 14-day cycle in ws1 with one bet per task.
func (f *fixture) cycleWithBets(t *testing.T, tasks ...string) {
	t.Helper()
	ctx := context.Background()
	_, err := f.svc.StartCycle(ctx, "ws1", "Cycle", 14)
	require.NoError(t, err)
	for _, id := range tasks {
		f.host.AddTask("ws1", "wf1", "step-bet", id, "Title "+id)
		require.NoError(t, f.svc.PlaceBet(ctx, "ws1", id, AppetiteSmall))
	}
}

func (f *fixture) pastDeadline() { f.clock.now = t0.Add(14*24*time.Hour + time.Minute) }

func (f *fixture) snapshot(t *testing.T) Snapshot {
	t.Helper()
	snap, err := f.svc.Snapshot(context.Background(), "ws1")
	require.NoError(t, err)
	return snap
}

func TestBreaker_NoopBeforeDeadline(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.clock.now = t0.Add(14*24*time.Hour - time.Second)

	require.NoError(t, f.svc.RunBreaker(context.Background()))

	require.Zero(t, f.host.ArchiveCalls)
	require.False(t, f.host.Archived("t1"))
	snap := f.snapshot(t)
	require.NotNil(t, snap.Active)
	require.Equal(t, BetActive, snap.Active.Bets[0].Status)
}

func TestBreaker_CutsActiveBetsAndClosesCycle(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2")
	require.NoError(t, f.svc.MarkShipped(ctx, "ws1", "t2"))
	f.pastDeadline()

	require.NoError(t, f.svc.RunBreaker(ctx))

	require.True(t, f.host.Archived("t1"), "the unfinished task is archived")
	require.False(t, f.host.Archived("t2"), "shipped bets are never archived")
	require.Equal(t, 1, f.host.ArchiveCalls)

	snap := f.snapshot(t)
	require.Nil(t, snap.Active, "nothing is extended")
	require.Len(t, snap.History, 1)
	closed := snap.History[0]
	require.Equal(t, CycleClosed, closed.Status)
	require.True(t, closed.ClosedAt.Equal(f.clock.now))
	require.Equal(t, BetCut, closed.Bet("t1").Status)
	require.Equal(t, BetShipped, closed.Bet("t2").Status)

	ids, err := NewStore(f.host).Workspaces(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)

	require.NoError(t, f.svc.RunBreaker(ctx))
	require.Equal(t, 1, f.host.ArchiveCalls, "a second run is a no-op")
	require.Len(t, f.snapshot(t).History, 1)
}

func TestBreaker_TaskAlreadyArchivedOrDeletedCountsAsCut(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2")
	f.host.ArchiveDirectly("t1")
	f.host.DeleteTask("t2")
	f.pastDeadline()

	require.NoError(t, f.svc.RunBreaker(context.Background()))

	require.Zero(t, f.host.ArchiveCalls)
	history := f.snapshot(t).History
	require.Len(t, history, 1)
	require.Equal(t, BetCut, history[0].Bet("t1").Status)
	require.Equal(t, BetCut, history[0].Bet("t2").Status)
}

func TestBreaker_MissingGrantKeepsCycleOpenUntilGranted(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.host.Granted = false
	f.pastDeadline()

	require.NoError(t, f.svc.RunBreaker(ctx))

	require.False(t, f.host.Archived("t1"))
	snap := f.snapshot(t)
	require.NotNil(t, snap.Active, "the cycle stays open so the cut is retried")
	require.Equal(t, BetActive, snap.Active.Bets[0].Status)
	require.Contains(t, snap.Active.Bets[0].Error, "Workspace access")
	require.True(t, snap.Overdue)

	f.host.Granted = true
	require.NoError(t, f.svc.RunBreaker(ctx))
	require.True(t, f.host.Archived("t1"))
	require.Nil(t, f.snapshot(t).Active)
}

// storedActive reads the store directly: Snapshot would run the breaker again.
func (f *fixture) storedActive(t *testing.T) *Cycle {
	t.Helper()
	active, err := NewStore(f.host).Active(context.Background(), "ws1")
	require.NoError(t, err)
	return active
}

func TestBreaker_RetriesWhenHostIsUnavailable(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()
	f.host.ArchiveStatus = pluginsdk.CommandUnavailable

	require.NoError(t, f.svc.RunBreaker(ctx))
	active := f.storedActive(t)
	require.NotNil(t, active)
	require.Equal(t, reasonRetry, active.Bets[0].Error)

	require.NoError(t, f.svc.RunBreaker(ctx))
	require.True(t, f.host.Archived("t1"))
	require.Nil(t, f.snapshot(t).Active)
}

// The Host records a conflict against the idempotency key and replays it for
// the same payload, so a conflict is retried only once the task changed.
func TestBreaker_ConflictRetriesOnlyAfterTheTaskChanges(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()
	f.host.ArchiveStatus = pluginsdk.CommandConflict

	require.NoError(t, f.svc.RunBreaker(ctx))
	require.NoError(t, f.svc.RunBreaker(ctx))
	require.Equal(t, 1, f.host.Replays, "the unchanged request is answered from the receipt")
	active := f.storedActive(t)
	require.NotNil(t, active)
	require.Equal(t, reasonConflict, active.Bets[0].Error)
	require.Contains(t, reasonConflict, "archive it by hand", "a human can always finish the cut")

	f.host.TouchTask("t1")
	require.NoError(t, f.svc.RunBreaker(ctx))
	require.True(t, f.host.Archived("t1"))
	require.Nil(t, f.snapshot(t).Active)
}

// The Host replays a recorded result for the same key and payload, and the
// approval revision is not in the payload. Re-saving the grant must not reuse
// a key recorded under the old revision, or the old rejection is replayed.
func TestBreaker_NewGrantRevisionUsesANewKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()
	f.host.ArchiveStatus = pluginsdk.CommandInvalid

	require.NoError(t, f.svc.RunBreaker(ctx))
	require.Equal(t, reasonRejected, f.storedActive(t).Bets[0].Error)

	f.host.ApprovalRevision = 8
	require.NoError(t, f.svc.RunBreaker(ctx))
	require.True(t, f.host.Archived("t1"))
	require.Nil(t, f.snapshot(t).Active)
}

func TestBreaker_HumanArchiveFinishesABlockedCut(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.pastDeadline()
	f.host.ArchiveStatus = pluginsdk.CommandConflict
	require.NoError(t, f.svc.RunBreaker(ctx))

	f.host.ArchiveDirectly("t1")
	require.NoError(t, f.svc.RunBreaker(ctx))
	history := f.snapshot(t).History
	require.Len(t, history, 1)
	require.Equal(t, BetCut, history[0].Bet("t1").Status)
}

func TestBreaker_HostWithoutExactCommands(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	f.host.NoExact = true
	f.rebuild()
	f.pastDeadline()

	require.NoError(t, f.svc.RunBreaker(context.Background()))
	snap := f.snapshot(t)
	require.NotNil(t, snap.Active)
	require.Contains(t, snap.Active.Bets[0].Error, "cannot archive")
}

func TestBreaker_ChecksEveryIndexedWorkspace(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")

	f.clock.now = t0.Add(10 * 24 * time.Hour)
	_, err := f.svc.StartCycle(ctx, "ws2", "Later", 14)
	require.NoError(t, err)
	f.host.AddTask("ws2", "wf2", "s", "t9", "Other")
	require.NoError(t, f.svc.PlaceBet(ctx, "ws2", "t9", AppetiteBig))

	// A fresh Service, as after a plugin restart: the deadline passed while
	// the plugin was not running.
	f.pastDeadline()
	f.rebuild()
	require.NoError(t, f.svc.RunBreaker(ctx))

	require.True(t, f.host.Archived("t1"))
	require.False(t, f.host.Archived("t9"), "ws2 is not overdue yet")
	ids, err := NewStore(f.host).Workspaces(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"ws2"}, ids)
}

// Archiving stops running agents and can take far longer than a state read.
// Each archive gets its own budget, so a long one cannot starve the next and
// a caller close to its own deadline does not abort an archive midway.
func TestBreaker_EachArchiveHasItsOwnTimeout(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2")
	f.pastDeadline()

	const delay = 100 * time.Millisecond
	var deadlines []time.Time
	f.host.BeforeArchive = func(ctx context.Context, _ string) {
		deadline, _ := ctx.Deadline()
		deadlines = append(deadlines, deadline)
		if len(deadlines) == 1 {
			time.Sleep(delay) // the first archive is slow
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	require.NoError(t, f.svc.RunBreaker(ctx))

	require.True(t, f.host.Archived("t1"))
	require.True(t, f.host.Archived("t2"))
	require.Len(t, deadlines, 2)
	for _, d := range deadlines {
		require.WithinRange(t, d, start.Add(ArchiveTimeout-time.Second), time.Now().Add(ArchiveTimeout))
	}
	require.GreaterOrEqual(t, deadlines[1].Sub(deadlines[0]), delay, "the second archive gets a fresh budget")
}

// The caller holds the service lock for the whole run, so once its deadline
// passes no new archive may start: page actions waiting on the lock would
// otherwise wait one archive budget per bet. What was cut is still saved.
func TestBreaker_StopsStartingArchivesAfterTheCallerDeadline(t *testing.T) {
	f := newFixture(t)
	f.cycleWithBets(t, "t1", "t2")
	f.pastDeadline()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	var started []string
	f.host.BeforeArchive = func(_ context.Context, taskID string) {
		started = append(started, taskID)
		<-ctx.Done() // the archive outlives the caller
	}
	require.NoError(t, f.svc.RunBreaker(ctx))

	require.Equal(t, []string{"t1"}, started, "no archive starts after the deadline")
	require.True(t, f.host.Archived("t1"))
	require.False(t, f.host.Archived("t2"))
	active := f.storedActive(t)
	require.NotNil(t, active, "the cycle stays open for the bet not reached")
	require.Equal(t, BetCut, active.Bet("t1").Status, "the finished cut is saved")
	require.Equal(t, BetActive, active.Bet("t2").Status)

	f.host.BeforeArchive = nil
	require.NoError(t, f.svc.RunBreaker(context.Background()))
	require.True(t, f.host.Archived("t2"))
	require.Nil(t, f.storedActive(t))
}

// The ticker takes the lock per workspace, so a page action waits for one
// workspace's cut at most, not for the whole run.
func TestBreaker_ReleasesTheLockBetweenWorkspaces(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	f.cycleWithBets(t, "t1")
	_, err := f.svc.StartCycle(ctx, "ws2", "Other", 14)
	require.NoError(t, err)
	f.host.AddTask("ws2", "wf2", "s", "t9", "Other")
	require.NoError(t, f.svc.PlaceBet(ctx, "ws2", "t9", AppetiteSmall))
	f.pastDeadline()

	var freeBetween []bool
	f.svc.afterWorkspace = func() {
		free := f.svc.mu.TryLock()
		if free {
			f.svc.mu.Unlock()
		}
		freeBetween = append(freeBetween, free)
	}
	require.NoError(t, f.svc.RunBreaker(ctx))

	require.Equal(t, []bool{true, true}, freeBetween)
	require.True(t, f.host.Archived("t1"))
	require.True(t, f.host.Archived("t9"))
}
