// Package sqlguard adapts database/sql operations to operation.Engine. It does
// not install a driver, own the database, or enforce inside PostgreSQL itself.
package sqlguard

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/HN026/gatewise/operation"
	"sync"
)

type Metadata func(context.Context) operation.Request
type DB struct {
	raw      *sql.DB
	engine   *operation.Engine
	metadata Metadata
}

func New(db *sql.DB, engine *operation.Engine, metadata Metadata) (*DB, error) {
	if db == nil || engine == nil || metadata == nil {
		return nil, fmt.Errorf("sqlguard: database, engine and trusted metadata required")
	}
	return &DB{db, engine, metadata}, nil
}
func outcome(err error) operation.Outcome {
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return operation.Outcome{Err: err}
}
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (result sql.Result, err error) {
	p, err := d.engine.Begin(ctx, d.metadata(ctx))
	if err != nil {
		return nil, err
	}
	done := false
	defer func() { o := outcome(err); o.Panicked = !done; p.Finish(o) }()
	result, err = d.raw.ExecContext(p.Context(), query, args...)
	done = true
	return
}

// QueryContext holds admission until Rows.Close or terminal iteration. Always
// defer Close. Abandoning rows without closing them retains the permit.
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	p, err := d.engine.Begin(ctx, d.metadata(ctx))
	if err != nil {
		return nil, err
	}
	transferred := false
	defer func() {
		if !transferred {
			p.Finish(operation.Outcome{Panicked: true})
		}
	}()
	rows, err := d.raw.QueryContext(p.Context(), query, args...)
	if err != nil {
		p.Finish(outcome(err))
		transferred = true
		return nil, err
	}
	r := &Rows{raw: rows, permit: p, done: make(chan struct{})}
	transferred = true
	return r, nil
}

type Rows struct {
	raw        *sql.Rows
	permit     *operation.Permit
	done       chan struct{}
	once       sync.Once
	report     func(error)
	pending    bool
	errorMu    sync.Mutex
	firstError error
}

func (r *Rows) Next() bool {
	if r.pending {
		return false
	}
	if r.raw.Next() {
		return true
	}
	if r.raw.NextResultSet() {
		r.pending = true
		return false
	}
	_ = r.Close()
	return false
}

// NextResultSet is explicit for multiple-result drivers; callers still close on exit.
func (r *Rows) NextResultSet() bool {
	if r.pending {
		r.pending = false
		return true
	}
	if r.raw.NextResultSet() {
		return true
	}
	_ = r.Close()
	return false
}
func (r *Rows) Scan(dest ...any) error {
	err := r.raw.Scan(dest...)
	if err != nil {
		r.errorMu.Lock()
		if r.firstError == nil {
			r.firstError = err
		}
		r.errorMu.Unlock()
		if r.report != nil {
			r.report(err)
		}
		_ = r.Close()
	}
	return err
}
func (r *Rows) Err() error                 { return r.raw.Err() }
func (r *Rows) Columns() ([]string, error) { return r.raw.Columns() }
func (r *Rows) Close() error {
	err := r.raw.Close()
	r.once.Do(func() {
		r.errorMu.Lock()
		if err == nil {
			err = r.firstError
		}
		r.errorMu.Unlock()
		if err == nil {
			err = r.raw.Err()
		}
		if r.report != nil {
			r.report(err)
		}
		if r.permit != nil {
			r.permit.Finish(outcome(err))
		}
		close(r.done)
	})
	return err
}

type Row struct {
	rows *Rows
	err  error
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	rows, err := d.QueryContext(ctx, query, args...)
	return &Row{rows, err}
}
func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return sql.ErrNoRows
	}
	return r.rows.Scan(dest...)
}

// BeginTx owns cancellation cleanup. It acquires a connection with the caller
// context, then begins with a noncanceling control context so database/sql cannot
// mark a transaction done before an asynchronous driver rollback actually ends.
// Begin/Commit/Rollback driver control calls may wait for driver network timeouts;
// the permit remains held throughout. Queries use caller cancellation normally.
func (d *DB) BeginTx(ctx context.Context, options *sql.TxOptions) (*Tx, error) {
	p, err := d.engine.Begin(ctx, d.metadata(ctx))
	if err != nil {
		return nil, err
	}
	conn, err := d.raw.Conn(p.Context())
	if err != nil {
		p.Finish(outcome(err))
		return nil, err
	}
	tx, err := conn.BeginTx(context.WithoutCancel(p.Context()), options)
	if err != nil {
		conn.Close()
		p.Finish(outcome(err))
		return nil, err
	}
	t := &Tx{raw: tx, conn: conn, permit: p, ctx: p.Context(), done: make(chan struct{}), rows: map[*Rows]struct{}{}}
	if err := ctx.Err(); err != nil {
		_ = t.Rollback()
		return nil, err
	}
	go func() {
		select {
		case <-ctx.Done():
			_ = t.Rollback()
		case <-t.done:
		}
	}()
	return t, nil
}

type Tx struct {
	rowsMu   sync.Mutex
	rows     map[*Rows]struct{}
	panicked bool
	raw      *sql.Tx
	conn     *sql.Conn
	permit   *operation.Permit
	ctx      context.Context
	mu       sync.Mutex
	terminal bool
	done     chan struct{}
	errorsMu sync.Mutex
	firstErr error
}

func (t *Tx) Context() context.Context { return t.ctx }
func (t *Tx) record(err error) {
	if err == nil || errors.Is(err, sql.ErrNoRows) {
		return
	}
	t.errorsMu.Lock()
	if t.firstErr == nil {
		t.firstErr = err
	}
	t.errorsMu.Unlock()
}
func (t *Tx) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return nil, sql.ErrTxDone
	}
	result, err := t.raw.ExecContext(ctx, query, args...)
	t.record(err)
	return result, err
}
func (t *Tx) QueryContext(ctx context.Context, query string, args ...any) (*Rows, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return nil, sql.ErrTxDone
	}
	rows, err := t.raw.QueryContext(ctx, query, args...)
	if err != nil {
		t.record(err)
		return nil, err
	}
	r := &Rows{raw: rows, done: make(chan struct{})}
	r.report = func(err error) { t.record(err); t.rowsMu.Lock(); delete(t.rows, r); t.rowsMu.Unlock() }
	t.rowsMu.Lock()
	t.rows[r] = struct{}{}
	t.rowsMu.Unlock()
	return r, nil
}
func (t *Tx) QueryRowContext(ctx context.Context, query string, args ...any) *Row {
	r, err := t.QueryContext(ctx, query, args...)
	return &Row{r, err}
}
func (t *Tx) finish(commit bool) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.terminal {
		return sql.ErrTxDone
	}
	t.terminal = true
	var err error
	if commit && t.ctx.Err() == nil {
		err = t.raw.Commit()
	} else {
		err = t.raw.Rollback()
	}
	t.record(err)
	t.rowsMu.Lock()
	pending := make([]*Rows, 0, len(t.rows))
	for r := range t.rows {
		pending = append(pending, r)
	}
	t.rowsMu.Unlock()
	for _, r := range pending {
		_ = r.Close()
	}
	closeErr := t.conn.Close()
	t.record(closeErr)
	t.errorsMu.Lock()
	failure := t.firstErr
	t.errorsMu.Unlock()
	o := outcome(failure)
	o.Panicked = t.panicked
	t.permit.Finish(o)
	close(t.done)
	if commit && t.ctx.Err() != nil && err == nil {
		return t.ctx.Err()
	}
	return err
}
func (t *Tx) Commit() error   { return t.finish(true) }
func (t *Tx) Rollback() error { return t.finish(false) }

// InTx guarantees rollback on callback failure or panic, and commits only on a
// successful return. Use tx methods, not the outer DB wrapper, inside the body.
func (d *DB) InTx(ctx context.Context, options *sql.TxOptions, fn func(*Tx) error) (err error) {
	if fn == nil {
		return fmt.Errorf("sqlguard: nil transaction body")
	}
	tx, err := d.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	completed := false
	defer func() {
		if !completed {
			tx.mu.Lock()
			tx.panicked = true
			tx.mu.Unlock()
		}
		_ = tx.Rollback()
	}()
	if err = fn(tx); err != nil {
		completed = true
		tx.record(err)
		return err
	}
	completed = true
	return tx.Commit()
}
