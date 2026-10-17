package shapeup

import (
	"strings"
	"time"
	"unicode/utf8"
)

// StartCycle opens a new cycle of the given length. active is the current
// cycle of the workspace, nil when there is none. days may be a fraction, so a
// smoke test can run a cycle of a few minutes; one minute is the minimum.
func StartCycle(active *Cycle, id, name string, days float64, now time.Time) (*Cycle, error) {
	if active != nil && active.Status == CycleActive {
		return nil, ErrCycleActive
	}
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return nil, ErrInvalidName
	}
	if days > MaxCycleDays {
		return nil, ErrInvalidLength
	}
	length := time.Duration(days * float64(24*time.Hour)).Round(time.Second)
	if length < time.Minute {
		return nil, ErrInvalidLength
	}
	start := now.UTC()
	return &Cycle{
		ID:     id,
		Name:   name,
		Start:  start,
		End:    start.Add(length),
		Status: CycleActive,
	}, nil
}

// PlaceBet adds an active bet. A task gets at most one bet per cycle: once
// cut, it comes back only through a new bet in a later cycle.
func (c *Cycle) PlaceBet(taskID, title string, appetite Appetite, now time.Time) error {
	if c.Status != CycleActive {
		return ErrCycleClosed
	}
	if c.Overdue(now) {
		return ErrCycleOverdue
	}
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return ErrInvalidTask
	}
	if appetite != AppetiteSmall && appetite != AppetiteBig {
		return ErrInvalidAppetite
	}
	for _, b := range c.Bets {
		if b.TaskID == taskID {
			return ErrDuplicateBet
		}
	}
	c.Bets = append(c.Bets, Bet{
		TaskID:    taskID,
		TaskTitle: title,
		CycleID:   c.ID,
		Appetite:  appetite,
		PlacedAt:  now.UTC(),
		Status:    BetActive,
	})
	return nil
}

// MarkShipped closes an active bet as shipped.
func (c *Cycle) MarkShipped(taskID string, now time.Time) error {
	return c.closeBet(taskID, BetShipped, now)
}

// Cut closes an active bet as cut. Archiving the task is the caller's job.
func (c *Cycle) Cut(taskID string, now time.Time) error {
	return c.closeBet(taskID, BetCut, now)
}

func (c *Cycle) closeBet(taskID string, status BetStatus, now time.Time) error {
	b := c.Bet(taskID)
	if b == nil {
		return ErrBetNotFound
	}
	if b.Status != BetActive {
		return ErrBetNotActive
	}
	at := now.UTC()
	b.Status = status
	b.ClosedAt = &at
	b.Error = ""
	return nil
}

// Bet returns the bet on taskID, nil when there is none.
func (c *Cycle) Bet(taskID string) *Bet {
	for i := range c.Bets {
		if c.Bets[i].TaskID == taskID {
			return &c.Bets[i]
		}
	}
	return nil
}

// ActiveBets returns the bets the circuit breaker still has to cut.
func (c *Cycle) ActiveBets() []Bet {
	var out []Bet
	for _, b := range c.Bets {
		if b.Status == BetActive {
			out = append(out, b)
		}
	}
	return out
}

// Overdue reports whether the deadline has been reached.
func (c *Cycle) Overdue(now time.Time) bool {
	return !now.Before(c.End)
}

// TimeLeft is the time until the deadline, never negative.
func (c *Cycle) TimeLeft(now time.Time) time.Duration {
	if left := c.End.Sub(now); left > 0 {
		return left
	}
	return 0
}

// Close ends the cycle. Every bet must be shipped or cut first: nothing is
// carried over or extended.
func (c *Cycle) Close(now time.Time) error {
	if c.Status != CycleActive {
		return ErrCycleClosed
	}
	if len(c.ActiveBets()) > 0 {
		return ErrActiveBets
	}
	at := now.UTC()
	c.Status = CycleClosed
	c.ClosedAt = &at
	return nil
}
