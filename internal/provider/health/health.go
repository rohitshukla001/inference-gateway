// Package health runs periodic provider health checks. State is in-memory
// and per-process; each orchestrator replica keeps its own view.
package health

import (
	"context"
	"encoding/json"
	"log"
	"maps"
	"net/http"
	"sort"
	"sync"
	"time"

	"github.com/rshuklas/inference-gateway/internal/provider"
)

// State is the latest health observation for a single provider.
type State struct {
	Healthy   bool      `json:"healthy"`
	LastError string    `json:"last_error,omitempty"`
	LastCheck time.Time `json:"last_check"`
}

// Checker pings each provider periodically and tracks the latest result.
// Providers start healthy so a fresh process does not deny traffic before
// the first ping.
type Checker struct {
	providers map[string]provider.Provider
	interval  time.Duration
	timeout   time.Duration

	mu     sync.RWMutex
	states map[string]State
}

// New builds a Checker that pings every interval with a per-ping timeout.
// interval <= 0 disables pinging, leaving every provider healthy.
func New(providers map[string]provider.Provider, interval, timeout time.Duration) *Checker {
	states := make(map[string]State, len(providers))
	for name := range providers {
		states[name] = State{Healthy: true}
	}
	return &Checker{
		providers: providers,
		interval:  interval,
		timeout:   timeout,
		states:    states,
	}
}

// Start pings in the background until ctx is cancelled. Call it once.
func (c *Checker) Start(ctx context.Context) {
	if c.interval <= 0 {
		return
	}
	go c.run(ctx)
}

func (c *Checker) run(ctx context.Context) {
	c.checkAll(ctx)
	t := time.NewTicker(c.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			c.checkAll(ctx)
		}
	}
}

func (c *Checker) checkAll(parent context.Context) {
	for name, p := range c.providers {
		ctx, cancel := context.WithTimeout(parent, c.timeout)
		err := p.HealthCheck(ctx)
		cancel()
		c.update(name, err)
	}
}

func (c *Checker) update(name string, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.states[name]
	wasHealthy := s.Healthy
	s.Healthy = err == nil
	s.LastCheck = time.Now()
	if err != nil {
		s.LastError = err.Error()
	} else {
		s.LastError = ""
	}
	c.states[name] = s
	if wasHealthy && !s.Healthy {
		log.Printf("[health] provider %s went unhealthy: %v", name, err)
	} else if !wasHealthy && s.Healthy {
		log.Printf("[health] provider %s recovered", name)
	}
}

// IsHealthy reports the latest health for name; unknown providers are
// unhealthy.
func (c *Checker) IsHealthy(name string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.states[name]
	return ok && s.Healthy
}

// Snapshot returns a copy of the per-provider state map.
func (c *Checker) Snapshot() map[string]State {
	c.mu.RLock()
	defer c.mu.RUnlock()
	out := make(map[string]State, len(c.states))
	maps.Copy(out, c.states)
	return out
}

// Handler serves the snapshot as JSON, sorted by provider name.
func (c *Checker) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		snap := c.Snapshot()
		names := make([]string, 0, len(snap))
		for name := range snap {
			names = append(names, name)
		}
		sort.Strings(names)
		ordered := make([]map[string]any, 0, len(names))
		for _, name := range names {
			s := snap[name]
			ordered = append(ordered, map[string]any{
				"name":       name,
				"healthy":    s.Healthy,
				"last_error": s.LastError,
				"last_check": s.LastCheck,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"providers": ordered})
	})
}
