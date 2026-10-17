package main

import (
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// Every mutating action decides which tasks get archived (a bet is cut at the
// deadline unless it ships), so only a kandev administrator may call them. The host enforces access: admin before
// the plugin runs, and only from kandev 0.91.1 on.
func TestManifest_ActionAccess(t *testing.T) {
	raw, err := os.ReadFile("../manifest.yaml")
	require.NoError(t, err)
	var m struct {
		MinKandevVersion string `yaml:"min_kandev_version"`
		Actions          []struct {
			Key    string `yaml:"key"`
			Scope  string `yaml:"scope"`
			Access string `yaml:"access"`
		} `yaml:"actions"`
	}
	require.NoError(t, yaml.Unmarshal(raw, &m))
	require.Equal(t, "0.97.0", m.MinKandevVersion)

	access := map[string]string{}
	for _, a := range m.Actions {
		require.Equal(t, "workspace", a.Scope, a.Key)
		access[a.Key] = a.Access
	}
	require.Equal(t, map[string]string{
		actionStateGet:   "authenticated",
		actionCandidates: "authenticated",
		actionCycleStart: "admin",
		actionBetPlace:   "admin",
		actionBetShip:    "admin",
		actionBetCut:     "admin",
	}, access)
}
