package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"github.com/kandev/kandev/pkg/pluginsdk"

	"kandev-plugin-shapeup/server/shapeup"
)

const (
	defaultBreakerInterval = time.Minute
	breakerRunTimeout      = 30 * time.Second
)

// shapeupPlugin serves the betting table actions and runs the circuit
// breaker. kandev has no plugin start hook, but it injects the Host once per
// process start; SetHost is where the breaker loop begins, so a deadline that
// passed while kandev was off is enforced as soon as the plugin is back.
type shapeupPlugin struct {
	pluginsdk.UnimplementedPlugin

	now func() time.Time
	// breakerInterval is the timer period; zero disables the background loop.
	breakerInterval time.Duration

	mu        sync.RWMutex
	svc       *shapeup.Service
	startLoop sync.Once
}

var (
	_ pluginsdk.Plugin        = (*shapeupPlugin)(nil)
	_ pluginsdk.ActionHandler = (*shapeupPlugin)(nil)
)

func newPlugin(now func() time.Time) *shapeupPlugin {
	return &shapeupPlugin{now: now, breakerInterval: defaultBreakerInterval}
}

func (p *shapeupPlugin) SetHost(host pluginsdk.Host) {
	p.UnimplementedPlugin.SetHost(host)
	p.mu.Lock()
	if p.svc == nil {
		p.svc = shapeup.NewService(host, p.now, newCycleID)
	} else {
		// One Service, so one mutex guards every Host state write.
		p.svc.SetHost(host)
	}
	p.mu.Unlock()
	if p.breakerInterval > 0 {
		p.startLoop.Do(func() { go p.breakerLoop() })
	}
}

func (p *shapeupPlugin) service() *shapeup.Service {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.svc
}

// breakerLoop runs for the life of the process; kandev stops the process on
// disable, upgrade or shutdown.
func (p *shapeupPlugin) breakerLoop() {
	p.runBreaker()
	ticker := time.NewTicker(p.breakerInterval)
	defer ticker.Stop()
	for range ticker.C {
		p.runBreaker()
	}
}

func (p *shapeupPlugin) runBreaker() {
	ctx, cancel := context.WithTimeout(context.Background(), breakerRunTimeout)
	defer cancel()
	if err := p.service().RunBreaker(ctx); err != nil {
		log.Printf("circuit breaker: %v", err)
	}
}

// newCycleID returns "c" plus 16 hex characters.
func newCycleID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	return "c" + hex.EncodeToString(b[:])
}
