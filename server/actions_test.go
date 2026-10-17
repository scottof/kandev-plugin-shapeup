package main

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"
	"github.com/stretchr/testify/require"

	"kandev-plugin-shapeup/server/internal/fakehost"
)

var t0 = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

type harness struct {
	host   *fakehost.Host
	plugin *shapeupPlugin
	now    time.Time
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	h := &harness{host: fakehost.New(), now: t0}
	h.plugin = newPlugin(func() time.Time { return h.now })
	h.plugin.breakerInterval = 0 // no background loop in action tests
	h.plugin.SetHost(h.host.AsHost())
	return h
}

func (h *harness) call(t *testing.T, key, workspaceID string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		var err error
		raw, err = json.Marshal(body)
		require.NoError(t, err)
	}
	resp, err := h.plugin.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: key,
		Context:   pluginsdk.VerifiedActionContext{ActorID: "u1", WorkspaceID: workspaceID},
		Body:      raw,
	})
	require.NoError(t, err)
	require.Equal(t, "application/json", resp.Headers["Content-Type"])
	var out map[string]any
	require.NoError(t, json.Unmarshal(resp.Body, &out))
	status := resp.Status
	if status == 0 {
		status = 200
	}
	return status, out
}

func active(t *testing.T, out map[string]any) map[string]any {
	t.Helper()
	a, ok := out["active"].(map[string]any)
	require.True(t, ok, "expected an active cycle in %v", out)
	return a
}

func TestActions_FullCycle(t *testing.T) {
	h := newHarness(t)
	h.host.AddStep("wf1", "s-bet", "Betting table", 2)
	h.host.AddTask("ws1", "wf1", "s-bet", "t1", "Search pitch")
	h.host.AddTask("ws1", "wf1", "s-bet", "t2", "Export pitch")

	status, out := h.call(t, "state.get", "ws1", nil)
	require.Equal(t, 200, status)
	require.Nil(t, out["active"])
	require.Empty(t, out["history"])

	status, out = h.call(t, "cycle.start", "ws1", map[string]any{"name": "October", "days": 14})
	require.Equal(t, 200, status)
	require.Equal(t, "October", active(t, out)["name"])

	status, out = h.call(t, "tasks.candidates", "ws1", nil)
	require.Equal(t, 200, status)
	require.Len(t, out["tasks"], 2)

	for _, id := range []string{"t1", "t2"} {
		status, _ = h.call(t, "bet.place", "ws1", map[string]any{"task_id": id, "appetite": "small"})
		require.Equal(t, 200, status)
	}
	status, _ = h.call(t, "bet.ship", "ws1", map[string]any{"task_id": "t1"})
	require.Equal(t, 200, status)
	status, out = h.call(t, "bet.cut", "ws1", map[string]any{"task_id": "t2"})
	require.Equal(t, 200, status)
	require.True(t, h.host.Archived("t2"))

	bets := active(t, out)["bets"].([]any)
	require.Equal(t, "shipped", bets[0].(map[string]any)["status"])
	require.Equal(t, "cut", bets[1].(map[string]any)["status"])
}

func TestActions_WorkspaceComesFromVerifiedContext(t *testing.T) {
	h := newHarness(t)

	status, _ := h.call(t, "cycle.start", "ws1", map[string]any{"name": "Mine", "days": 7, "workspaceId": "ws2", "workspace_id": "ws2"})
	require.Equal(t, 200, status)

	_, out := h.call(t, "state.get", "ws2", nil)
	require.Nil(t, out["active"], "the body cannot choose the workspace")
	_, out = h.call(t, "state.get", "ws1", nil)
	require.Equal(t, "Mine", active(t, out)["name"])

	status, out = h.call(t, "state.get", "", nil)
	require.Equal(t, 400, status)
	require.NotEmpty(t, out["error"])
}

func TestActions_ErrorStatuses(t *testing.T) {
	h := newHarness(t)
	h.host.AddTask("ws1", "wf1", "s", "t1", "Task")

	tests := []struct {
		name   string
		key    string
		body   any
		status int
	}{
		{"unknown action", "nope", nil, 404},
		{"malformed body", "cycle.start", "not an object", 400},
		{"invalid length", "cycle.start", map[string]any{"name": "x", "days": 0}, 400},
		{"bet without cycle", "bet.place", map[string]any{"task_id": "t1", "appetite": "small"}, 409},
		{"ship without cycle", "bet.ship", map[string]any{"task_id": "t1"}, 409},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			status, out := h.call(t, tc.key, "ws1", tc.body)
			require.Equal(t, tc.status, status)
			require.NotEmpty(t, out["error"])
		})
	}

	_, _ = h.call(t, "cycle.start", "ws1", map[string]any{"name": "C", "days": 7})
	status, out := h.call(t, "cycle.start", "ws1", map[string]any{"name": "C", "days": 7})
	require.Equal(t, 409, status)
	require.Equal(t, "A cycle is already active in this workspace.", out["error"])

	status, _ = h.call(t, "bet.place", "ws1", map[string]any{"task_id": "missing", "appetite": "small"})
	require.Equal(t, 404, status)
	status, _ = h.call(t, "bet.place", "ws1", map[string]any{"task_id": "t1", "appetite": "huge"})
	require.Equal(t, 400, status)
}

func TestActions_CutWithoutGrantExplainsWhy(t *testing.T) {
	h := newHarness(t)
	h.host.AddTask("ws1", "wf1", "s", "t1", "Task")
	h.call(t, "cycle.start", "ws1", map[string]any{"name": "C", "days": 7})
	h.call(t, "bet.place", "ws1", map[string]any{"task_id": "t1", "appetite": "big"})
	h.host.Granted = false

	status, out := h.call(t, "bet.cut", "ws1", map[string]any{"task_id": "t1"})
	require.Equal(t, 409, status)
	require.Contains(t, out["error"], "Workspace access")
}

func TestActions_OverdueCycleIsBrokenBeforeTheAction(t *testing.T) {
	h := newHarness(t)
	h.host.AddTask("ws1", "wf1", "s", "t1", "Task")
	h.call(t, "cycle.start", "ws1", map[string]any{"name": "C", "days": 1})
	h.call(t, "bet.place", "ws1", map[string]any{"task_id": "t1", "appetite": "small"})
	h.now = t0.Add(25 * time.Hour)

	_, out := h.call(t, "state.get", "ws1", nil)
	require.Nil(t, out["active"])
	require.True(t, h.host.Archived("t1"))
	history := out["history"].([]any)
	require.Len(t, history, 1)
	bet := history[0].(map[string]any)["bets"].([]any)[0].(map[string]any)
	require.Equal(t, "cut", bet["status"])
}

func TestActions_BeforeHostInjection(t *testing.T) {
	p := newPlugin(time.Now)
	resp, err := p.HandleAction(context.Background(), &pluginsdk.PluginActionRequest{
		ActionKey: "state.get",
		Context:   pluginsdk.VerifiedActionContext{WorkspaceID: "ws1"},
	})
	require.NoError(t, err)
	require.Equal(t, 503, resp.Status)
}
