package shapeup

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

// Host state layout. Every value carries "v" so the shape can evolve:
//
//	workspace/<id>/active      {v, cycle}  the active cycle, absent when none
//	workspace/<id>/cycle:<id>  {v, cycle}  a closed cycle (history)
//	instance//workspaces       {v, ids}    workspaces with an active cycle
//
// The index lets the circuit breaker find overdue cycles at startup without
// the api_read:workspaces capability.
const (
	stateVersion       = 1
	scopeWorkspace     = "workspace"
	scopeInstance      = "instance"
	keyActive          = "active"
	keyHistoryPrefix   = "cycle:"
	keyWorkspacesIndex = "workspaces"
)

var (
	ErrUnsupportedVersion = errors.New("shapeup: unsupported state version")
	ErrCycleNotClosed     = errors.New("shapeup: only a closed cycle can be archived")
)

// Store reads and writes cycles in Host state. Host state has no
// transactions; callers serialize writes (see Service).
type Store struct {
	host pluginsdk.Host
}

func NewStore(host pluginsdk.Host) *Store { return &Store{host: host} }

// Active returns the workspace's active cycle, nil when there is none.
func (s *Store) Active(ctx context.Context, workspaceID string) (*Cycle, error) {
	value, found, err := s.host.GetState(ctx, scopeWorkspace, workspaceID, keyActive)
	if err != nil {
		return nil, fmt.Errorf("reading active cycle: %w", err)
	}
	if !found {
		return nil, nil
	}
	return decodeCycle(value)
}

// SaveActive writes the active cycle and makes sure the workspace is indexed.
// The index comes first: an indexed workspace without a cycle is harmless,
// while a cycle missing from the index would escape the background breaker.
func (s *Store) SaveActive(ctx context.Context, workspaceID string, c *Cycle) error {
	value, err := encode(map[string]any{"cycle": c})
	if err != nil {
		return err
	}
	if err := s.updateIndex(ctx, workspaceID, true); err != nil {
		return err
	}
	if err := s.host.SetState(ctx, scopeWorkspace, workspaceID, keyActive, value); err != nil {
		return fmt.Errorf("writing active cycle: %w", err)
	}
	return nil
}

// Archive stores a closed cycle in the history, then removes it as the
// active one. The history write comes first, so a crash in between leaves the
// cycle active and the next breaker run archives it again under the same key.
func (s *Store) Archive(ctx context.Context, workspaceID string, c *Cycle) error {
	if c.Status != CycleClosed {
		return ErrCycleNotClosed
	}
	value, err := encode(map[string]any{"cycle": c})
	if err != nil {
		return err
	}
	if err := s.host.SetState(ctx, scopeWorkspace, workspaceID, keyHistoryPrefix+c.ID, value); err != nil {
		return fmt.Errorf("writing cycle history: %w", err)
	}
	if err := s.host.DeleteState(ctx, scopeWorkspace, workspaceID, keyActive); err != nil {
		return fmt.Errorf("clearing active cycle: %w", err)
	}
	return s.updateIndex(ctx, workspaceID, false)
}

// History returns the closed cycles of a workspace, newest deadline first.
func (s *Store) History(ctx context.Context, workspaceID string) ([]Cycle, error) {
	entries, err := s.host.ListState(ctx, scopeWorkspace, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("listing cycle history: %w", err)
	}
	var out []Cycle
	for _, e := range entries {
		if !strings.HasPrefix(e.Key, keyHistoryPrefix) {
			continue
		}
		c, err := decodeCycle(e.Value)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", e.Key, err)
		}
		out = append(out, *c)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].End.After(out[j].End) })
	return out, nil
}

// Workspaces returns the indexed workspaces, sorted.
func (s *Store) Workspaces(ctx context.Context) ([]string, error) {
	value, found, err := s.host.GetState(ctx, scopeInstance, "", keyWorkspacesIndex)
	if err != nil {
		return nil, fmt.Errorf("reading workspace index: %w", err)
	}
	if !found {
		return nil, nil
	}
	var doc struct {
		IDs []string `json:"ids"`
	}
	if err := decode(value, &doc); err != nil {
		return nil, err
	}
	sort.Strings(doc.IDs)
	return doc.IDs, nil
}

func (s *Store) updateIndex(ctx context.Context, workspaceID string, present bool) error {
	ids, err := s.Workspaces(ctx)
	if err != nil {
		return err
	}
	next := make([]string, 0, len(ids)+1)
	listed := false
	for _, id := range ids {
		if id == workspaceID {
			listed = true
			if !present {
				continue
			}
		}
		next = append(next, id)
	}
	if listed == present {
		return nil
	}
	if present {
		next = append(next, workspaceID)
	}
	value, err := encode(map[string]any{"ids": next})
	if err != nil {
		return err
	}
	if err := s.host.SetState(ctx, scopeInstance, "", keyWorkspacesIndex, value); err != nil {
		return fmt.Errorf("writing workspace index: %w", err)
	}
	return nil
}

func decodeCycle(value map[string]any) (*Cycle, error) {
	var doc struct {
		Cycle Cycle `json:"cycle"`
	}
	if err := decode(value, &doc); err != nil {
		return nil, err
	}
	return &doc.Cycle, nil
}

// encode turns fields into a state value through JSON, which is the shape
// the Host stores anyway.
func encode(fields map[string]any) (map[string]any, error) {
	fields["v"] = stateVersion
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encoding state: %w", err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("encoding state: %w", err)
	}
	return out, nil
}

func decode(value map[string]any, into any) error {
	// Host state numbers arrive as float64.
	if v, _ := value["v"].(float64); v != stateVersion {
		return fmt.Errorf("%w: %v", ErrUnsupportedVersion, value["v"])
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("decoding state: %w", err)
	}
	if err := json.Unmarshal(data, into); err != nil {
		return fmt.Errorf("decoding state: %w", err)
	}
	return nil
}
