package regulator

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"sync"
	"time"
)

// SQLPoolContentionConfig configures a local, sampled pool-wait signal. The
// application owns DB. RecoveryQuiet defaults to 200ms; values must be positive.
type SQLPoolContentionConfig struct {
	DB            *sql.DB
	RecoveryQuiet time.Duration
}
type SQLPoolContentionState struct {
	Pressure               bool
	Utilization            float64
	WaitCountDelta         int64
	LastWaitAt, ObservedAt time.Time
	Reason                 string
}

// SQLPoolContention reports 0 or 1. Unlike SQLPool, occupancy alone does not
// establish pressure. A new DBStats.WaitCount increment activates pressure;
// recovery requires a quiet interval and a pool that is no longer full. It is
// not queue depth, an availability check, or a guarantee of fair scheduling.
// Use one provider per pool/collection stream and a decision threshold of 1.
// Historical waits before construction are ignored. LastWaitAt is the sample
// time detecting new waits, not a database-provided event timestamp.
// Construct with NewSQLPoolContention; the zero value is not usable.
type SQLPoolContention struct {
	db          *sql.DB
	quiet       time.Duration
	mu          sync.Mutex
	initialized bool
	previous    int64
	last        SQLPoolContentionState
}

func NewSQLPoolContention(c SQLPoolContentionConfig) (*SQLPoolContention, error) {
	if c.DB == nil {
		return nil, fmt.Errorf("regulator: SQL pool is required")
	}
	if c.RecoveryQuiet == 0 {
		c.RecoveryQuiet = 200 * time.Millisecond
	}
	if c.RecoveryQuiet < 0 {
		return nil, fmt.Errorf("regulator: recovery quiet interval must be positive")
	}
	return &SQLPoolContention{db: c.DB, quiet: c.RecoveryQuiet, initialized: true, previous: c.DB.Stats().WaitCount, last: SQLPoolContentionState{Reason: "not_sampled"}}, nil
}
func (p *SQLPoolContention) Snapshot() SQLPoolContentionState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.last
}
func (p *SQLPoolContention) Sample(ctx context.Context) (decision.Observation, error) {
	if err := ctx.Err(); err != nil {
		return decision.Observation{}, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.db.Stats()
	now := time.Now()
	if s.MaxOpenConnections <= 0 {
		return decision.Observation{}, fmt.Errorf("regulator: configure finite MaxOpenConns")
	}
	state := p.last
	state.ObservedAt = now
	state.Utilization = float64(s.InUse) / float64(s.MaxOpenConnections)
	state.WaitCountDelta = 0
	if !p.initialized || s.WaitCount < p.previous {
		p.initialized = true
		state.Pressure = false
		state.LastWaitAt = time.Time{}
		state.Reason = "baseline"
	} else {
		state.WaitCountDelta = s.WaitCount - p.previous
		if state.WaitCountDelta > 0 {
			state.Pressure = true
			state.LastWaitAt = now
			state.Reason = "new_waits"
		} else if state.Pressure {
			if s.InUse >= s.MaxOpenConnections {
				state.Reason = "full_after_wait"
			} else if now.Sub(state.LastWaitAt) < p.quiet {
				state.Reason = "recovery_quiet"
			} else {
				state.Pressure = false
				state.Reason = "recovered"
			}
		} else {
			state.Reason = "no_new_waits"
		}
	}
	p.previous = s.WaitCount
	p.last = state
	value := float64(0)
	if state.Pressure {
		value = 1
	}
	return decision.Observation{State: "measured", Value: value, ObservedAt: now}, nil
}
