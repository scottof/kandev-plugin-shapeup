package shapeup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"kandev-plugin-shapeup/server/internal/fakehost"
)

func TestStore_ActiveRoundTrip(t *testing.T) {
	ctx := context.Background()
	host := fakehost.New()
	s := NewStore(host)

	got, err := s.Active(ctx, "ws1")
	require.NoError(t, err)
	require.Nil(t, got, "no key means no active cycle")

	c := activeCycle(t)
	require.NoError(t, c.PlaceBet("t1", "Task one", AppetiteBig, t0.Add(time.Minute)))
	require.NoError(t, c.MarkShipped("t1", t0.Add(time.Hour)))
	require.NoError(t, s.SaveActive(ctx, "ws1", c))

	got, err = s.Active(ctx, "ws1")
	require.NoError(t, err)
	require.Equal(t, c, got, "survives the JSON round trip the Host applies")

	raw, found, err := host.GetState(ctx, "workspace", "ws1", "active")
	require.NoError(t, err)
	require.True(t, found)
	require.InDelta(t, float64(1), raw["v"], 0, "numbers come back as float64")

	other, err := s.Active(ctx, "ws2")
	require.NoError(t, err)
	require.Nil(t, other, "state is scoped per workspace")
}

func TestStore_RejectsUnknownVersion(t *testing.T) {
	ctx := context.Background()
	host := fakehost.New()
	host.SetRaw("workspace", "ws1", "active", map[string]any{"v": 2, "cycle": map[string]any{}})
	_, err := NewStore(host).Active(ctx, "ws1")
	require.ErrorIs(t, err, ErrUnsupportedVersion)

	host.SetRaw("workspace", "ws1", "active", map[string]any{"cycle": map[string]any{}})
	_, err = NewStore(host).Active(ctx, "ws1")
	require.ErrorIs(t, err, ErrUnsupportedVersion)
}

func TestStore_Index(t *testing.T) {
	ctx := context.Background()
	host := fakehost.New()
	s := NewStore(host)

	ids, err := s.Workspaces(ctx)
	require.NoError(t, err)
	require.Empty(t, ids)

	require.NoError(t, s.SaveActive(ctx, "ws1", activeCycle(t)))
	require.NoError(t, s.SaveActive(ctx, "ws1", activeCycle(t)))
	require.NoError(t, s.SaveActive(ctx, "ws2", activeCycle(t)))

	ids, err = s.Workspaces(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"ws1", "ws2"}, ids, "saved once per workspace")
}

func TestStore_ArchiveMovesCycleToHistory(t *testing.T) {
	ctx := context.Background()
	host := fakehost.New()
	s := NewStore(host)

	first := activeCycle(t)
	require.NoError(t, first.PlaceBet("t1", "T1", AppetiteSmall, t0))
	require.NoError(t, first.Cut("t1", t0))
	require.NoError(t, first.Close(t0.Add(time.Hour)))
	require.NoError(t, s.SaveActive(ctx, "ws1", first))
	require.NoError(t, s.Archive(ctx, "ws1", first))

	active, err := s.Active(ctx, "ws1")
	require.NoError(t, err)
	require.Nil(t, active)
	ids, err := s.Workspaces(ctx)
	require.NoError(t, err)
	require.Empty(t, ids, "closed workspaces leave the breaker index")

	second, err := StartCycle(nil, "c2", "Cycle 2", 7, t0.Add(30*24*time.Hour))
	require.NoError(t, err)
	require.NoError(t, second.Close(t0.Add(31*24*time.Hour)))
	require.NoError(t, s.Archive(ctx, "ws1", second))
	require.NoError(t, s.Archive(ctx, "ws1", second), "archiving twice is harmless")

	host.SetRaw("workspace", "ws1", "unrelated", map[string]any{"v": 1})

	history, err := s.History(ctx, "ws1")
	require.NoError(t, err)
	require.Len(t, history, 2)
	require.Equal(t, "c2", history[0].ID, "newest first")
	require.Equal(t, *first, history[1])
}

func TestStore_ArchiveRejectsActiveCycle(t *testing.T) {
	err := NewStore(fakehost.New()).Archive(context.Background(), "ws1", activeCycle(t))
	require.ErrorIs(t, err, ErrCycleNotClosed)
}

func TestStore_PropagatesStateErrors(t *testing.T) {
	host := fakehost.New()
	host.StateErr = errors.New("boom")
	err := NewStore(host).SaveActive(context.Background(), "ws1", activeCycle(t))
	require.ErrorContains(t, err, "boom")
}
