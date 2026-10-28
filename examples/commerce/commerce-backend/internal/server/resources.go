package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/HN026/gatewise-examples/commerce/internal/handler"
	"github.com/HN026/gatewise-examples/commerce/internal/store"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/regulator"
	"io"
	"net/http"
	"runtime"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"
)

type resourcePlan struct {
	Bytes   int    `json:"bytes"`
	Workers int    `json:"workers"`
	Key     string `json:"key"`
}
type planKey struct{}
type resourceWork struct {
	memory                                                                     *regulator.Memory
	liveBytes, peakBytes, liveWorkers, peakWorkers, writeBytes, peakWriteBytes atomic.Int64
	mu                                                                         sync.Mutex
	keyActive                                                                  map[[32]byte]int
	hotPeak                                                                    int
	holdMS                                                                     atomic.Int64
}

func newResources() *resourceWork {
	m, _ := regulator.NewMemory(regulator.MemoryConfig{LimitBytes: 128 << 20})
	w := &resourceWork{memory: m, keyActive: map[[32]byte]int{}}
	w.holdMS.Store(120)
	return w
}
func peak(max *atomic.Int64, n int64) {
	for old := max.Load(); n > old && !max.CompareAndSwap(old, n); old = max.Load() {
	}
}
func (w *resourceWork) parse(next http.Handler) http.Handler {
	return http.HandlerFunc(func(out http.ResponseWriter, r *http.Request) {
		var p resourcePlan
		r.Body = http.MaxBytesReader(out, r.Body, 4096)
		d := json.NewDecoder(r.Body)
		d.DisallowUnknownFields()
		if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Bytes < 0 || p.Bytes > 8<<20 || p.Workers < 0 || p.Workers > 32 || len(p.Key) > 128 {
			handler.JSON(out, 400, map[string]string{"error": "invalid_resource_plan"})
			return
		}
		for _, c := range []byte(p.Key) {
			if c < 33 || c > 126 {
				handler.JSON(out, 400, map[string]string{"error": "invalid_resource_key"})
				return
			}
		}
		next.ServeHTTP(out, r.WithContext(context.WithValue(r.Context(), planKey{}, p)))
	})
}
func plannedCost(r *http.Request) (decision.Cost, error) {
	p, ok := r.Context().Value(planKey{}).(resourcePlan)
	if !ok {
		return decision.Cost{}, fmt.Errorf("no validated plan")
	}
	return decision.Cost{WriteBytes: int64(p.Bytes), Key: p.Key}, nil
}
func (w *resourceWork) operation(kind string, s *store.Store) http.HandlerFunc {
	return func(out http.ResponseWriter, r *http.Request) {
		p := r.Context().Value(planKey{}).(resourcePlan)
		hold := time.Duration(w.holdMS.Load()) * time.Millisecond
		switch kind {
		case "allocation":
			if p.Bytes == 0 {
				handler.JSON(out, 400, map[string]string{"error": "bytes_required"})
				return
			}
			block := make([]byte, p.Bytes)
			for i := 0; i < len(block); i += 4096 {
				block[i] = byte(i + 1)
			}
			n := w.liveBytes.Add(int64(len(block)))
			peak(&w.peakBytes, n)
			defer w.liveBytes.Add(-int64(len(block)))
			timer := time.NewTimer(hold)
			defer timer.Stop()
			select {
			case <-timer.C:
			case <-r.Context().Done():
				return
			}
			runtime.KeepAlive(block)
		case "fanout":
			if p.Workers == 0 {
				handler.JSON(out, 400, map[string]string{"error": "workers_required"})
				return
			}
			release := make(chan struct{})
			var workers sync.WaitGroup
			for i := 0; i < p.Workers; i++ {
				workers.Add(1)
				go func() {
					defer workers.Done()
					n := w.liveWorkers.Add(1)
					peak(&w.peakWorkers, n)
					defer w.liveWorkers.Add(-1)
					<-release
				}()
			}
			timer := time.NewTimer(hold)
			select {
			case <-timer.C:
			case <-r.Context().Done():
			}
			timer.Stop()
			close(release)
			workers.Wait()
			if r.Context().Err() != nil {
				return
			}
		case "write", "key_write":
			if p.Bytes < 1 || p.Key == "" {
				handler.JSON(out, 400, map[string]string{"error": "write_plan_required"})
				return
			}
			if values := r.Header.Values(middleware.CostHeader); len(values) > 0 {
				var cost decision.Cost
				if len(values) != 1 || json.Unmarshal([]byte(values[0]), &cost) != nil || cost.WriteBytes != int64(p.Bytes) || cost.Key != p.Key {
					handler.JSON(out, 400, map[string]string{"error": "write_plan_mismatch"})
					return
				}
			}
			ctx, cancel := context.WithTimeout(r.Context(), s.Timeout)
			defer cancel()
			tx, err := s.DB.BeginTx(ctx, nil)
			if err != nil {
				handler.JSON(out, 504, map[string]string{"error": "resource_database_timeout"})
				return
			}
			defer tx.Rollback()
			n := w.writeBytes.Add(int64(p.Bytes))
			peak(&w.peakWriteBytes, n)
			defer w.writeBytes.Add(-int64(p.Bytes))
			hash := sha256.Sum256([]byte(p.Key))
			w.mu.Lock()
			w.keyActive[hash]++
			if w.keyActive[hash] > w.hotPeak {
				w.hotPeak = w.keyActive[hash]
			}
			w.mu.Unlock()
			defer func() {
				w.mu.Lock()
				w.keyActive[hash]--
				if w.keyActive[hash] == 0 {
					delete(w.keyActive, hash)
				}
				w.mu.Unlock()
			}()
			payload := make([]byte, p.Bytes)
			for i := range payload {
				payload[i] = byte(i*31 + int(hash[i%32]))
			}
			_, err = tx.ExecContext(ctx, `INSERT INTO resource_writes(key,payload) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET payload=excluded.payload`, hex.EncodeToString(hash[:]), payload)
			if err == nil {
				_, err = tx.ExecContext(ctx, `SELECT pg_sleep($1)`, hold.Seconds())
			}
			if err == nil {
				err = tx.Commit()
			}
			if err != nil {
				handler.JSON(out, 504, map[string]string{"error": "resource_database_timeout"})
				return
			}
		}
		handler.JSON(out, 200, map[string]any{"kind": kind, "logical_write_bytes": p.Bytes})
	}
}
func (w *resourceWork) state(out http.ResponseWriter, r *http.Request) {
	m, err := w.memory.Sample(r.Context())
	_ = m
	w.mu.Lock()
	keys, hotPeak := len(w.keyActive), w.hotPeak
	w.mu.Unlock()
	state := map[string]any{"memory": w.memory.Snapshot(), "memory_available": err == nil, "allocation_live_bytes": w.liveBytes.Load(), "allocation_peak_bytes": w.peakBytes.Load(), "workers_active": w.liveWorkers.Load(), "workers_peak": w.peakWorkers.Load(), "write_bytes_active": w.writeBytes.Load(), "write_bytes_peak": w.peakWriteBytes.Load(), "active_keys": keys, "key_peak_concurrent": hotPeak, "hold_ms": w.holdMS.Load(), "goroutines": runtime.NumGoroutine()}
	handler.JSON(out, 200, state)
}
func (w *resourceWork) control(out http.ResponseWriter, r *http.Request) {
	var p struct {
		HoldMS  int  `json:"hold_ms"`
		Collect bool `json:"collect"`
		Reset   bool `json:"reset_peaks"`
	}
	r.Body = http.MaxBytesReader(out, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.HoldMS < 0 || p.HoldMS > 500 {
		handler.JSON(out, 400, map[string]string{"error": "invalid_resource_control"})
		return
	}
	w.holdMS.Store(int64(p.HoldMS))
	if p.Collect {
		runtime.GC()
		debug.FreeOSMemory()
	}
	if p.Reset {
		w.peakBytes.Store(w.liveBytes.Load())
		w.peakWorkers.Store(w.liveWorkers.Load())
		w.peakWriteBytes.Store(w.writeBytes.Load())
		w.mu.Lock()
		w.hotPeak = 0
		w.mu.Unlock()
	}
	w.state(out, r)
}
func (w *resourceWork) observation(out http.ResponseWriter, r *http.Request) {
	var provider decision.Provider = w.memory
	if r.PathValue("name") == "goroutines" {
		provider = regulator.Goroutines{}
	} else if r.PathValue("name") != "memory" {
		http.NotFound(out, r)
		return
	}
	o, err := provider.Sample(r.Context())
	if err != nil {
		handler.JSON(out, 503, map[string]string{"error": "resource_measurement_unavailable"})
		return
	}
	handler.JSON(out, 200, o)
}
func resourceEngine(mode string, w *resourceWork) (*decision.Engine, []decision.Collector, error) {
	e, err := decision.New(decision.Config{Mode: decision.Mode(mode), Rules: []decision.Rule{
		{Name: "memory", Scope: decision.Scope{Kind: "resource", Key: "allocation"}, Threshold: .65, MaxAge: time.Second, OnUnknown: "limit", UnknownLimit: 2, Pressure: decision.Effect{MaxConcurrent: 2}},
		{Name: "goroutines", Scope: decision.Scope{Kind: "resource", Key: "fanout"}, Threshold: 180, MaxAge: time.Second, OnUnknown: "limit", UnknownLimit: 2, Pressure: decision.Effect{MaxConcurrent: 2}},
	}, Budgets: []decision.Budget{{Name: "writes", Resource: "write", MaxWriteBytes: 256 << 10}, {Name: "keys", Resource: "key_write", Keys: &decision.KeyBudget{MaxConcurrent: 1, MaxTracked: 32, IdleTTL: 2 * time.Second, Missing: "reject", Overflow: "shared"}}}})
	if err != nil {
		return nil, nil, err
	}
	return e, []decision.Collector{{Name: "memory", Provider: w.memory, Interval: 50 * time.Millisecond, Timeout: 40 * time.Millisecond}, {Name: "goroutines", Provider: regulator.Goroutines{}, Interval: 50 * time.Millisecond, Timeout: 40 * time.Millisecond}}, nil
}

func (w *resourceWork) persisted(s *store.Store) http.HandlerFunc {
	return func(out http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()
		var rows, maxBytes int64
		err := s.DB.QueryRowContext(ctx, `SELECT count(*),coalesce(max(octet_length(payload)),0) FROM resource_writes`).Scan(&rows, &maxBytes)
		if err != nil {
			handler.JSON(out, 503, map[string]string{"error": "resource_readback_unavailable"})
			return
		}
		handler.JSON(out, 200, map[string]int64{"rows": rows, "max_logical_bytes": maxBytes})
	}
}
