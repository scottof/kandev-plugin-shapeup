package main

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kandev-plugin-shapeup/server/internal/fakehost"
	"kandev-plugin-shapeup/server/shapeup"
)

// The deadline passed while the plugin was not running: injecting the Host
// at startup must cut the overdue bet without waiting for a page action.
func TestSetHost_RunsBreakerAtStartup(t *testing.T) {
	ctx := context.Background()
	host := fakehost.New()
	host.AddTask("ws1", "wf1", "s", "t1", "Task")

	seed := shapeup.NewService(host.AsHost(), func() time.Time { return t0 }, func() string { return "c1" })
	_, err := seed.StartCycle(ctx, "ws1", "C", 1)
	require.NoError(t, err)
	require.NoError(t, seed.PlaceBet(ctx, "ws1", "t1", shapeup.AppetiteSmall))

	p := newPlugin(func() time.Time { return t0.Add(48 * time.Hour) })
	p.breakerInterval = time.Hour
	p.SetHost(host.AsHost())

	require.Eventually(t, func() bool { return host.Archived("t1") }, 2*time.Second, 10*time.Millisecond)
}

// A second Host injection must not create a second Service: two services
// would have two mutexes and could overwrite each other's state writes.
func TestSetHost_TwiceKeepsOneService(t *testing.T) {
	ctx := context.Background()
	first, second := fakehost.New(), fakehost.New()
	p := newPlugin(func() time.Time { return t0 })
	p.breakerInterval = 0

	p.SetHost(first.AsHost())
	svc := p.service()
	p.SetHost(second.AsHost())
	require.Same(t, svc, p.service())

	_, err := p.service().StartCycle(ctx, "ws1", "C", 7)
	require.NoError(t, err)
	got, err := shapeup.NewStore(second).Active(ctx, "ws1")
	require.NoError(t, err)
	require.NotNil(t, got, "the service talks to the latest Host")
}

func TestNewCycleID(t *testing.T) {
	a, b := newCycleID(), newCycleID()
	require.Len(t, a, 17)
	require.NotEqual(t, a, b)
}
