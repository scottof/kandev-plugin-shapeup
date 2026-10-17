package shapeup

import (
	"context"
	"errors"
	"fmt"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

const archiveMethod = "ArchiveTaskExact"

// ErrArchiveFailed wraps the reason a task could not be archived.
var ErrArchiveFailed = &Error{KindConflict, "The task could not be archived"}

// Messages stored on a bet and shown on the betting table page.
const (
	reasonNoExact  = "This kandev version cannot archive tasks for plugins. Archive the task by hand to finish the cut."
	reasonNoGrant  = "Archiving needs the host.v2.write:tasks grant: open Settings > Plugins > Shape Up > Workspace access."
	reasonConflict = "kandev refused to archive this task (it changed, or another plugin manages it). It is retried when the task changes, or archive it by hand to finish the cut."
	reasonRejected = "kandev rejected the archive request. Archive the task by hand to finish the cut."
	reasonRetry    = "Archiving did not complete. Retrying."
)

// RunBreaker enforces the deadline of every indexed workspace. It is safe to
// run any number of times: it runs on a timer, at plugin start and before
// every page action.
func (s *Service) RunBreaker(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.store.Workspaces(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := s.breakWorkspace(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// breakWorkspace cuts every active bet of an overdue cycle and closes it.
// A bet whose task cannot be archived stays active with its reason, so the
// cycle stays open and the next run retries. Callers hold s.mu.
func (s *Service) breakWorkspace(ctx context.Context, workspaceID string) error {
	c, err := s.store.Active(ctx, workspaceID)
	if err != nil || c == nil {
		return err
	}
	now := s.now()
	if !c.Overdue(now) {
		return nil
	}
	if active := c.ActiveBets(); len(active) > 0 {
		tasks, err := s.listTasks(ctx, workspaceID, true)
		if err != nil {
			return err
		}
		for _, bet := range active {
			if reason := s.archive(ctx, workspaceID, c.ID, tasks[bet.TaskID]); reason != "" {
				c.Bet(bet.TaskID).Error = reason
				continue
			}
			if err := c.Cut(bet.TaskID, now); err != nil {
				return err
			}
		}
	}
	if len(c.ActiveBets()) > 0 {
		return s.store.SaveActive(ctx, workspaceID, c)
	}
	if err := c.Close(now); err != nil {
		return err
	}
	return s.store.Archive(ctx, workspaceID, c)
}

// archive archives task through the exact Host v2 command and returns an
// empty reason on success. A nil or already archived task counts as done:
// there is nothing left to stop.
func (s *Service) archive(ctx context.Context, workspaceID, cycleID string, task *pluginsdk.Task) string {
	if task == nil || task.ArchivedAt != nil {
		return ""
	}
	exact, ok := pluginsdk.HostV2(s.host)
	if !ok {
		return reasonNoExact
	}
	commands, ok := pluginsdk.HostTaskCommands(s.host)
	if !ok {
		return reasonNoExact
	}
	capability, err := exact.GetCapabilityContext(ctx, workspaceID)
	if err != nil {
		return reasonRetry
	}
	if !archiveAuthorized(capability) {
		return reasonNoGrant
	}
	// The Host records every result except UNAVAILABLE against the key and
	// replays it for the same payload, and answers a reused key with a
	// different payload as a conflict. The key therefore covers everything in
	// the payload that can change: the task version and the approval
	// revision. An unchanged request replays its old result without a new
	// Host receipt; a changed task or a re-saved grant gets a fresh attempt.
	key := fmt.Sprintf("shapeup:cut:%s:%s:%s:%d", cycleID, task.ID, task.ResourceVersion, capability.ApprovalRevision)
	result, _, err := commands.Archive(ctx, pluginsdk.ExactTaskArchive{
		RequestID:               key,
		WorkspaceID:             workspaceID,
		TaskID:                  task.ID,
		IdempotencyKey:          key,
		ExpectedResourceVersion: task.ResourceVersion,
		ApprovalRevision:        capability.ApprovalRevision,
		ManifestDigest:          capability.ManifestDigest,
	})
	if err != nil || result == nil {
		return reasonRetry
	}
	switch result.Status {
	case pluginsdk.CommandApplied, pluginsdk.CommandAlreadyApplied, pluginsdk.CommandNoChange, pluginsdk.CommandNotFound:
		return ""
	case pluginsdk.CommandDenied:
		return reasonNoGrant
	case pluginsdk.CommandConflict:
		return reasonConflict
	case pluginsdk.CommandInvalid, pluginsdk.CommandUnsupported:
		return reasonRejected
	default:
		return reasonRetry
	}
}

func archiveAuthorized(c *pluginsdk.CapabilityContext) bool {
	if c == nil {
		return false
	}
	for _, op := range c.Operations {
		if op.Method == archiveMethod {
			return op.Supported && op.Authorized
		}
	}
	return false
}
