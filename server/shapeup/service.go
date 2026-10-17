package shapeup

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

// BettingTableStep is the workflow step name whose tasks are offered first
// when placing a bet (see workflows/shape-up.yaml).
const BettingTableStep = "Betting table"

// Service applies the domain rules to Host state. One mutex serializes every
// read-modify-write, because Host state has no transactions and the breaker
// ticker runs concurrently with browser actions.
type Service struct {
	host  pluginsdk.Host
	store *Store
	now   func() time.Time
	newID func() string
	mu    sync.Mutex

	// afterWorkspace, when set by a test, runs after each workspace of a
	// breaker run.
	afterWorkspace func()
}

func NewService(host pluginsdk.Host, now func() time.Time, newID func() string) *Service {
	return &Service{host: host, store: NewStore(host), now: now, newID: newID}
}

// SetHost swaps the Host after a reconnect, waiting for any operation in
// flight.
func (s *Service) SetHost(host pluginsdk.Host) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.host = host
	s.store = NewStore(host)
}

// Snapshot is what the betting table page renders.
type Snapshot struct {
	Now      time.Time     `json:"now"`
	Active   *Cycle        `json:"active"`
	Overdue  bool          `json:"overdue"`
	TimeLeft time.Duration `json:"-"`
	History  []Cycle       `json:"history"`
}

// Candidate is a task that can receive a bet.
type Candidate struct {
	TaskID         string `json:"task_id"`
	Title          string `json:"title"`
	StepName       string `json:"step_name"`
	AtBettingTable bool   `json:"at_betting_table"`
}

// Snapshot never fails because of the breaker or the archived-task check:
// both are retried on the next run, and the page must keep rendering.
func (s *Service) Snapshot(ctx context.Context, workspaceID string) (Snapshot, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.breakWorkspace(ctx, workspaceID); err != nil {
		log.Printf("circuit breaker: workspace %s: %v", workspaceID, err)
	}
	now := s.now()
	active, err := s.store.Active(ctx, workspaceID)
	if err != nil {
		return Snapshot{}, err
	}
	if err := s.closeArchivedBets(ctx, workspaceID, active); err != nil {
		log.Printf("closing archived bets: workspace %s: %v", workspaceID, err)
	}
	history, err := s.store.History(ctx, workspaceID)
	if err != nil {
		return Snapshot{}, err
	}
	if len(history) > MaxHistory {
		history = history[:MaxHistory]
	}
	snap := Snapshot{Now: now.UTC(), Active: active, History: history}
	if active != nil {
		snap.Overdue = active.Overdue(now)
		snap.TimeLeft = active.TimeLeft(now)
	}
	return snap, nil
}

func (s *Service) StartCycle(ctx context.Context, workspaceID, name string, days float64) (*Cycle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.breakWorkspace(ctx, workspaceID); err != nil {
		return nil, err
	}
	active, err := s.store.Active(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	if active != nil && active.Overdue(s.now()) {
		// The breaker could not finish (for example a missing grant); a new
		// cycle must not hide the unfinished cut.
		return nil, ErrCycleStuck
	}
	c, err := StartCycle(active, s.newID(), name, days, s.now())
	if err != nil {
		return nil, err
	}
	if err := s.store.SaveActive(ctx, workspaceID, c); err != nil {
		return nil, err
	}
	return c, nil
}

func (s *Service) PlaceBet(ctx context.Context, workspaceID, taskID string, appetite Appetite) error {
	return s.withActive(ctx, workspaceID, func(c *Cycle) error {
		task, err := s.liveTask(ctx, workspaceID, strings.TrimSpace(taskID))
		if err != nil {
			return err
		}
		return c.PlaceBet(task.ID, task.Title, appetite, s.now())
	})
}

func (s *Service) MarkShipped(ctx context.Context, workspaceID, taskID string) error {
	return s.withActive(ctx, workspaceID, func(c *Cycle) error {
		return c.MarkShipped(strings.TrimSpace(taskID), s.now())
	})
}

// CutNow archives the bet's task and marks the bet cut. When the archive
// fails the bet stays active and the error says why.
func (s *Service) CutNow(ctx context.Context, workspaceID, taskID string) error {
	return s.withActive(ctx, workspaceID, func(c *Cycle) error {
		bet := c.Bet(taskID)
		if bet == nil {
			return ErrBetNotFound
		}
		if bet.Status != BetActive {
			return ErrBetNotActive
		}
		tasks, err := s.listTasks(ctx, workspaceID, true)
		if err != nil {
			return err
		}
		if reason := s.archive(ctx, workspaceID, c.ID, tasks[taskID]); reason != "" {
			return fmt.Errorf("%w: %s", ErrArchiveFailed, reason)
		}
		return c.Cut(taskID, s.now())
	})
}

// Candidates lists the workspace's live tasks without a bet in the active
// cycle, tasks at the betting table first, then by title.
func (s *Service) Candidates(ctx context.Context, workspaceID string) ([]Candidate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	active, err := s.store.Active(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	tasks, err := s.listTasks(ctx, workspaceID, false)
	if err != nil {
		return nil, err
	}
	stepNames := map[string]string{}
	loaded := map[string]bool{}
	out := make([]Candidate, 0, len(tasks))
	for _, t := range tasks {
		if active != nil && active.Bet(t.ID) != nil {
			continue
		}
		if t.WorkflowID != "" && !loaded[t.WorkflowID] {
			loaded[t.WorkflowID] = true
			steps, err := s.host.Workflows().ListSteps(ctx, t.WorkflowID)
			if err != nil {
				return nil, fmt.Errorf("listing workflow steps: %w", err)
			}
			for _, st := range steps {
				stepNames[st.ID] = st.Name
			}
		}
		step := stepNames[t.WorkflowStepID]
		out = append(out, Candidate{
			TaskID:         t.ID,
			Title:          t.Title,
			StepName:       step,
			AtBettingTable: strings.EqualFold(step, BettingTableStep),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].AtBettingTable != out[j].AtBettingTable {
			return out[i].AtBettingTable
		}
		return strings.ToLower(out[i].Title) < strings.ToLower(out[j].Title)
	})
	return out, nil
}

// withActive runs fn on the workspace's active cycle after the breaker had
// its chance, and saves the cycle when fn succeeds.
func (s *Service) withActive(ctx context.Context, workspaceID string, fn func(*Cycle) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.breakWorkspace(ctx, workspaceID); err != nil {
		return err
	}
	c, err := s.store.Active(ctx, workspaceID)
	if err != nil {
		return err
	}
	if c == nil {
		return ErrNoActiveCycle
	}
	if err := s.closeArchivedBets(ctx, workspaceID, c); err != nil {
		return err
	}
	if err := fn(c); err != nil {
		return err
	}
	return s.store.SaveActive(ctx, workspaceID, c)
}

// closeArchivedBets marks cut every active bet whose task is already archived
// and saves the cycle. This happens when a "Cut now" reply timed out after the
// Host had archived the task, or when someone archived it by hand.
func (s *Service) closeArchivedBets(ctx context.Context, workspaceID string, c *Cycle) error {
	if c == nil || len(c.ActiveBets()) == 0 {
		return nil
	}
	tasks, err := s.listTasks(ctx, workspaceID, true)
	if err != nil {
		return err
	}
	changed := false
	for _, bet := range c.ActiveBets() {
		if t := tasks[bet.TaskID]; t != nil && t.ArchivedAt != nil {
			if err := c.Cut(bet.TaskID, s.now()); err != nil {
				return err
			}
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return s.store.SaveActive(ctx, workspaceID, c)
}

func (s *Service) liveTask(ctx context.Context, workspaceID, taskID string) (*pluginsdk.Task, error) {
	if taskID == "" {
		return nil, ErrInvalidTask
	}
	tasks, err := s.listTasks(ctx, workspaceID, false)
	if err != nil {
		return nil, err
	}
	t, ok := tasks[taskID]
	if !ok {
		return nil, ErrTaskNotFound
	}
	return t, nil
}

// listTasks returns the workspace's tasks by id. The task list is used rather
// than Tasks().Get because Get reports a missing task only as an opaque gRPC
// error, while a missing entry here is unambiguous.
func (s *Service) listTasks(ctx context.Context, workspaceID string, includeArchived bool) (map[string]*pluginsdk.Task, error) {
	out := map[string]*pluginsdk.Task{}
	page := pluginsdk.Page{Limit: 200}
	for {
		tasks, info, err := s.host.Tasks().List(ctx, pluginsdk.TaskFilter{
			WorkspaceIDs:    []string{workspaceID},
			IncludeArchived: includeArchived,
		}, page)
		if err != nil {
			return nil, fmt.Errorf("listing tasks: %w", err)
		}
		for i := range tasks {
			if tasks[i].WorkspaceID == workspaceID {
				out[tasks[i].ID] = &tasks[i]
			}
		}
		if info == nil || !info.HasMore || info.NextCursor == "" {
			return out, nil
		}
		page.Cursor = info.NextCursor
	}
}
