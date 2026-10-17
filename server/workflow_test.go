package main

import (
	"bytes"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"

	"kandev-plugin-shapeup/server/shapeup"
)

// These types mirror the portable workflow export of kandev v0.97.0
// (apps/backend/internal/workflow/models/export.go and models.go). Go forbids
// importing kandev's internal packages, so the test decodes strictly into a
// copy: a misspelled or unknown field fails here instead of being silently
// dropped by the importer.
type workflowExport struct {
	Version   int                `yaml:"version"`
	Type      string             `yaml:"type"`
	Workflows []workflowPortable `yaml:"workflows"`
}

type workflowPortable struct {
	Name        string         `yaml:"name"`
	Description string         `yaml:"description"`
	Prompt      string         `yaml:"prompt"`
	Steps       []stepPortable `yaml:"steps"`
}

type stepPortable struct {
	Name                        string     `yaml:"name"`
	Position                    int        `yaml:"position"`
	Color                       string     `yaml:"color"`
	Prompt                      string     `yaml:"prompt"`
	Events                      stepEvents `yaml:"events"`
	IsStartStep                 bool       `yaml:"is_start_step"`
	ShowInCommandPanel          bool       `yaml:"show_in_command_panel"`
	AllowManualMove             bool       `yaml:"allow_manual_move"`
	AutoArchiveAfterHours       int        `yaml:"auto_archive_after_hours"`
	DisableUnclassifiedFallback bool       `yaml:"disable_unclassified_fallback"`
	AutoAdvanceRequiresSignal   bool       `yaml:"auto_advance_requires_signal"`
	CancelTriggersTurnComplete  bool       `yaml:"cancel_triggers_turn_complete"`
	CompleteTaskOnEnter         *bool      `yaml:"complete_task_on_enter"`
	WIPLimit                    int        `yaml:"wip_limit"`
}

type stepAction struct {
	Type   string         `yaml:"type"`
	Config map[string]any `yaml:"config"`
}

type stepEvents struct {
	OnEnter        []stepAction `yaml:"on_enter"`
	OnTurnStart    []stepAction `yaml:"on_turn_start"`
	OnTurnComplete []stepAction `yaml:"on_turn_complete"`
	OnExit         []stepAction `yaml:"on_exit"`
}

func loadWorkflow(t *testing.T) workflowExport {
	t.Helper()
	data, err := os.ReadFile("../workflows/shape-up.yaml")
	require.NoError(t, err)
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var export workflowExport
	require.NoError(t, dec.Decode(&export))
	return export
}

func TestWorkflow_MatchesTheV0970ImportFormat(t *testing.T) {
	export := loadWorkflow(t)
	require.Equal(t, 2, export.Version)
	require.Equal(t, "kandev_workflow", export.Type)
	require.Len(t, export.Workflows, 1)

	wf := export.Workflows[0]
	require.Equal(t, "Shape Up", wf.Name)

	names := []string{}
	starts := 0
	for i, step := range wf.Steps {
		names = append(names, step.Name)
		require.Equal(t, i, step.Position, "positions are 0..n in order")
		require.NotEmpty(t, step.Color)
		require.NotNil(t, step.CompleteTaskOnEnter, "%s: version 2 needs complete_task_on_enter set explicitly", step.Name)
		require.True(t, step.AllowManualMove, "%s: humans move tasks between steps", step.Name)
		if step.IsStartStep {
			starts++
		}
		for _, a := range step.Events.OnEnter {
			require.Contains(t, []string{"enable_plan_mode", "auto_start_agent"}, a.Type)
		}
		for _, a := range step.Events.OnExit {
			require.Equal(t, "disable_plan_mode", a.Type)
		}
		require.Empty(t, step.Events.OnTurnStart)
		require.Empty(t, step.Events.OnTurnComplete, "%s: no step advances on its own", step.Name)
	}
	require.Equal(t, []string{"Raw ideas", "Shape", shapeup.BettingTableStep, "Build", "Ship"}, names)
	require.Equal(t, 1, starts)
	require.True(t, wf.Steps[0].IsStartStep, "ideas start as raw ideas, not a backlog")
}

func TestWorkflow_StepBehavior(t *testing.T) {
	steps := loadWorkflow(t).Workflows[0].Steps
	types := func(actions []stepAction) []string {
		out := []string{}
		for _, a := range actions {
			out = append(out, a.Type)
		}
		return out
	}

	shape := steps[1]
	require.Equal(t, []string{"enable_plan_mode", "auto_start_agent"}, types(shape.Events.OnEnter))
	require.Equal(t, []string{"disable_plan_mode"}, types(shape.Events.OnExit))
	require.Contains(t, shape.Prompt, "create_task_plan_kandev")
	require.Contains(t, shape.Prompt, "Do NOT write or change any code")
	for _, section := range []string{"Problem", "Appetite", "Solution", "Rabbit holes", "No-gos"} {
		require.Contains(t, shape.Prompt, section)
	}

	require.Empty(t, types(steps[2].Events.OnEnter), "the betting table is human-only")
	require.Empty(t, steps[2].Prompt)

	build := steps[3]
	require.Equal(t, []string{"auto_start_agent"}, types(build.Events.OnEnter))
	require.Contains(t, build.Prompt, "get_task_plan_kandev")
	require.Contains(t, build.Prompt, "hard constraints")

	require.Empty(t, types(steps[4].Events.OnEnter), "shipping is a manual review and merge")
}
