// Package postgres observes a named physical standby from a PostgreSQL primary.
// It uses database/sql, leaving driver/connection ownership to the application.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"math"
	"sync"
	"time"
)

type Config struct {
	DB              *sql.DB
	ApplicationName string
	StaleAfter      time.Duration
}
type State struct {
	Available                 bool
	Status                    string
	LagBytes, ReplyAgeSeconds float64
	ObservedAt                time.Time
}
type Replication struct {
	config Config
	mu     sync.Mutex
	last   State
}

func NewReplication(c Config) (*Replication, error) {
	if c.DB == nil || len(c.ApplicationName) < 1 || len(c.ApplicationName) > 63 || c.StaleAfter <= 0 {
		return nil, fmt.Errorf("postgres: database, unique standby name and positive heartbeat age required")
	}
	return &Replication{config: c}, nil
}
func (r *Replication) Snapshot() State { r.mu.Lock(); defer r.mu.Unlock(); return r.last }
func (r *Replication) Sample(ctx context.Context) (decision.Observation, error) {
	rows, err := r.config.DB.QueryContext(ctx, `SELECT pg_is_in_recovery(), state, pg_wal_lsn_diff(pg_current_wal_lsn(),replay_lsn)::float8, extract(epoch FROM clock_timestamp()-reply_time)::float8 FROM pg_stat_replication WHERE application_name=$1`, r.config.ApplicationName)
	state := State{ObservedAt: time.Now()}
	defer func() { r.mu.Lock(); r.last = state; r.mu.Unlock() }()
	if err != nil {
		return decision.Observation{}, fmt.Errorf("postgres: replication observer unavailable")
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var recovery bool
		var status string
		var lag, age sql.NullFloat64
		if rows.Scan(&recovery, &status, &lag, &age) != nil || recovery || !lag.Valid || !age.Valid || math.IsNaN(lag.Float64) || math.IsNaN(age.Float64) || math.IsInf(lag.Float64, 0) || math.IsInf(age.Float64, 0) || lag.Float64 < 0 || age.Float64 < 0 || age.Float64 > r.config.StaleAfter.Seconds() || status != "streaming" && status != "catchup" {
			return decision.Observation{}, fmt.Errorf("postgres: replication state unknown or stale")
		}
		state.Status = status
		state.LagBytes = lag.Float64
		state.ReplyAgeSeconds = age.Float64
	}
	if rows.Err() != nil || count != 1 {
		return decision.Observation{}, fmt.Errorf("postgres: expected exactly one fresh named standby")
	}
	state.Available = true
	state.ObservedAt = time.Now()
	return decision.Observation{State: "measured", Value: state.LagBytes, ObservedAt: state.ObservedAt}, nil
}
