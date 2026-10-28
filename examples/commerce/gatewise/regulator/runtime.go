// Package regulator supplies measured process signals for the decision engine.
// Providers sample in the background, never during request admission.
package regulator

import (
	"context"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/resource"
	"math"
	"os"
	"runtime"
	"runtime/debug"
	"sync"
	"time"
)

type MemoryConfig struct{ LimitBytes int64 }
type MemoryState struct {
	Source                string
	UsedBytes, LimitBytes uint64
	Ratio                 float64
	ObservedAt            time.Time
}
type Memory struct {
	config MemoryConfig
	mu     sync.Mutex
	last   MemoryState
}

// NewMemory compares Go-managed memory against an explicit budget (or the
// current finite GOMEMLIMIT) and, on Linux, visible finite cgroup limits. The
// highest measured ratio wins. No host-RAM fallback is guessed. Go-managed
// bytes are runtime Sys - HeapReleased, not process RSS or native allocations.
func NewMemory(c MemoryConfig) (*Memory, error) {
	if c.LimitBytes < 0 {
		return nil, fmt.Errorf("regulator: memory limit must be nonnegative")
	}
	return &Memory{config: c}, nil
}
func (m *Memory) Snapshot() MemoryState { m.mu.Lock(); defer m.mu.Unlock(); return m.last }
func (m *Memory) Sample(ctx context.Context) (decision.Observation, error) {
	if err := ctx.Err(); err != nil {
		return decision.Observation{}, err
	}
	state := MemoryState{}
	if runtime.GOOS == "linux" {
		v, found, err := resource.CgroupMemory(os.DirFS("/"))
		if err != nil {
			return decision.Observation{}, fmt.Errorf("regulator: container memory unavailable")
		}
		if found {
			state = MemoryState{Source: v.Source, UsedBytes: v.Used, LimitBytes: v.Limit, Ratio: float64(v.Used) / float64(v.Limit)}
		}
	}
	limit := m.config.LimitBytes
	if limit == 0 {
		limit = debug.SetMemoryLimit(-1)
	}
	if limit > 0 && limit < math.MaxInt64 {
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		used := stats.Sys - stats.HeapReleased
		ratio := float64(used) / float64(limit)
		if state.Source == "" || ratio > state.Ratio {
			state = MemoryState{Source: "go_managed", UsedBytes: used, LimitBytes: uint64(limit), Ratio: ratio}
		}
	}
	if state.Source == "" {
		return decision.Observation{}, fmt.Errorf("regulator: configure a memory budget or finite runtime/container limit")
	}
	if err := ctx.Err(); err != nil {
		return decision.Observation{}, err
	}
	state.ObservedAt = time.Now()
	m.mu.Lock()
	m.last = state
	m.mu.Unlock()
	return decision.Observation{State: "measured", Value: state.Ratio, ObservedAt: state.ObservedAt}, nil
}

type Goroutines struct{}

func (Goroutines) Sample(ctx context.Context) (decision.Observation, error) {
	if err := ctx.Err(); err != nil {
		return decision.Observation{}, err
	}
	return decision.Observation{State: "measured", Value: float64(runtime.NumGoroutine()), ObservedAt: time.Now()}, nil
}
