// Package workload models a service whose latency increases under contention.
// This is a synthetic experiment fixture, not a production capacity model.
package workload

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

type Service struct {
	capacity               atomic.Int64
	active                 atomic.Int64
	peak                   atomic.Int64
	base, penalty, maximum time.Duration
}

func New(capacity int, base, penalty, maximum time.Duration) *Service {
	s := &Service{base: base, penalty: penalty, maximum: maximum}
	s.capacity.Store(int64(capacity))
	return s
}

func (s *Service) Control(w http.ResponseWriter, r *http.Request) {
	var value struct {
		Capacity int `json:"capacity"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&value) != nil || value.Capacity < 1 || value.Capacity > 1024 {
		http.Error(w, "capacity must be between 1 and 1024", http.StatusBadRequest)
		return
	}
	s.capacity.Store(int64(value.Capacity))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Service) State(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]int64{"capacity": s.capacity.Load(), "active": s.active.Load(), "peak_active": s.peak.Load()})
}

func (s *Service) Work(w http.ResponseWriter, r *http.Request) {
	n := s.active.Add(1)
	defer s.active.Add(-1)
	for peak := s.peak.Load(); n > peak && !s.peak.CompareAndSwap(peak, n); peak = s.peak.Load() {
	}
	delay := s.base
	if excess := n - s.capacity.Load(); excess > 0 {
		delay += time.Duration(excess) * s.penalty
	}
	if delay > s.maximum {
		delay = s.maximum
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-r.Context().Done():
		return
	case <-timer.C:
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
