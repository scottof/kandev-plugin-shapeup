package shapeup

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
)

const archiveMethod = "ArchiveTaskExact"

const (
	// ArchiveTimeout bounds one archive call. Archiving stops the task's
	// agents and snapshots their worktrees, which can take much longer than a
	// state read, so every call gets its own budget instead of sharing one.
	ArchiveTimeout = 25 * time.Second
	// stateTimeout bounds the state and task reads and writes of one
	// workspace run, outside the archive calls.
	stateTimeout = 30 * time.Second
	// saveTimeout bounds the save that ends a breaker run.
	saveTimeout = 5 * time.Second
)

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
// every page action. The lock is taken per workspace, so page actions can run
// between two slow workspaces instead of waiting for the whole run.
func (s *Service) RunBreaker(ctx context.Context) error {
	ids, err := s.indexedWorkspaces(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if err := s.breakIndexedWorkspace(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("workspace %s: %w", id, err))
		}
		if s.afterWorkspace != nil {
			s.afterWorkspace()
		}
	}
	return errors.Join(errs...)
}

func (s *Service) indexedWorkspaces(ctx context.Context) ([]string, error) {
	ctx, cancel := context.WithTimeout(ctx, stateTimeout)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.store.Workspaces(ctx)
}

func (s *Service) breakIndexedWorkspace(ctx context.Context, workspaceID string) error {
	ctx, cancel := context.WithTimeout(ctx, stateTimeout)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.breakWorkspace(ctx, workspaceID, s.newTaskList(workspaceID))
}

// breakWorkspace cuts every active bet of an overdue cycle and closes it.
// A bet whose task cannot be archived stays active with its reason, so the
// cycle stays open and the next run retries. Callers hold s.mu.
func (s *Service) breakWorkspace(ctx context.Context, workspaceID string, list *taskList) error {
	c, err := s.store.Active(ctx, workspaceID)
	if err != nil || c == nil {
		return err
	}
	now := s.now()
	if !c.Overdue(now) {
		return nil
	}
	if active := c.ActiveBets(); len(active) > 0 {
		tasks, err := list.all(ctx)
		if err != nil {
			return err
		}
		for _, bet := range active {
			// The caller holds s.mu, and each archive may take its full budget:
			// past the caller's deadline the bets left wait for the next run.
			if ctx.Err() != nil {
				break
			}
			if reason := s.archive(ctx, workspaceID, c.ID, tasks[bet.TaskID]); reason != "" {
				c.Bet(bet.TaskID).Error = reason
				continue
			}
			if err := c.Cut(bet.TaskID, now); err != nil {
				return err
			}
		}
	}
	// Saved even when the caller gave up, so finished cuts and the reasons of
	// failed ones are never lost.
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), saveTimeout)
	defer cancel()
	if len(c.ActiveBets()) > 0 {
		return s.store.SaveActive(saveCtx, workspaceID, c)
	}
	if err := c.Close(now); err != nil {
		return err
	}
	return s.store.Archive(saveCtx, workspaceID, c)
}

// archive archives task through the exact Host v2 command and returns an
// empty reason on success. A nil or already archived task counts as done:
// there is nothing left to stop.
func (s *Service) archive(ctx context.Context, workspaceID, cycleID string, task *pluginsdk.Task) string {
	if task == nil || task.ArchivedAt != nil {
		return ""
	}
	// Detached from the caller: an archive cut short by a page action's
	// deadline would still complete on the Host, unseen by the plugin.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), ArchiveTimeout)
	defer cancel()
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
	// different payload as a conflict. The key therefore covers the task
	// version, which is in the payload, so a changed task gets a fresh attempt
	// instead of a conflict. The approval revision is not in the payload: it is
	// in the key so a re-saved grant gets a fresh attempt instead of a replay
	// of the result recorded under the old grant.
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
