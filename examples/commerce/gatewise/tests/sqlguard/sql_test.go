package sqlguard_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/operation"
	"github.com/HN026/gatewise/regulator"
	"github.com/HN026/gatewise/sqlguard"
	"github.com/prometheus/client_golang/prometheus"
	"io"
	"sync"
	"testing"
	"time"
)

type state struct {
	multiple                                                     bool
	rollbackStarted, rollbackRelease, closeStarted, closeRelease chan struct{}
	rollbackOnce, closeOnce                                      sync.Once
}
type connector struct{ s *state }

func (c connector) Connect(context.Context) (driver.Conn, error) { return &conn{c.s}, nil }
func (c connector) Driver() driver.Driver                        { return drv{c.s} }

type drv struct{ s *state }

func (d drv) Open(string) (driver.Conn, error) { return &conn{d.s}, nil }

type conn struct{ s *state }

func (c *conn) Prepare(string) (driver.Stmt, error)                          { return nil, errors.New("unused") }
func (c *conn) Close() error                                                 { return nil }
func (c *conn) Begin() (driver.Tx, error)                                    { return &tx{c.s}, nil }
func (c *conn) BeginTx(context.Context, driver.TxOptions) (driver.Tx, error) { return &tx{c.s}, nil }
func (c *conn) ExecContext(context.Context, string, []driver.NamedValue) (driver.Result, error) {
	return driver.RowsAffected(1), nil
}
func (c *conn) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &rows{s: c.s}, nil
}

type tx struct{ s *state }

func (t *tx) Commit() error { return nil }
func (t *tx) Rollback() error {
	if t.s.rollbackStarted != nil {
		t.s.rollbackOnce.Do(func() { close(t.s.rollbackStarted) })
		<-t.s.rollbackRelease
	}
	return nil
}

type rows struct {
	set int
	s   *state
	n   int
}

func (r *rows) Columns() []string { return []string{"value"} }
func (r *rows) Close() error {
	if r.s.closeStarted != nil {
		r.s.closeOnce.Do(func() { close(r.s.closeStarted) })
		<-r.s.closeRelease
	}
	return nil
}
func (r *rows) Next(dest []driver.Value) error {
	if r.n > 0 {
		return io.EOF
	}
	r.n++
	dest[0] = int64(7)
	return nil
}
func setup(t *testing.T, s *state) (*sql.DB, *sqlguard.DB, *operation.Engine) {
	t.Helper()
	raw := sql.OpenDB(connector{s})
	raw.SetMaxOpenConns(1)
	t.Cleanup(func() { raw.Close() })
	e, _ := operation.New(operation.Config{Name: "database", MaxConcurrent: 1})
	guard, err := sqlguard.New(raw, e, func(context.Context) operation.Request { return operation.Request{Resource: "database"} })
	if err != nil {
		t.Fatal(err)
	}
	return raw, guard, e
}
func TestStreamsHoldUntilActualDriverClose(t *testing.T) {
	s := &state{closeStarted: make(chan struct{}), closeRelease: make(chan struct{})}
	_, db, e := setup(t, s)
	r, err := db.QueryContext(context.Background(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Active != 1 {
		t.Fatal("query returned and freed permit")
	}
	if !r.Next() {
		t.Fatal("missing row")
	}
	var v int
	if r.Scan(&v) != nil || v != 7 {
		t.Fatal("scan failed")
	}
	done := make(chan struct{})
	go func() { defer close(done); _ = r.Close() }()
	<-s.closeStarted
	if e.Snapshot().Active != 1 {
		t.Fatal("close released before driver cleanup")
	}
	close(s.closeRelease)
	<-done
	_ = r.Close()
	if e.Snapshot().Active != 0 {
		t.Fatal("stream leaked")
	}
}
func TestCanceledTransactionWaitsForRollback(t *testing.T) {
	s := &state{rollbackStarted: make(chan struct{}), rollbackRelease: make(chan struct{})}
	_, db, e := setup(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-s.rollbackStarted
	if e.Snapshot().Active != 1 {
		t.Fatal("canceled transaction released before rollback")
	}
	close(s.rollbackRelease)
	deadline := time.Now().Add(time.Second)
	for e.Snapshot().Active > 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if e.Snapshot().Active != 0 {
		t.Fatal("rollback leaked")
	}
	if !errors.Is(tx.Commit(), sql.ErrTxDone) {
		t.Fatal("terminal transaction usable")
	}
}
func TestIterationTransactionAndPoolWait(t *testing.T) {
	raw, db, e := setup(t, &state{})
	r, err := db.QueryContext(context.Background(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	for r.Next() {
		var v int
		if r.Scan(&v) != nil {
			t.Fatal("scan")
		}
	}
	if e.Snapshot().Active != 0 {
		t.Fatal("terminal iteration retained slot")
	}
	if err := db.InTx(context.Background(), nil, func(tx *sqlguard.Tx) error { var n int; return tx.QueryRowContext(tx.Context(), "row").Scan(&n) }); err != nil {
		t.Fatal(err)
	}
	if e.Snapshot().Active != 0 {
		t.Fatal("transaction leaked")
	}
	conn, err := raw.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := db.BeginTx(ctx, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if e.Snapshot().Active != 0 {
		t.Fatal("pool timeout leaked")
	}
	conn.Close()
	if o, err := (regulator.SQLPool{DB: raw}).Sample(context.Background()); err != nil || o.Value != 0 {
		t.Fatal(o, err)
	}
	registry := prometheus.NewRegistry()
	if err := metrics.Register(registry, e); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Gather(); err != nil {
		t.Fatal(err)
	}
	var absent *middleware.Service
	if err := metrics.Register(registry, absent); err == nil {
		t.Fatal("typed nil accepted")
	}
}

func TestScanFailureAndTransactionPanic(t *testing.T) {
	_, db, e := setup(t, &state{})
	r, err := db.QueryContext(context.Background(), "row")
	if err != nil {
		t.Fatal(err)
	}
	if !r.Next() {
		t.Fatal("missing row")
	}
	var value struct{}
	if r.Scan(&value) == nil {
		t.Fatal("invalid scan accepted")
	}
	if e.Snapshot().Active != 0 || e.Statistics().Work[0].Completed[1] != 1 {
		t.Fatal("scan failure not released and classified")
	}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		_ = db.InTx(context.Background(), nil, func(*sqlguard.Tx) error { panic("body") })
	}()
	if e.Snapshot().Active != 0 || e.Statistics().Work[0].Completed[3] != 1 {
		t.Fatal("panic rollback leaked or misclassified")
	}
}

func (r *rows) HasNextResultSet() bool { return r.s.multiple && r.set == 0 }
func (r *rows) NextResultSet() error {
	if !r.HasNextResultSet() {
		return io.EOF
	}
	r.set++
	r.n = 0
	return nil
}
func TestMultipleResultSetsRetainOnePermit(t *testing.T) {
	_, db, e := setup(t, &state{multiple: true})
	r, err := db.QueryContext(context.Background(), "multiple")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if !r.Next() {
		t.Fatal("first set missing")
	}
	var v int
	if r.Scan(&v) != nil {
		t.Fatal("scan")
	}
	if r.Next() {
		t.Fatal("unexpected extra row")
	}
	if e.Snapshot().Active != 1 {
		t.Fatal("released between result sets")
	}
	if !r.NextResultSet() || !r.Next() {
		t.Fatal("second set missing")
	}
	if r.Scan(&v) != nil {
		t.Fatal("scan")
	}
	if r.Next() || r.NextResultSet() || e.Snapshot().Active != 0 {
		t.Fatal("final set did not release")
	}
}
