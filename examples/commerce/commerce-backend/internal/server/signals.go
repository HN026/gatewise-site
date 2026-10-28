package server

// These loopback-only controls model signal delivery, not real resource sensors.
import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/HN026/gatewise/decision"
	"io"
	"math"
	"net/http"
	"sync"
	"time"
)

type signalControl struct {
	Value   float64 `json:"value"`
	State   string  `json:"state"`
	AgeMS   int     `json:"age_ms"`
	DelayMS int     `json:"delay_ms"`
	Error   bool    `json:"error"`
}
type signalControls struct {
	mu     sync.RWMutex
	values map[string]signalControl
}

func newSignals() *signalControls {
	return &signalControls{values: map[string]signalControl{"primary": {Value: .2, State: "measured"}, "secondary": {Value: .2, State: "measured"}}}
}
func (s *signalControls) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.RLock()
	c, ok := s.values[name]
	s.mu.RUnlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "PUT" {
		data, err := io.ReadAll(io.LimitReader(r.Body, 4097))
		d := json.NewDecoder(bytes.NewReader(data))
		d.DisallowUnknownFields()
		var v signalControl
		if err != nil || len(data) > 4096 || d.Decode(&v) != nil || d.Decode(new(any)) != io.EOF || math.IsNaN(v.Value) || math.IsInf(v.Value, 0) || v.Value < 0 || v.Value > 1 || v.State != "measured" && v.State != "unknown" || v.AgeMS < 0 || v.AgeMS > 10000 || v.DelayMS < 0 || v.DelayMS > 500 {
			w.WriteHeader(400)
			return
		}
		s.mu.Lock()
		s.values[name] = v
		s.mu.Unlock()
		_ = json.NewEncoder(w).Encode(v)
		return
	}
	if c.DelayMS > 0 {
		timer := time.NewTimer(time.Duration(c.DelayMS) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-r.Context().Done():
			return
		}
	}
	if c.Error {
		w.WriteHeader(503)
		_, _ = w.Write([]byte(`{"error":"controlled_provider_failure"}`))
		return
	}
	_ = json.NewEncoder(w).Encode(decision.Observation{Value: c.Value, State: c.State, ObservedAt: time.Now().Add(-time.Duration(c.AgeMS) * time.Millisecond)})
}
func signalEngine(mode, address string) (*decision.Engine, []decision.Collector, error) {
	e, err := decision.New(decision.Config{Mode: decision.Mode(mode), Rules: []decision.Rule{
		{Name: "primary", Threshold: .8, MaxAge: 600 * time.Millisecond, OnUnknown: "limit", UnknownLimit: 2, Pressure: decision.Effect{MaxConcurrent: 4}},
		{Name: "secondary", Threshold: .8, MaxAge: 600 * time.Millisecond, OnUnknown: "allow", Pressure: decision.Effect{Reject: true}},
	}})
	if err != nil {
		return nil, nil, err
	}
	var collectors []decision.Collector
	for _, name := range []string{"primary", "secondary"} {
		p, err := decision.NewHTTPProvider("http://" + address + "/signals/" + name)
		if err != nil {
			return nil, nil, fmt.Errorf("controlled source: %w", err)
		}
		collectors = append(collectors, decision.Collector{Name: name, Interval: 100 * time.Millisecond, Timeout: 50 * time.Millisecond, Provider: p})
	}
	return e, collectors, e.ValidateCollectors(collectors)
}
