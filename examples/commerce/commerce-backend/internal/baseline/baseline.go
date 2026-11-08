// Package baseline contains independent reference policies for the demo only.
// These are per-process, classless and fail-fast. They are not Gatewise APIs.
package baseline

import (
	"encoding/json"
	"golang.org/x/time/rate"
	"net/http"
	"sync/atomic"
)

type Policy struct {
	mode                             string
	limit                            int
	rps                              float64
	burst                            int
	slots                            chan struct{}
	bucket                           *rate.Limiter
	active, peak, admitted, rejected atomic.Int64
}
type Snapshot struct {
	Mode           string  `json:"mode"`
	ExecutionLimit int     `json:"execution_limit"`
	Rate           float64 `json:"rate_per_second"`
	Burst          int     `json:"burst"`
	Active         int64   `json:"active"`
	Peak           int64   `json:"peak"`
	Admitted       int64   `json:"admitted"`
	Rejected       int64   `json:"rejected"`
}

// New expects validated settings. Non-reference modes only observe handler work.
func New(mode string, limit int, rps float64, burst int) *Policy {
	p := &Policy{mode: mode, limit: limit, rps: rps, burst: burst}
	if mode == "semaphore" {
		p.slots = make(chan struct{}, limit)
	}
	if mode == "rate" {
		p.bucket = rate.NewLimiter(rate.Limit(rps), burst)
	}
	return p
}
func (p *Policy) Snapshot() Snapshot {
	limit := 0
	rps := 0.
	burst := 0
	if p.slots != nil {
		limit = p.limit
	}
	if p.bucket != nil {
		rps = p.rps
		burst = p.burst
	}
	return Snapshot{p.mode, limit, rps, burst, p.active.Load(), p.peak.Load(), p.admitted.Load(), p.rejected.Load()}
}
func (p *Policy) Wrap(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Context().Err() != nil {
			return
		}
		status := 0
		reason := ""
		if p.slots != nil {
			select {
			case p.slots <- struct{}{}:
				defer func() { <-p.slots }()
			default:
				status = 503
				reason = "concurrency_limit"
			}
		}
		if p.bucket != nil && !p.bucket.Allow() {
			status = 429
			reason = "static_rate_limit"
		}
		if status != 0 {
			p.rejected.Add(1)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "service_overloaded", "reason": reason})
			return
		}
		n := p.active.Add(1)
		p.admitted.Add(1)
		for old := p.peak.Load(); n > old; old = p.peak.Load() {
			if p.peak.CompareAndSwap(old, n) {
				break
			}
		}
		defer p.active.Add(-1)
		next.ServeHTTP(w, r)
	})
}
