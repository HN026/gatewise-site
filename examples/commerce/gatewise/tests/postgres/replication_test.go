package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"github.com/HN026/gatewise/postgres"
	"io"
	"testing"
	"time"
)

type connector struct{ values [][]driver.Value }

func (c connector) Connect(context.Context) (driver.Conn, error) { return connection{c.values}, nil }
func (c connector) Driver() driver.Driver                        { return driverImpl{} }

type driverImpl struct{}

func (driverImpl) Open(string) (driver.Conn, error) { return nil, errors.New("unused") }

type connection struct{ values [][]driver.Value }

func (connection) Prepare(string) (driver.Stmt, error) { return nil, errors.New("unused") }
func (connection) Close() error                        { return nil }
func (connection) Begin() (driver.Tx, error)           { return nil, errors.New("unused") }
func (c connection) QueryContext(context.Context, string, []driver.NamedValue) (driver.Rows, error) {
	return &rows{values: c.values}, nil
}

type rows struct {
	values [][]driver.Value
	index  int
}

func (*rows) Columns() []string { return []string{"recovery", "state", "lag", "age"} }
func (*rows) Close() error      { return nil }
func (r *rows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
func TestPhysicalStandbyStates(t *testing.T) {
	good := []driver.Value{false, "streaming", float64(0), float64(.5)}
	for _, test := range []struct {
		name      string
		values    [][]driver.Value
		available bool
		lag       float64
	}{
		{"idle", [][]driver.Value{good}, true, 0},
		{"backlog", [][]driver.Value{{false, "streaming", float64(131072), float64(.5)}}, true, 131072},
		{"offline", nil, false, 0},
		{"duplicate", [][]driver.Value{good, good}, false, 0},
		{"stale", [][]driver.Value{{false, "streaming", float64(0), float64(4)}}, false, 0},
		{"missing position", [][]driver.Value{{false, "streaming", nil, float64(.5)}}, false, 0},
		{"wrong server", [][]driver.Value{{true, "streaming", float64(0), float64(.5)}}, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := sql.OpenDB(connector{test.values})
			defer db.Close()
			p, err := postgres.NewReplication(postgres.Config{DB: db, ApplicationName: "standby", StaleAfter: 3 * time.Second})
			if err != nil {
				t.Fatal(err)
			}
			o, err := p.Sample(context.Background())
			if (err == nil) != test.available || p.Snapshot().Available != test.available {
				t.Fatal(o, err, p.Snapshot())
			}
			if test.available && (o.State != "measured" || o.Value != test.lag) {
				t.Fatal(o)
			}
		})
	}
}
