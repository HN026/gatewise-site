package handler

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/HN026/gatewise-examples/commerce/internal/store"
	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/priority"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

type Control struct {
	Multiplier   int `json:"work_multiplier"`
	DelayMS      int `json:"sql_delay_ms"`
	FailureEvery int `json:"failure_every"`
}
type API struct {
	Store       *store.Store
	Guard       *middleware.Service
	Mode        string
	mu          sync.RWMutex
	control     Control
	requests    atomic.Uint64
	sqlFailures atomic.Uint64
}

func New(s *store.Store, g *middleware.Service, mode string) *API {
	return &API{Store: s, Guard: g, Mode: mode, control: Control{Multiplier: 1}}
}
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func decode(w http.ResponseWriter, r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if e := d.Decode(v); e != nil {
		return e
	}
	if d.Decode(new(any)) != io.EOF {
		return errors.New("one JSON object required")
	}
	return nil
}
func (a *API) config() (Control, bool) {
	a.mu.RLock()
	c := a.control
	a.mu.RUnlock()
	n := a.requests.Add(1)
	return c, c.FailureEvery > 0 && n%uint64(c.FailureEvery) == 0
}
func (a *API) fail(w http.ResponseWriter, r *http.Request, e error) {
	if errors.Is(e, store.ErrStock) {
		JSON(w, 409, map[string]string{"error": "insufficient_stock"})
		return
	}
	if errors.Is(e, store.ErrConflict) {
		JSON(w, 409, map[string]string{"error": "idempotency_conflict"})
		return
	}
	if errors.Is(e, sql.ErrNoRows) {
		JSON(w, 404, map[string]string{"error": "order_not_found"})
		return
	}
	a.sqlFailures.Add(1)
	if r.Context().Err() != nil {
		return
	}
	if errors.Is(e, context.DeadlineExceeded) {
		JSON(w, 504, map[string]string{"error": "database_timeout"})
		return
	}
	JSON(w, 500, map[string]string{"error": "database_error"})
}
func (a *API) Checkout(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if len(key) < 1 || len(key) > 128 {
		JSON(w, 400, map[string]string{"error": "idempotency_key_required"})
		return
	}
	var body struct {
		Amount int64 `json:"amount_cents"`
	}
	if decode(w, r, &body) != nil || body.Amount <= 0 || body.Amount > 1000000000 {
		JSON(w, 400, map[string]string{"error": "invalid_amount"})
		return
	}
	c, fail := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	o, created, e := a.Store.Checkout(ctx, key, body.Amount, time.Duration(c.DelayMS)*time.Millisecond, fail)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	status := 200
	if created {
		status = 201
	}
	w.Header().Set("Location", "/orders/"+o.ID)
	JSON(w, status, o)
}
func (a *API) Get(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	o, e := a.Store.Get(ctx, r.PathValue("id"))
	if e != nil {
		a.fail(w, r, e)
		return
	}
	JSON(w, 200, o)
}
func (a *API) Rebuild(w http.ResponseWriter, r *http.Request) {
	c, fail := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	total, e := a.Store.Rebuild(ctx, c.Multiplier, time.Duration(c.DelayMS)*time.Millisecond, fail)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	JSON(w, 200, map[string]any{"report": a.Store.Role, "total": total, "work_multiplier": c.Multiplier})
}
func (a *API) Health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 300*time.Millisecond)
	defer cancel()
	if a.Store.DB.PingContext(ctx) != nil {
		JSON(w, 503, map[string]string{"status": "database_unavailable"})
		return
	}
	JSON(w, 200, map[string]string{"status": "ok"})
}
func (a *API) State(w http.ResponseWriter, r *http.Request) {
	a.mu.RLock()
	c := a.control
	a.mu.RUnlock()
	v := map[string]any{"mode": a.Mode, "role": a.Store.Role, "pool": a.Store.DB.Stats(), "control": c, "database_failures": a.sqlFailures.Load()}
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	v["heap_bytes"] = m.HeapAlloc
	v["goroutines"] = runtime.NumGoroutine()
	if a.Guard != nil {
		v["guard"] = a.Guard.Snapshot()
		v["controller"] = a.Guard.ControllerSnapshot()
		v["queue"] = a.Guard.QueueSnapshot()
		v["tenants"] = a.Guard.TenantSnapshot()
		v["decisions"] = a.Guard.DecisionSnapshot()
	}
	JSON(w, 200, v)
}
func (a *API) Update(w http.ResponseWriter, r *http.Request) {
	var c Control
	if decode(w, r, &c) != nil || c.Multiplier < 1 || c.Multiplier > 500 || c.DelayMS < 0 || c.DelayMS > 500 || c.FailureEvery < 0 || c.FailureEvery > 1000000 {
		JSON(w, 400, map[string]string{"error": "invalid_control"})
		return
	}
	a.mu.Lock()
	a.control = c
	a.mu.Unlock()
	a.requests.Store(0)
	JSON(w, 200, c)
}

func (a *API) Products(w http.ResponseWriter, r *http.Request) {
	limit := 10
	var e error
	if v := r.URL.Query().Get("limit"); v != "" {
		limit, e = strconv.Atoi(v)
	}
	if e != nil || limit < 1 || limit > 100 {
		JSON(w, 400, map[string]string{"error": "invalid_limit"})
		return
	}
	c, fail := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	products, e := a.Store.Products(ctx, limit, time.Duration(c.DelayMS)*time.Millisecond, fail)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	result := map[string]any{"products": products}
	if m, ok := priority.FromContext(r.Context()); ok {
		result["priority"] = m
	}
	JSON(w, 200, result)
}
func (a *API) Reserve(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	var b struct {
		ProductID int `json:"product_id"`
		Quantity  int `json:"quantity"`
	}
	if len(key) < 1 || len(key) > 128 || decode(w, r, &b) != nil || b.ProductID < 1 || b.ProductID > 20000 || b.Quantity < 1 || b.Quantity > 1000 {
		JSON(w, 400, map[string]string{"error": "invalid_reservation"})
		return
	}
	c, fail := a.config()
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	v, created, e := a.Store.Reserve(ctx, key, b.ProductID, b.Quantity, time.Duration(c.DelayMS)*time.Millisecond, fail)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	status := 200
	if created {
		status = 201
	}
	JSON(w, status, v)
}
func (a *API) Stock(w http.ResponseWriter, r *http.Request) {
	id, e := strconv.Atoi(r.PathValue("id"))
	if e != nil || id < 1 {
		JSON(w, 400, map[string]string{"error": "invalid_product"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), a.Store.Timeout)
	defer cancel()
	n, e := a.Store.Stock(ctx, id)
	if e != nil {
		a.fail(w, r, e)
		return
	}
	JSON(w, 200, map[string]any{"product_id": id, "available": n})
}

// Flow performs a real database-backed downstream read. This demo operation is
// read-only; downstream failure does not leave a partially committed checkout.
func (a *API) Flow(base string) http.HandlerFunc {
	target, err := url.Parse(base)
	if err != nil {
		panic(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: priority.Transport{Base: transport, Hosts: []string{target.Host}}, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(w http.ResponseWriter, r *http.Request) {
		req, e := http.NewRequestWithContext(r.Context(), "GET", base+"/catalog/products?limit=10", nil)
		if e != nil {
			JSON(w, 500, map[string]string{"error": "request_build_failed"})
			return
		}
		resp, e := client.Do(req)
		if e != nil {
			JSON(w, 502, map[string]string{"error": "catalog_unavailable"})
			return
		}
		defer resp.Body.Close()
		var downstream map[string]any
		if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&downstream) != nil {
			JSON(w, 502, map[string]string{"error": "catalog_response_invalid"})
			return
		}
		upstream, _ := priority.FromContext(r.Context())
		JSON(w, resp.StatusCode, map[string]any{"upstream": upstream, "downstream": downstream, "reason": downstream["reason"]})
	}
}
