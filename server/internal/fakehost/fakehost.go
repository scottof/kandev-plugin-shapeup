// Package fakehost is an in-memory pluginsdk.Host for tests: Host state, the
// task and workflow readers, and the exact (Host v2) archive command. It is
// imported only from _test.go files, so it never ships in the plugin binary.
package fakehost

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

// ManifestDigest is the digest GetCapabilityContext reports. The real Host
// rejects an archive whose digest does not match the installed manifest.
const ManifestDigest = "0000000000000000000000000000000000000000000000000000000000000000"

// Host stores state as JSON round-tripped maps, so numbers come back as
// float64 exactly like the real protobuf Struct conversion.
type Host struct {
	pluginsdk.UnimplementedHostData

	mu    sync.Mutex
	state map[string]map[string]any
	tasks map[string]*pluginsdk.Task
	steps map[string][]pluginsdk.WorkflowStep

	// Granted mirrors the workspace grant for host.v2.write:tasks, and
	// ApprovalRevision its revision (it changes each time the grant is saved).
	Granted          bool
	ApprovalRevision uint64
	// NoExact hides the Host v2 extension, like an older Kandev host.
	NoExact bool
	// ArchiveCalls counts archive commands that reached the Host.
	ArchiveCalls int
	// Replays counts archive calls answered from a recorded receipt.
	Replays int
	// ArchiveStatus, when set, is the result of the next newly admitted
	// archive instead of archiving.
	ArchiveStatus pluginsdk.CommandStatus
	// StateErr, when set, fails every state write.
	StateErr error

	receipts map[string]receipt
	clock    int
}

func New() *Host {
	return &Host{
		state:            map[string]map[string]any{},
		tasks:            map[string]*pluginsdk.Task{},
		steps:            map[string][]pluginsdk.WorkflowStep{},
		receipts:         map[string]receipt{},
		Granted:          true,
		ApprovalRevision: 7,
	}
}

func stateKey(scope, scopeID, key string) string { return scope + "\x00" + scopeID + "\x00" + key }

func roundTrip(v map[string]any) map[string]any {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		panic(err)
	}
	return out
}

func (h *Host) GetState(_ context.Context, scope, scopeID, key string) (map[string]any, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.state[stateKey(scope, scopeID, key)]
	if !ok {
		return nil, false, nil
	}
	return roundTrip(v), true, nil
}

func (h *Host) SetState(_ context.Context, scope, scopeID, key string, value map[string]any) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.StateErr != nil {
		return h.StateErr
	}
	h.state[stateKey(scope, scopeID, key)] = roundTrip(value)
	return nil
}

func (h *Host) DeleteState(_ context.Context, scope, scopeID, key string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.StateErr != nil {
		return h.StateErr
	}
	delete(h.state, stateKey(scope, scopeID, key))
	return nil
}

func (h *Host) ListState(_ context.Context, scope, scopeID string) ([]pluginsdk.StateEntry, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	prefix := scope + "\x00" + scopeID + "\x00"
	var out []pluginsdk.StateEntry
	for k, v := range h.state {
		if strings.HasPrefix(k, prefix) {
			out = append(out, pluginsdk.StateEntry{Key: strings.TrimPrefix(k, prefix), Value: roundTrip(v)})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

// SetRaw stores a value without validation, to simulate foreign or old data.
func (h *Host) SetRaw(scope, scopeID, key string, value map[string]any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.state[stateKey(scope, scopeID, key)] = roundTrip(value)
}

func (h *Host) GetConfig(context.Context) (map[string]any, error)    { return map[string]any{}, nil }
func (h *Host) RevealSecret(context.Context, string) (string, error) { return "", nil }
func (h *Host) GetSecret(context.Context, string) (string, bool, error) {
	return "", false, nil
}
func (h *Host) SetSecret(context.Context, string, string) error         { return nil }
func (h *Host) DeleteSecret(context.Context, string) error              { return nil }
func (h *Host) EmitEvent(context.Context, string, map[string]any) error { return nil }

// AddTask registers a live task. Its ResourceVersion changes on every write.
func (h *Host) AddTask(workspaceID, workflowID, stepID, id, title string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks[id] = &pluginsdk.Task{
		ID: id, WorkspaceID: workspaceID, WorkflowID: workflowID, WorkflowStepID: stepID,
		Title: title, ResourceVersion: h.nextVersion(),
	}
}

// AddStep registers a workflow step for Workflows().ListSteps.
func (h *Host) AddStep(workflowID, stepID, name string, position int32) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.steps[workflowID] = append(h.steps[workflowID], pluginsdk.WorkflowStep{
		ID: stepID, WorkflowID: workflowID, Name: name, Position: position,
	})
}

// ArchiveDirectly archives a task outside the plugin, as a human would.
func (h *Host) ArchiveDirectly(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	at := "2026-01-01T00:00:00Z"
	h.tasks[id].ArchivedAt = &at
	h.tasks[id].ResourceVersion = h.nextVersion()
}

// TouchTask changes a task's resource version, as any edit does.
func (h *Host) TouchTask(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.tasks[id].ResourceVersion = h.nextVersion()
}

// DeleteTask removes a task entirely.
func (h *Host) DeleteTask(id string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.tasks, id)
}

// Archived reports whether a task is archived.
func (h *Host) Archived(id string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	t, ok := h.tasks[id]
	return ok && t.ArchivedAt != nil
}

func (h *Host) nextVersion() string {
	h.clock++
	return time.Date(2026, 1, 1, 0, 0, h.clock, 0, time.UTC).Format(time.RFC3339Nano)
}

func (h *Host) Tasks() pluginsdk.TaskReader         { return taskReader{h} }
func (h *Host) Workflows() pluginsdk.WorkflowReader { return workflowReader{h} }

type taskReader struct{ h *Host }

func (r taskReader) List(_ context.Context, filter pluginsdk.TaskFilter, _ pluginsdk.Page) ([]pluginsdk.Task, *pluginsdk.PageInfo, error) {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	var out []pluginsdk.Task
	for _, t := range r.h.tasks {
		if len(filter.WorkspaceIDs) > 0 && !contains(filter.WorkspaceIDs, t.WorkspaceID) {
			continue
		}
		if t.ArchivedAt != nil && !filter.IncludeArchived {
			continue
		}
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, &pluginsdk.PageInfo{}, nil
}

func (r taskReader) Get(_ context.Context, id string) (*pluginsdk.Task, error) {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	t, ok := r.h.tasks[id]
	if !ok {
		return nil, errors.New("task not found")
	}
	cp := *t
	return &cp, nil
}

func (taskReader) Create(context.Context, pluginsdk.CreateTaskInput) (*pluginsdk.Task, error) {
	return nil, errors.New("not implemented")
}
func (taskReader) Update(context.Context, pluginsdk.UpdateTaskInput) (*pluginsdk.Task, error) {
	return nil, errors.New("not implemented")
}
func (taskReader) Move(context.Context, pluginsdk.MoveTaskInput) (*pluginsdk.MoveTaskOutcome, error) {
	return nil, errors.New("not implemented")
}

type workflowReader struct{ h *Host }

func (workflowReader) List(context.Context, string, pluginsdk.Page) ([]pluginsdk.Workflow, *pluginsdk.PageInfo, error) {
	return nil, &pluginsdk.PageInfo{}, nil
}

func (r workflowReader) ListSteps(_ context.Context, workflowID string) ([]pluginsdk.WorkflowStep, error) {
	r.h.mu.Lock()
	defer r.h.mu.Unlock()
	return append([]pluginsdk.WorkflowStep(nil), r.h.steps[workflowID]...), nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}

// Exact is the Host v2 view of a Host. Tests pass Exact(h) where the plugin
// would receive the real gRPC host, which implements both extensions.
type Exact struct{ *Host }

// AsHost returns h with the Host v2 extensions unless NoExact is set.
func (h *Host) AsHost() pluginsdk.Host {
	if h.NoExact {
		return h
	}
	return Exact{h}
}

func (e Exact) GetCapabilityContext(_ context.Context, workspaceID string) (*pluginsdk.CapabilityContext, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	revision := uint64(0)
	if e.Granted {
		revision = e.ApprovalRevision
	}
	return &pluginsdk.CapabilityContext{
		WorkspaceID: workspaceID, ManifestDigest: ManifestDigest, ApprovalRevision: revision,
		Operations: []pluginsdk.CapabilityOperation{{
			Method: "ArchiveTaskExact", CapabilityID: "host.v2.write:tasks",
			Supported: true, Authorized: e.Granted,
		}},
	}, nil
}

func (Exact) UpdateTaskExact(context.Context, pluginsdk.ExactTaskUpdate) (*pluginsdk.CommandResult, *pluginsdk.Task, error) {
	return nil, nil, errors.New("not implemented")
}

func (Exact) ManagedAgentConversations() pluginsdk.ManagedAgentConversationManager { return nil }

func (e Exact) TaskCommands() pluginsdk.ExactTaskCommandManager { return commands{h: e.Host} }

// commands implements only Archive; the embedded nil interface panics on any
// other command, which would be a test bug.
type commands struct {
	h *Host
	pluginsdk.ExactTaskCommandManager
}

// Archive mirrors ArchiveTaskExact in kandev v0.97.0
// (internal/plugins/host_exact_tasks.go): a denied call is rejected before a
// receipt exists; otherwise the result is recorded against the idempotency
// key and replayed for the same payload, a different payload under that key
// is a conflict, and only UNAVAILABLE is left unrecorded so it can be retried.
func (c commands) Archive(_ context.Context, in pluginsdk.ExactTaskArchive) (*pluginsdk.CommandResult, *pluginsdk.Task, error) {
	h := c.h
	h.mu.Lock()
	defer h.mu.Unlock()
	h.ArchiveCalls++
	if in.RequestID == "" || in.IdempotencyKey == "" || in.WorkspaceID == "" || in.ExpectedResourceVersion == "" {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandInvalid, Reason: "invalid_request"}, nil, nil
	}
	if !h.Granted || in.ApprovalRevision != h.ApprovalRevision || in.ManifestDigest != ManifestDigest {
		return &pluginsdk.CommandResult{Status: pluginsdk.CommandDenied, Reason: "approval_missing"}, nil, nil
	}
	// Same fields as exactTaskArchiveDigest: the approval revision and manifest
	// digest are checked at admission but are not part of the payload digest.
	digest := strings.Join([]string{in.WorkspaceID, in.TaskID, in.ExpectedResourceVersion,
		in.ManagementInstanceKey, strconv.FormatInt(in.ExpectedClaimGeneration, 10)}, "|")
	if r, ok := h.receipts[in.IdempotencyKey]; ok {
		if r.digest != digest {
			return &pluginsdk.CommandResult{Status: pluginsdk.CommandConflict, Reason: "idempotency_payload_mismatch"}, nil, nil
		}
		h.Replays++
		return &pluginsdk.CommandResult{Status: r.status, Reason: "replayed"}, nil, nil
	}
	status := h.ArchiveStatus
	h.ArchiveStatus = ""
	if status == "" {
		t, ok := h.tasks[in.TaskID]
		switch {
		case !ok:
			status = pluginsdk.CommandNotFound
		case t.WorkspaceID != in.WorkspaceID || t.ResourceVersion != in.ExpectedResourceVersion || t.ArchivedAt != nil:
			status = pluginsdk.CommandConflict
		default:
			at := "2026-01-01T00:00:00Z"
			t.ArchivedAt = &at
			t.ResourceVersion = h.nextVersion()
			status = pluginsdk.CommandApplied
		}
	}
	if status != pluginsdk.CommandUnavailable {
		h.receipts[in.IdempotencyKey] = receipt{digest: digest, status: status}
	}
	var out *pluginsdk.Task
	if t, ok := h.tasks[in.TaskID]; ok && status == pluginsdk.CommandApplied {
		cp := *t
		out = &cp
	}
	return &pluginsdk.CommandResult{Status: status}, out, nil
}

type receipt struct {
	digest string
	status pluginsdk.CommandStatus
}

var (
	_ pluginsdk.Host                 = (*Host)(nil)
	_ pluginsdk.ExactHost            = Exact{}
	_ pluginsdk.ExactTaskCommandHost = Exact{}
)
