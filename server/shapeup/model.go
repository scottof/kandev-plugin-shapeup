// Package shapeup holds the Shape Up domain: cycles, bets and the circuit
// breaker. The rules in cycle.go are pure and take the current time as an
// argument; store.go and breaker.go are the only parts that talk to the Host.
package shapeup

import (
	"errors"
	"time"
)

type Appetite string

const (
	// AppetiteSmall is a bet worth part of a cycle.
	AppetiteSmall Appetite = "small"
	// AppetiteBig is a bet worth the whole cycle.
	AppetiteBig Appetite = "big"
)

type CycleStatus string

const (
	CycleActive CycleStatus = "active"
	CycleClosed CycleStatus = "closed"
)

type BetStatus string

const (
	BetActive  BetStatus = "active"
	BetShipped BetStatus = "shipped"
	BetCut     BetStatus = "cut"
)

const (
	MaxNameLength = 80
	MaxCycleDays  = 90
)

// Cycle is a fixed time box. End is the deadline the circuit breaker
// enforces. All times are UTC.
type Cycle struct {
	ID       string      `json:"id"`
	Name     string      `json:"name"`
	Start    time.Time   `json:"start"`
	End      time.Time   `json:"end"`
	Status   CycleStatus `json:"status"`
	ClosedAt *time.Time  `json:"closed_at,omitempty"`
	Bets     []Bet       `json:"bets"`
}

// Bet commits a cycle to one kandev task. TaskTitle is a snapshot taken when
// the bet is placed, so the history still reads well after the task is
// archived or renamed. Error holds the last circuit-breaker failure for an
// active bet (for example a missing workspace grant).
type Bet struct {
	TaskID    string     `json:"task_id"`
	TaskTitle string     `json:"task_title"`
	CycleID   string     `json:"cycle_id"`
	Appetite  Appetite   `json:"appetite"`
	PlacedAt  time.Time  `json:"placed_at"`
	Status    BetStatus  `json:"status"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"`
	Error     string     `json:"error,omitempty"`
}

// Kind classifies domain errors so the action layer can map them to HTTP
// statuses without parsing messages.
type Kind int

const (
	KindUnknown Kind = iota
	KindInvalid
	KindConflict
	KindNotFound
)

// Error is a domain error with a message safe to show in the UI.
type Error struct {
	Kind Kind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func KindOf(err error) Kind {
	var de *Error
	if errors.As(err, &de) {
		return de.Kind
	}
	return KindUnknown
}

var (
	ErrInvalidName     = &Error{KindInvalid, "Cycle name must be 1 to 80 characters."}
	ErrInvalidLength   = &Error{KindInvalid, "Cycle length must be between one minute and 90 days."}
	ErrInvalidTask     = &Error{KindInvalid, "Pick a task to bet on."}
	ErrInvalidAppetite = &Error{KindInvalid, "Appetite must be small or big."}
	ErrCycleActive     = &Error{KindConflict, "A cycle is already active in this workspace."}
	ErrNoActiveCycle   = &Error{KindConflict, "There is no active cycle. Start one first."}
	ErrCycleClosed     = &Error{KindConflict, "This cycle is closed."}
	ErrCycleOverdue    = &Error{KindConflict, "The cycle deadline has passed. Start a new cycle."}
	ErrDuplicateBet    = &Error{KindConflict, "This task already has a bet in this cycle."}
	ErrBetNotActive    = &Error{KindConflict, "This bet is already closed."}
	ErrActiveBets      = &Error{KindConflict, "Some bets are still active."}
	ErrBetNotFound     = &Error{KindNotFound, "No bet for this task in the active cycle."}
	ErrTaskNotFound    = &Error{KindNotFound, "Task not found in this workspace."}
)
