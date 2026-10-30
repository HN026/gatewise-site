package regulator_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/HN026/gatewise/regulator"
	"testing"
	"time"
)

type poolConnector struct{}

func (poolConnector) Connect(context.Context) (driver.Conn, error) { return poolConn{}, nil }
func (poolConnector) Driver() driver.Driver                        { return poolDriver{} }

type poolDriver struct{}

func (poolDriver) Open(string) (driver.Conn, error) { return poolConn{}, nil }

type poolConn struct{}

func (poolConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (poolConn) Close() error                        { return nil }
func (poolConn) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func TestContentionRequiresWaitAndRecovers(t *testing.T) {
	db := sql.OpenDB(poolConnector{})
	defer db.Close()
	db.SetMaxOpenConns(1)
	source, err := regulator.NewSQLPoolContention(regulator.SQLPoolContentionConfig{DB: db, RecoveryQuiet: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	hold, err := db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer hold.Close()
	sample := func(want float64) {
		t.Helper()
		o, err := source.Sample(context.Background())
		if err != nil || o.Value != want || o.State != "measured" {
			t.Fatal(o, err, source.Snapshot())
		}
	}
	sample(0) // Full occupancy, with no contention, is not overload evidence.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		c, err := db.Conn(ctx)
		if c != nil {
			c.Close()
		}
		done <- err
	}()
	end := time.Now().Add(time.Second)
	for db.Stats().WaitCount == 0 && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if db.Stats().WaitCount == 0 {
		t.Fatal("wait was not registered")
	}
	sample(1)
	if source.Snapshot().WaitCountDelta != 1 {
		t.Fatal(source.Snapshot())
	}
	cancel()
	<-done
	time.Sleep(25 * time.Millisecond)
	sample(1) // Still full after a wait: do not declare recovery.
	hold.Close()
	sample(0)
	// A second episode must also respect the quiet interval after the pool drains.
	hold2, _ := db.Conn(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	done2 := make(chan struct{})
	go func() {
		defer close(done2)
		c, _ := db.Conn(ctx2)
		if c != nil {
			c.Close()
		}
	}()
	end = time.Now().Add(time.Second)
	for db.Stats().WaitCount < 2 && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if db.Stats().WaitCount < 2 {
		t.Fatal("second wait missing")
	}
	sample(1)
	cancel2()
	<-done2
	hold2.Close()
	sample(1)
	time.Sleep(25 * time.Millisecond)
	sample(0)
	fresh, _ := regulator.NewSQLPoolContention(regulator.SQLPoolContentionConfig{DB: db})
	o, err := fresh.Sample(context.Background())
	if err != nil || o.Value != 0 {
		t.Fatal("historical waits treated as new", o, err)
	}
	cancelCtx, stop := context.WithCancel(context.Background())
	stop()
	if _, err := fresh.Sample(cancelCtx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(0)
	if _, err := fresh.Sample(context.Background()); err == nil {
		t.Fatal("unbounded pool accepted")
	}
}
