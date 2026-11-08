// Package operations is an isolated reference storage/query service inside the
// orders application. Its fixture queue is bounded and not a durable broker.
package operations

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"github.com/HN026/gatewise-examples/commerce/internal/handler"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/operation"
	"github.com/HN026/gatewise/postgres"
	"github.com/HN026/gatewise/regulator"
	"github.com/HN026/gatewise/sqlguard"
	"github.com/prometheus/client_golang/prometheus"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

type metadataKey struct{}
type Manager struct {
	contention                                                         *regulator.SQLPoolContention
	Mode                                                               string
	Storage, Replica, Workers                                          *operation.Engine
	db, follower                                                       *sqlguard.DB
	raw, read, observer                                                *sql.DB
	replication                                                        *postgres.Replication
	ctx                                                                context.Context
	cancel                                                             context.CancelFunc
	wg                                                                 sync.WaitGroup
	queue                                                              chan struct{}
	enabled                                                            atomic.Bool
	delay                                                              atomic.Int64
	submitted, dropped, started, completed, rejected, failed, canceled atomic.Uint64
	mu                                                                 sync.Mutex
	stream                                                             *sqlguard.Rows
	streamCancel                                                       context.CancelFunc
	holders                                                            []*sql.Conn
}

func New(parent context.Context, primary *sql.DB, primaryURL, replicaURL, mode string) (*Manager, error) {
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{Mode: mode, raw: primary, ctx: ctx, cancel: cancel, queue: make(chan struct{}, 16)}
	m.delay.Store(120)
	fail := func(err error) (*Manager, error) { m.Close(); return nil, err }
	if _, err := primary.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS operation_events(id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,payload bytea NOT NULL)`); err != nil {
		return fail(err)
	}
	var err error
	m.read, err = sql.Open("pgx", replicaURL)
	if err != nil {
		return fail(err)
	}
	m.read.SetMaxOpenConns(2)
	m.read.SetMaxIdleConns(2)
	ready, stop := context.WithTimeout(ctx, 10*time.Second)
	defer stop()
	for {
		var exists bool
		err = m.read.QueryRowContext(ready, `SELECT to_regclass('public.operation_events') IS NOT NULL`).Scan(&exists)
		if err == nil && exists {
			break
		}
		select {
		case <-ready.Done():
			return fail(fmt.Errorf("operations: standby schema unavailable"))
		case <-time.After(50 * time.Millisecond):
		}
	}
	m.observer, err = sql.Open("pgx", primaryURL)
	if err != nil {
		return fail(err)
	}
	m.observer.SetMaxOpenConns(1)
	m.observer.SetMaxIdleConns(1)
	m.replication, err = postgres.NewReplication(postgres.Config{DB: m.observer, ApplicationName: "gatewise_standby", StaleAfter: 3 * time.Second})
	if err != nil {
		return fail(err)
	}
	var poolEngine *decision.Engine
	if mode == "pool" || mode == "balanced" {
		poolEngine, err = decision.New(decision.Config{Rules: []decision.Rule{{Name: "pool", Scope: decision.Scope{Kind: "resource", Key: "background"}, Threshold: func() float64 {
			if mode == "balanced" {
				return 1
			}
			return .75
		}(), MaxAge: time.Second, OnUnknown: "limit", UnknownLimit: 1, Pressure: decision.Effect{MaxConcurrent: 1}}}})
		if err != nil {
			return fail(err)
		}
	}
	storageConfig := operation.Config{Name: "orders_storage", MaxConcurrent: 4, ReservedCritical: 1, Decisions: poolEngine}
	if mode == "balanced" {
		storageConfig.Tenants = &operation.TenantConfig{MaxConcurrent: 1, Limits: map[string]int{"interactive": 3, "batch": 1}, MaxTracked: 2, IdleTTL: time.Second, Sharing: &operation.TenantSharingConfig{MaxConcurrent: 4, DemandTTL: 200 * time.Millisecond}}
	}
	m.Storage, err = operation.New(storageConfig)
	if err != nil {
		return fail(err)
	}
	m.Workers, err = operation.New(operation.Config{Name: "orders_worker", MaxConcurrent: 6})
	if err != nil {
		return fail(err)
	}
	var replicaEngine *decision.Engine
	if mode != "disabled" {
		replicaEngine, err = decision.New(decision.Config{Rules: []decision.Rule{{Name: "replication", Scope: decision.Scope{Kind: "resource", Key: "replica_read"}, Threshold: 65536, MaxAge: time.Second, OnUnknown: "reject", Pressure: decision.Effect{Reject: true}}}})
		if err != nil {
			return fail(err)
		}
	}
	m.Replica, err = operation.New(operation.Config{Name: "orders_replica", MaxConcurrent: 2, Decisions: replicaEngine})
	if err != nil {
		return fail(err)
	}
	extract := func(ctx context.Context) operation.Request {
		r, _ := ctx.Value(metadataKey{}).(operation.Request)
		return r
	}
	m.db, err = sqlguard.New(primary, m.Storage, extract)
	if err != nil {
		return fail(err)
	}
	m.follower, err = sqlguard.New(m.read, m.Replica, extract)
	if err != nil {
		return fail(err)
	}
	if poolEngine != nil {
		var provider decision.Provider = regulator.SQLPool{DB: primary}
		if mode == "balanced" {
			m.contention, err = regulator.NewSQLPoolContention(regulator.SQLPoolContentionConfig{DB: primary, RecoveryQuiet: 200 * time.Millisecond})
			if err != nil {
				return fail(err)
			}
			provider = m.contention
		}
		m.collect(poolEngine, []decision.Collector{{Name: "pool", Provider: provider, Interval: 100 * time.Millisecond, Timeout: 50 * time.Millisecond}})
	}
	if replicaEngine != nil {
		m.collect(replicaEngine, []decision.Collector{{Name: "replication", Provider: m.replication, Interval: 100 * time.Millisecond, Timeout: 80 * time.Millisecond}})
	}
	for i := 0; i < 6; i++ {
		m.wg.Add(1)
		go m.worker()
	}
	m.wg.Add(1)
	go m.schedule()
	return m, nil
}
func (m *Manager) collect(engine *decision.Engine, c []decision.Collector) {
	m.wg.Add(1)
	go func() { defer m.wg.Done(); _ = engine.Run(m.ctx, c) }()
}
func (m *Manager) Close() {
	if m.cancel != nil {
		m.cancel()
	}
	m.closeStream()
	m.releasePool()
	m.wg.Wait()
	if m.read != nil {
		m.read.Close()
	}
	if m.observer != nil {
		m.observer.Close()
	}
}
func (m *Manager) Register(r *prometheus.Registry) error {
	for _, e := range []*operation.Engine{m.Storage, m.Replica, m.Workers} {
		if err := metrics.Register(r, e); err != nil {
			return err
		}
	}
	return nil
}
func plan(ctx context.Context, priority operation.Priority, resource string) context.Context {
	target := 100 * time.Millisecond
	if priority == operation.Background {
		target = 500 * time.Millisecond
	}
	return context.WithValue(ctx, metadataKey{}, operation.Request{Priority: priority, Tenant: map[bool]string{true: "batch", false: "interactive"}[priority == operation.Background], Resource: resource, Cost: decision.Cost{WriteBytes: 4096, Key: "events"}, LatencyTarget: target})
}

type executor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func (m *Manager) write(ctx context.Context, background bool) error {
	priority, resource, hold := operation.Critical, "storage", 20*time.Millisecond
	if background {
		priority, resource, hold = operation.Background, "background", time.Duration(m.delay.Load())*time.Millisecond
	}
	ctx = plan(ctx, priority, resource)
	body := func(ctx context.Context, tx executor) error {
		data := make([]byte, 4096)
		if _, err := rand.Read(data); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO operation_events(payload) VALUES($1)`, data); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `SELECT pg_sleep($1)`, hold.Seconds())
		return err
	}
	if m.Mode == "disabled" {
		tx, err := m.raw.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		if err := body(ctx, tx); err != nil {
			return err
		}
		return tx.Commit()
	}
	return m.db.InTx(ctx, nil, func(tx *sqlguard.Tx) error { return body(tx.Context(), tx) })
}
func (m *Manager) submit() {
	select {
	case m.queue <- struct{}{}:
		m.submitted.Add(1)
	default:
		m.dropped.Add(1)
	}
}
func (m *Manager) schedule() {
	defer m.wg.Done()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-tick.C:
			if m.enabled.Load() {
				m.submit()
			}
		}
	}
}
func (m *Manager) worker() {
	defer m.wg.Done()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-m.queue:
			m.started.Add(1)
			ctx, cancel := context.WithTimeout(m.ctx, time.Second)
			err := m.Workers.Do(ctx, operation.Request{Priority: operation.Background, Resource: "job", Tenant: "batch", LatencyTarget: 500 * time.Millisecond}, func(ctx context.Context) error { return m.write(ctx, true) })
			cancel()
			var rejected *operation.Rejection
			if errors.As(err, &rejected) {
				m.rejected.Add(1)
			} else if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				m.canceled.Add(1)
			} else if err != nil {
				m.failed.Add(1)
			} else {
				m.completed.Add(1)
			}
		}
	}
}
func (m *Manager) response(out http.ResponseWriter, err error) {
	if err == nil {
		handler.JSON(out, 200, map[string]string{"status": "ok"})
		return
	}
	var rejection *operation.Rejection
	if errors.As(err, &rejection) {
		handler.JSON(out, 503, map[string]any{"error": "service_overloaded", "reason": rejection.Reason, "decision": rejection.Decision})
		return
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		handler.JSON(out, 504, map[string]string{"error": "database_timeout"})
		return
	}
	handler.JSON(out, 500, map[string]string{"error": "database_operation_failed"})
}
func (m *Manager) Transaction(out http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	m.response(out, m.write(ctx, false))
}
func (m *Manager) ReadReplica(out http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	ctx = plan(ctx, operation.Critical, "replica_read")
	var n int64
	var err error
	if m.Mode == "disabled" {
		err = m.read.QueryRowContext(ctx, `SELECT count(*) FROM operation_events`).Scan(&n)
	} else {
		err = m.follower.QueryRowContext(ctx, `SELECT count(*) FROM operation_events`).Scan(&n)
	}
	if err != nil {
		m.response(out, err)
		return
	}
	handler.JSON(out, 200, map[string]int64{"rows": n})
}
func (m *Manager) State(out http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	streamOpen, held := m.stream != nil, len(m.holders)
	m.mu.Unlock()
	handler.JSON(out, 200, map[string]any{"mode": m.Mode, "storage": m.Storage.Snapshot(), "storage_sharing": m.Storage.TenantSnapshot(), "pool_contention": func() any {
		if m.contention == nil {
			return nil
		}
		return m.contention.Snapshot()
	}(), "replica": m.Replica.Snapshot(), "worker": m.Workers.Snapshot(), "storage_work": m.Storage.Statistics().Work, "replica_work": m.Replica.Statistics().Work, "storage_signals": m.Storage.DecisionSnapshot(), "replica_signals": m.Replica.DecisionSnapshot(), "replication": m.replication.Snapshot(), "pool": m.raw.Stats(), "stream_open": streamOpen, "pool_held": held, "queue_length": len(m.queue), "queue_capacity": cap(m.queue), "workers_enabled": m.enabled.Load(), "jobs": map[string]uint64{"submitted": m.submitted.Load(), "dropped": m.dropped.Load(), "started": m.started.Load(), "completed": m.completed.Load(), "rejected": m.rejected.Load(), "failed": m.failed.Load(), "canceled": m.canceled.Load()}})
}
func (m *Manager) Control(out http.ResponseWriter, r *http.Request) {
	var c struct {
		Enabled bool  `json:"enabled"`
		DelayMS int64 `json:"delay_ms"`
		Jobs    int   `json:"jobs"`
	}
	if err := decode(out, r, &c); err != nil || c.DelayMS < 0 || c.DelayMS > 500 || c.Jobs < 0 || c.Jobs > 16 {
		handler.JSON(out, 400, map[string]string{"error": "invalid_operation_control"})
		return
	}
	m.delay.Store(c.DelayMS)
	m.enabled.Store(c.Enabled)
	for i := 0; i < c.Jobs; i++ {
		m.submit()
	}
	m.State(out, r)
}
func (m *Manager) closeStream() { m.closeOwnedStream(nil) }
func (m *Manager) closeOwnedStream(expected *sqlguard.Rows) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if expected != nil && m.stream != expected {
		return
	}
	if m.stream != nil {
		_ = m.stream.Close()
		m.stream = nil
	}
	if m.streamCancel != nil {
		m.streamCancel()
		m.streamCancel = nil
	}
}
func (m *Manager) Stream(out http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		m.closeStream()
		m.State(out, r)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.stream != nil {
		handler.JSON(out, 409, map[string]string{"error": "stream_already_open"})
		return
	}
	ctx, cancel := context.WithTimeout(plan(m.ctx, operation.Critical, "storage"), 5*time.Second)
	rows, err := m.db.QueryContext(ctx, `SELECT generate_series(1,3)`)
	if err != nil {
		cancel()
		m.response(out, err)
		return
	}
	if !rows.Next() {
		rows.Close()
		cancel()
		m.response(out, fmt.Errorf("stream empty"))
		return
	}
	var n int
	if err = rows.Scan(&n); err != nil {
		rows.Close()
		cancel()
		m.response(out, err)
		return
	}
	if r.Context().Err() != nil {
		rows.Close()
		cancel()
		return
	}
	m.stream = rows
	m.streamCancel = cancel
	go func() { <-ctx.Done(); m.closeOwnedStream(rows) }()
	handler.JSON(out, 200, map[string]any{"stream_open": true, "first_row": n, "lifetime_seconds": 5})
}
func (m *Manager) releasePool() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, c := range m.holders {
		c.Close()
	}
	m.holders = nil
}
func (m *Manager) Pool(out http.ResponseWriter, r *http.Request) {
	if r.Method == "DELETE" {
		m.releasePool()
		m.State(out, r)
		return
	}
	var c struct {
		Count int `json:"count"`
	}
	if decode(out, r, &c) != nil || c.Count < 1 || c.Count > 4 {
		handler.JSON(out, 400, map[string]string{"error": "invalid_pool_hold"})
		return
	}
	m.mu.Lock()
	if len(m.holders) > 0 {
		m.mu.Unlock()
		handler.JSON(out, 409, map[string]string{"error": "pool_already_held"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Second)
	defer cancel()
	for i := 0; i < c.Count; i++ {
		conn, err := m.raw.Conn(ctx)
		if err != nil {
			for _, h := range m.holders {
				h.Close()
			}
			m.holders = nil
			m.mu.Unlock()
			m.response(out, err)
			return
		}
		m.holders = append(m.holders, conn)
	}
	m.mu.Unlock()
	m.State(out, r)
}
