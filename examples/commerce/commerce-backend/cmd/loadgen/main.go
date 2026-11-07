// loadgen schedules independent arrivals. It never retries rejected requests.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"
)

type TenantWeight struct {
	ID     string `json:"id"`
	Weight int    `json:"weight"`
}
type Config struct {
	Tenants       []TenantWeight    `json:"tenants"`
	UniqueNoisy   bool              `json:"unique_noisy"`
	URLs          map[string]string `json:"urls"`
	Seconds       float64           `json:"seconds"`
	StartRPS      float64           `json:"start_rps"`
	EndRPS        float64           `json:"end_rps"`
	BusinessSlots int               `json:"business_slots"`
	RunID         string            `json:"run_id"`
}
type Row struct {
	Role, Priority        string
	Tenant, Reason        string
	Code                  int
	Latency, Lag, Arrival float64
	Limit                 int
}
type Traffic struct {
	RejectionReasons map[string]int `json:"rejection_reasons"`
	Statuses         map[string]int `json:"statuses"`
	Sent             int            `json:"sent"`
	Successful       int            `json:"successful"`
	Rejected         int            `json:"rejected_503"`
	Errors           int            `json:"backend_5xx"`
	Transport        int            `json:"transport_errors"`
	Other            int            `json:"other_statuses"`
	P95              *float64       `json:"successful_p95_ms"`
	Timely           float64        `json:"successful_within_100ms_per_offered_second"`
	Dispatch         *float64       `json:"dispatch_p95_ms"`
	Arrival          *float64       `json:"arrival_p95_ms"`
}

func percentile(v []float64) *float64 {
	if len(v) == 0 {
		return nil
	}
	sort.Float64s(v)
	x := v[int(math.Ceil(float64(len(v))*.95))-1]
	return &x
}
func summarize(rows []Row, seconds float64) Traffic {
	t := Traffic{RejectionReasons: map[string]int{}, Statuses: map[string]int{}}
	var lat, lag, arrival []float64
	for _, r := range rows {
		t.Sent++
		t.Statuses[strconv.Itoa(r.Code)]++
		if r.Code == 503 {
			t.RejectionReasons[r.Reason]++
		}
		lag = append(lag, r.Lag)
		arrival = append(arrival, r.Arrival)
		switch {
		case r.Code >= 200 && r.Code < 300:
			t.Successful++
			lat = append(lat, r.Latency)
			if r.Latency <= 100 {
				t.Timely += 1 / seconds
			}
		case r.Code == 503:
			t.Rejected++
		case r.Code >= 500:
			t.Errors++
		case r.Code == 0:
			t.Transport++
		default:
			t.Other++
		}
	}
	t.P95 = percentile(lat)
	t.Dispatch = percentile(lag)
	t.Arrival = percentile(arrival)
	return t
}
func main() {
	var c Config
	if e := json.NewDecoder(os.Stdin).Decode(&c); e != nil {
		panic(e)
	}
	if c.Seconds <= 0 || c.StartRPS <= 0 || c.EndRPS <= 0 || c.BusinessSlots < 0 || c.BusinessSlots > 5 {
		panic("invalid schedule")
	}
	for _, r := range []string{"orders", "catalog", "inventory"} {
		if c.URLs[r] == "" {
			panic("missing URL")
		}
	}
	weightTotal := 0
	for _, tenant := range c.Tenants {
		if tenant.Weight <= 0 {
			panic("invalid tenant weight")
		}
		weightTotal += tenant.Weight
	}
	stride := 1
	if weightTotal > 0 {
		stride = int(float64(weightTotal)*.618) + 1
		for gcd(stride, weightTotal) != 1 {
			stride++
		}
	}
	transport := &http.Transport{MaxIdleConns: 4096, MaxIdleConnsPerHost: 2048, MaxConnsPerHost: 4096, IdleConnTimeout: 30 * time.Second, DialContext: (&net.Dialer{Timeout: time.Second}).DialContext}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
	count := int(math.Round(c.Seconds * (c.StartRPS + c.EndRPS) / 2))
	rows := make([]Row, 0, count)
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, 4096)
	dropped := 0
	start := time.Now()
	slope := (c.EndRPS - c.StartRPS) / c.Seconds
	roles := []string{"orders", "catalog", "orders", "inventory", "orders"}
	for i := 0; i < count; i++ {
		dueSeconds := float64(i) / c.StartRPS
		if math.Abs(slope) > 1e-9 {
			dueSeconds = 2 * float64(i) / (c.StartRPS + math.Sqrt(c.StartRPS*c.StartRPS+2*slope*float64(i)))
		}
		due := start.Add(time.Duration(dueSeconds * float64(time.Second)))
		if d := time.Until(due); d > 0 {
			time.Sleep(d)
		}
		select {
		case slots <- struct{}{}:
		default:
			dropped++
			continue
		}
		wg.Add(1)
		go func(i int, due time.Time) {
			defer wg.Done()
			defer func() { <-slots }()
			role := roles[(i/5)%5]
			tenantID, tenantGroup := "", ""
			if weightTotal > 0 {
				slot := (i * stride) % weightTotal
				for _, tenant := range c.Tenants {
					if slot < tenant.Weight {
						tenantID = tenant.ID
						break
					}
					slot -= tenant.Weight
				}
				tenantGroup = tenantID
				if c.UniqueNoisy && tenantID == "noisy" {
					tenantID = fmt.Sprintf("churn-%s-%d", c.RunID, i)
					tenantGroup = "churn"
				}
			}
			priority := "background"
			method := "POST"
			path := "/" + role + "/reports/rebuild"
			body := []byte("{}")
			if i%5 < c.BusinessSlots {
				priority = "critical"
				switch role {
				case "orders":
					path = "/orders/checkout"
					body = []byte(`{"amount_cents":1000}`)
				case "catalog":
					method = "GET"
					path = "/catalog/products?limit=10"
					body = nil
				case "inventory":
					path = "/inventory/reservations"
					body = []byte(fmt.Sprintf(`{"product_id":%d,"quantity":1}`, 1+i%997))
				}
			}
			req, e := http.NewRequest(method, c.URLs[role]+path, bytes.NewReader(body))
			if e != nil {
				panic(e)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Idempotency-Key", fmt.Sprintf("%s-%d", c.RunID, i))
			if tenantID != "" {
				req.Header.Set("X-Demo-Tenant", tenantID)
			}
			sent := time.Now()
			code, limit := 0, 0
			reason := ""
			resp, e := client.Do(req)
			if e == nil {
				if resp.StatusCode == 503 {
					body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
					e = err
					var rejection struct {
						Reason string `json:"reason"`
					}
					_ = json.Unmarshal(body, &rejection)
					switch rejection.Reason {
					case "tenant_concurrency_limit", "tenant_registry_full", "concurrency_limit", "critical_capacity_reserved":
						reason = rejection.Reason
					default:
						reason = "other_503"
					}
				} else {
					_, e = io.Copy(io.Discard, resp.Body)
				}
				resp.Body.Close()
				if e == nil {
					code = resp.StatusCode
				}
				limit, _ = strconv.Atoi(resp.Header.Get("X-Gatewise-Limit"))
			}
			end := time.Now()
			r := Row{Role: role, Priority: priority, Tenant: tenantGroup, Reason: reason, Code: code, Latency: float64(end.Sub(sent)) / 1e6, Lag: float64(sent.Sub(due)) / 1e6, Arrival: float64(end.Sub(due)) / 1e6, Limit: limit}
			mu.Lock()
			rows = append(rows, r)
			mu.Unlock()
		}(i, due)
	}
	if d := time.Until(start.Add(time.Duration(c.Seconds * float64(time.Second)))); d > 0 {
		time.Sleep(d)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	traffic := map[string]Traffic{}
	services := map[string]map[string]Traffic{}
	min, max := 0, 0
	for _, priority := range []string{"critical", "background"} {
		var subset []Row
		for _, r := range rows {
			if r.Priority == priority {
				subset = append(subset, r)
			}
		}
		traffic[priority] = summarize(subset, c.Seconds)
	}
	for _, role := range []string{"orders", "catalog", "inventory"} {
		services[role] = map[string]Traffic{}
		for _, priority := range []string{"critical", "background"} {
			var subset []Row
			for _, r := range rows {
				if r.Role == role && r.Priority == priority {
					subset = append(subset, r)
				}
			}
			services[role][priority] = summarize(subset, c.Seconds)
		}
	}
	for _, r := range rows {
		if r.Limit > 0 {
			if min == 0 || r.Limit < min {
				min = r.Limit
			}
			if r.Limit > max {
				max = r.Limit
			}
		}
	}
	tenantTraffic := map[string]Traffic{}
	tenantServices := map[string]map[string]Traffic{}
	groups := map[string][]Row{}
	for _, r := range rows {
		if r.Tenant != "" {
			groups[r.Tenant] = append(groups[r.Tenant], r)
		}
	}
	for group, subset := range groups {
		tenantTraffic[group] = summarize(subset, c.Seconds)
		tenantServices[group] = map[string]Traffic{}
		for _, role := range []string{"orders", "catalog", "inventory"} {
			var selected []Row
			for _, r := range subset {
				if r.Role == role {
					selected = append(selected, r)
				}
			}
			tenantServices[group][role] = summarize(selected, c.Seconds)
		}
	}
	out := map[string]any{"tenant_traffic": tenantTraffic, "tenant_services": tenantServices, "scheduled": count, "sent": len(rows), "dropped_schedules": dropped, "offered_seconds": c.Seconds, "elapsed_including_drain_seconds": elapsed, "start_rps": c.StartRPS, "end_rps": c.EndRPS, "traffic": traffic, "services": services, "observed_min_limit": min, "observed_max_limit": max}
	if e := json.NewEncoder(os.Stdout).Encode(out); e != nil {
		panic(e)
	}
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}
