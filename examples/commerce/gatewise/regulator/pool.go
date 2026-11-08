package regulator

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"time"
)

// SQLPool reports current connection utilization, not availability or queue
// length. The pool must have a finite MaxOpenConns; Stats makes no SQL calls.
type SQLPool struct{ DB *sql.DB }

func (p SQLPool) Sample(ctx context.Context) (decision.Observation, error) {
	if err := ctx.Err(); err != nil {
		return decision.Observation{}, err
	}
	if p.DB == nil {
		return decision.Observation{}, fmt.Errorf("regulator: missing SQL pool")
	}
	s := p.DB.Stats()
	if s.MaxOpenConnections <= 0 {
		return decision.Observation{}, fmt.Errorf("regulator: configure finite MaxOpenConns")
	}
	return decision.Observation{State: "measured", Value: float64(s.InUse) / float64(s.MaxOpenConnections), ObservedAt: time.Now()}, nil
}
