package shapeup

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

func activeCycle(t *testing.T) *Cycle {
	t.Helper()
	c, err := StartCycle(nil, "c1", "Cycle 1", 14, t0)
	require.NoError(t, err)
	return c
}

func TestStartCycle(t *testing.T) {
	rome := time.FixedZone("Rome", 2*3600)
	tests := []struct {
		name    string
		active  *Cycle
		cname   string
		days    float64
		length  time.Duration
		wantErr error
	}{
		{name: "ok", cname: "Cycle 1", days: 14, length: 14 * 24 * time.Hour},
		{name: "trims name", cname: "  Cycle 1  ", days: 1, length: 24 * time.Hour},
		{name: "fraction of a day for a short smoke cycle", cname: "Cycle 1", days: 0.005, length: 7*time.Minute + 12*time.Second},
		{name: "one minute is the shortest cycle", cname: "Cycle 1", days: 1.0 / 1440, length: time.Minute},
		{name: "rejects while one is active", active: &Cycle{ID: "x", Status: CycleActive}, cname: "c", days: 7, wantErr: ErrCycleActive},
		{name: "rejects empty name", cname: "   ", days: 7, wantErr: ErrInvalidName},
		{name: "rejects long name", cname: string(make([]byte, MaxNameLength+1)), days: 7, wantErr: ErrInvalidName},
		{name: "rejects zero days", cname: "c", days: 0, wantErr: ErrInvalidLength},
		{name: "rejects under a minute", cname: "c", days: 0.0005, wantErr: ErrInvalidLength},
		{name: "rejects negative days", cname: "c", days: -1, wantErr: ErrInvalidLength},
		{name: "rejects too many days", cname: "c", days: MaxCycleDays + 1, wantErr: ErrInvalidLength},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, err := StartCycle(tc.active, "c1", tc.cname, tc.days, t0.In(rome))
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Nil(t, c)
				return
			}
			require.NoError(t, err)
			require.Equal(t, "c1", c.ID)
			require.Equal(t, "Cycle 1", c.Name)
			require.Equal(t, CycleActive, c.Status)
			require.Equal(t, time.UTC, c.Start.Location(), "deadlines are stored in UTC")
			require.True(t, c.Start.Equal(t0))
			require.Equal(t, tc.length, c.End.Sub(c.Start))
			require.Empty(t, c.Bets)
		})
	}
}

func TestStartCycle_AllowsWhenPreviousIsClosed(t *testing.T) {
	_, err := StartCycle(&Cycle{ID: "old", Status: CycleClosed}, "c2", "Next", 7, t0)
	require.NoError(t, err)
}

func TestPlaceBet(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(c *Cycle)
		task     string
		appetite Appetite
		at       time.Time
		wantErr  error
	}{
		{name: "small", task: "t1", appetite: AppetiteSmall, at: t0.Add(time.Hour)},
		{name: "big", task: "t1", appetite: AppetiteBig, at: t0.Add(time.Hour)},
		{name: "empty task", task: " ", appetite: AppetiteSmall, at: t0, wantErr: ErrInvalidTask},
		{name: "unknown appetite", task: "t1", appetite: "medium", at: t0, wantErr: ErrInvalidAppetite},
		{
			name: "duplicate", task: "t1", appetite: AppetiteSmall, at: t0, wantErr: ErrDuplicateBet,
			setup: func(c *Cycle) { require.NoError(t, c.PlaceBet("t1", "T1", AppetiteBig, t0)) },
		},
		{
			name: "cut task cannot be re-bet in the same cycle", task: "t1", appetite: AppetiteSmall, at: t0, wantErr: ErrDuplicateBet,
			setup: func(c *Cycle) {
				require.NoError(t, c.PlaceBet("t1", "T1", AppetiteBig, t0))
				require.NoError(t, c.Cut("t1", t0))
			},
		},
		{
			name: "cycle is full", task: "t1", appetite: AppetiteSmall, at: t0, wantErr: ErrTooManyBets,
			setup: func(c *Cycle) {
				for i := range MaxBetsPerCycle {
					require.NoError(t, c.PlaceBet(fmt.Sprintf("full-%d", i), "T", AppetiteSmall, t0))
				}
			},
		},
		{name: "overdue cycle", task: "t1", appetite: AppetiteSmall, at: t0.Add(14 * 24 * time.Hour), wantErr: ErrCycleOverdue},
		{
			name: "closed cycle", task: "t1", appetite: AppetiteSmall, at: t0, wantErr: ErrCycleClosed,
			setup: func(c *Cycle) { require.NoError(t, c.Close(t0)) },
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := activeCycle(t)
			if tc.setup != nil {
				tc.setup(c)
			}
			before := len(c.Bets)
			err := c.PlaceBet(tc.task, "Title", tc.appetite, tc.at)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				require.Len(t, c.Bets, before)
				return
			}
			require.NoError(t, err)
			bet := c.Bets[len(c.Bets)-1]
			require.Equal(t, Bet{
				TaskID: tc.task, TaskTitle: "Title", CycleID: "c1", Appetite: tc.appetite,
				PlacedAt: tc.at, Status: BetActive,
			}, bet)
		})
	}
}

func TestShipAndCut(t *testing.T) {
	at := t0.Add(48 * time.Hour)
	for _, op := range []struct {
		name   string
		apply  func(c *Cycle, task string) error
		status BetStatus
	}{
		{"ship", func(c *Cycle, task string) error { return c.MarkShipped(task, at) }, BetShipped},
		{"cut", func(c *Cycle, task string) error { return c.Cut(task, at) }, BetCut},
	} {
		t.Run(op.name, func(t *testing.T) {
			c := activeCycle(t)
			require.NoError(t, c.PlaceBet("t1", "T1", AppetiteSmall, t0))
			c.Bets[0].Error = "previous failure"

			require.NoError(t, op.apply(c, "t1"))
			require.Equal(t, op.status, c.Bets[0].Status)
			require.NotNil(t, c.Bets[0].ClosedAt)
			require.True(t, c.Bets[0].ClosedAt.Equal(at))
			require.Empty(t, c.Bets[0].Error)

			require.ErrorIs(t, op.apply(c, "t1"), ErrBetNotActive)
			require.ErrorIs(t, op.apply(c, "missing"), ErrBetNotFound)
		})
	}
}

// Past the deadline only the circuit breaker closes bets: shipping late would
// extend the cycle.
func TestMarkShipped_RejectedAfterDeadline(t *testing.T) {
	c := activeCycle(t)
	require.NoError(t, c.PlaceBet("t1", "T1", AppetiteSmall, t0))
	late := t0.Add(14 * 24 * time.Hour)

	require.ErrorIs(t, c.MarkShipped("t1", late), ErrCycleOverdue)
	require.Equal(t, BetActive, c.Bets[0].Status)
	require.NoError(t, c.Cut("t1", late), "cutting stays possible")
}

func TestOverdueAndTimeLeft(t *testing.T) {
	c := activeCycle(t)
	end := t0.Add(14 * 24 * time.Hour)

	require.False(t, c.Overdue(end.Add(-time.Nanosecond)))
	require.True(t, c.Overdue(end), "the deadline itself is overdue")
	require.True(t, c.Overdue(end.Add(time.Hour)))

	require.Equal(t, time.Hour, c.TimeLeft(end.Add(-time.Hour)))
	require.Equal(t, time.Duration(0), c.TimeLeft(end.Add(time.Hour)), "never negative")
}

func TestClose(t *testing.T) {
	c := activeCycle(t)
	require.NoError(t, c.PlaceBet("t1", "T1", AppetiteSmall, t0))
	require.NoError(t, c.PlaceBet("t2", "T2", AppetiteSmall, t0))

	require.ErrorIs(t, c.Close(t0), ErrActiveBets)
	require.Equal(t, CycleActive, c.Status)

	require.NoError(t, c.MarkShipped("t1", t0))
	require.NoError(t, c.Cut("t2", t0))
	require.Equal(t, []Bet(nil), c.ActiveBets())

	closeAt := t0.Add(time.Hour)
	require.NoError(t, c.Close(closeAt))
	require.Equal(t, CycleClosed, c.Status)
	require.True(t, c.ClosedAt.Equal(closeAt))
	require.ErrorIs(t, c.Close(closeAt), ErrCycleClosed)
}

func TestErrorKinds(t *testing.T) {
	require.Equal(t, KindInvalid, KindOf(ErrInvalidName))
	require.Equal(t, KindConflict, KindOf(ErrCycleActive))
	require.Equal(t, KindNotFound, KindOf(ErrBetNotFound))
	require.Equal(t, KindUnknown, KindOf(errors.New("boom")))
}

func TestErrTooManyBets_NamesTheCap(t *testing.T) {
	require.Contains(t, ErrTooManyBets.Error(), fmt.Sprintf(" %d ", MaxBetsPerCycle))
}
