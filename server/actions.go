package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/kandev/kandev/pkg/pluginsdk"

	"kandev-plugin-shapeup/server/shapeup"
)

// The manifest declares each key under actions[] with scope: workspace, so
// the host has already authenticated the caller and verified the workspace.
const (
	actionStateGet   = "state.get"
	actionCycleStart = "cycle.start"
	actionBetPlace   = "bet.place"
	actionBetShip    = "bet.ship"
	actionBetCut     = "bet.cut"
	actionCandidates = "tasks.candidates"
)

type betBody struct {
	TaskID   string           `json:"task_id"`
	Appetite shapeup.Appetite `json:"appetite"`
}

type cycleBody struct {
	Name string `json:"name"`
	Days int    `json:"days"`
}

func (p *shapeupPlugin) HandleAction(ctx context.Context, req *pluginsdk.PluginActionRequest) (*pluginsdk.PluginActionResponse, error) {
	svc := p.service()
	if svc == nil {
		return failure(pluginsdk.ActionErrorUnavailable, "The plugin is starting. Try again in a moment."), nil
	}
	// Only the verified context names the workspace; the body never does.
	ws := req.Context.WorkspaceID
	if ws == "" {
		return failure(pluginsdk.ActionErrorInvalidArgument, "Open this page from a workspace."), nil
	}

	switch req.ActionKey {
	case actionStateGet:
		return p.snapshot(ctx, svc, ws)
	case actionCandidates:
		tasks, err := svc.Candidates(ctx, ws)
		if err != nil {
			return domainFailure(err), nil
		}
		return success(map[string]any{"tasks": tasks})
	case actionCycleStart:
		var body cycleBody
		if !decodeBody(req.Body, &body) {
			return badBody(), nil
		}
		if _, err := svc.StartCycle(ctx, ws, body.Name, body.Days); err != nil {
			return domainFailure(err), nil
		}
	case actionBetPlace, actionBetShip, actionBetCut:
		var body betBody
		if !decodeBody(req.Body, &body) {
			return badBody(), nil
		}
		var err error
		switch req.ActionKey {
		case actionBetPlace:
			err = svc.PlaceBet(ctx, ws, body.TaskID, body.Appetite)
		case actionBetShip:
			err = svc.MarkShipped(ctx, ws, body.TaskID)
		default:
			err = svc.CutNow(ctx, ws, body.TaskID)
		}
		if err != nil {
			return domainFailure(err), nil
		}
	default:
		return failure(pluginsdk.ActionErrorNotFound, "Unknown action."), nil
	}
	// Mutations answer with the fresh state, so the page needs one round trip.
	return p.snapshot(ctx, svc, ws)
}

func (p *shapeupPlugin) snapshot(ctx context.Context, svc *shapeup.Service, ws string) (*pluginsdk.PluginActionResponse, error) {
	snap, err := svc.Snapshot(ctx, ws)
	if err != nil {
		return domainFailure(err), nil
	}
	return success(snap)
}

// decodeBody accepts an empty body and ignores unknown fields.
func decodeBody(raw []byte, into any) bool {
	if len(raw) == 0 {
		return true
	}
	return json.Unmarshal(raw, into) == nil
}

func badBody() *pluginsdk.PluginActionResponse {
	return failure(pluginsdk.ActionErrorInvalidArgument, "The request body is not valid.")
}

// domainFailure maps domain errors to their status and message. Anything
// else is a Host or state failure: it is logged and the UI gets a generic
// message, never the raw error text.
func domainFailure(err error) *pluginsdk.PluginActionResponse {
	var de *shapeup.Error
	if errors.As(err, &de) {
		code := pluginsdk.ActionErrorConflict
		switch de.Kind {
		case shapeup.KindInvalid:
			code = pluginsdk.ActionErrorInvalidArgument
		case shapeup.KindNotFound:
			code = pluginsdk.ActionErrorNotFound
		}
		return failure(code, err.Error())
	}
	log.Printf("action failed: %v", err)
	return failure(pluginsdk.ActionErrorUnavailable, "Something went wrong. Try again.")
}

func failure(code pluginsdk.ActionErrorCode, msg string) *pluginsdk.PluginActionResponse {
	body, _ := json.Marshal(map[string]string{"error": msg, "code": string(code)})
	return &pluginsdk.PluginActionResponse{
		Status:  pluginsdk.ActionErrorHTTPStatus(code),
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    body,
	}
}

func success(v any) (*pluginsdk.PluginActionResponse, error) {
	body, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return &pluginsdk.PluginActionResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"},
		Body:    body,
	}, nil
}
