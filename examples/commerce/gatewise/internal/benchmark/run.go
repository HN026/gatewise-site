// Package benchmark implements bounded local M6 experiments. It is deliberately
// separate from request handling and is not imported by the Gatewise server.
package benchmark

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

func Run(ctx context.Context, c Config) error {
	if c.Repeats < 1 || c.Repeats > 10 || c.Duration < 2*time.Second || c.Duration > time.Minute || c.BaselineRate < 1 || c.PressureRate < 1 || c.BaselineRate > 5000 || c.PressureRate > 5000 || c.MaxInFlight < 1 || c.MaxInFlight > 4096 || c.MaxDropPercent < 0 || c.MaxDropPercent > 10 {
		return fmt.Errorf("invalid experiment bounds: repeats 1..10, phase 2s..1m, rates 1..5000, in-flight 1..4096, dropped schedule percent 0..10")
	}
	for _, p := range []*string{&c.Backend, &c.Proxy, &c.Output} {
		abs, err := filepath.Abs(*p)
		if err != nil {
			return err
		}
		*p = abs
	}
	for _, binary := range []string{c.Backend, c.Proxy} {
		info, err := os.Stat(binary)
		if err != nil {
			return err
		}
		if info.IsDir() || info.Mode()&0111 == 0 {
			return fmt.Errorf("%s must be an executable binary", binary)
		}
	}
	if err := os.MkdirAll(filepath.Dir(c.Output), 0755); err != nil {
		return err
	}
	if err := os.Mkdir(c.Output, 0755); err != nil {
		return fmt.Errorf("use a new output directory: %w", err)
	}
	host, _ := os.Hostname()
	r := Report{Started: time.Now().UTC(), GoVersion: runtime.Version(), OS: runtime.GOOS, Arch: runtime.GOARCH, CPUs: runtime.NumCPU(), Hostname: host, Config: c, Passed: true, Methodology: []string{
		"Loopback synthetic service: base 20ms, +50ms per active request above capacity, capped at 400ms. Delay is chosen at admission. This does not model all production bottlenecks.",
		"All guards: max/initial 6, reservation 1. Adaptive: min 2, 500ms interval, 10 samples, 100ms target. Backend capacity changes 12 -> 2 -> 12.",
		"Scheduled open-loop arrivals, one critical for every seven background requests, no retries. Warm-up 1s excluded. Runs isolated and sequential; deployment order rotates each repeat.",
		"Generator drops dispatches >5ms late or above in-flight ceiling; late/dropped schedules must be checked before capacity claims. 2s client timeout, keep-alive enabled, redirects disabled.",
		"Success and 503 latency are separate. Nearest-rank percentiles. Successful/second uses actual elapsed time including drain; successful/offered-second uses configured arrival duration. Arrival latency includes dispatch lag and all HTTP outcomes; timeouts are reported as errors.",
		"Child CPU is OS user+system time over entire child lifetime, including startup/warm-up/control/scrapes/logging. RSS sampled with ps every 250ms; observed peak is a lower bound. Resource CSV includes generator RSS. Go/proxy CPU is separated from backend CPU.",
		"All processes share this host; resource contention, client CPU and loopback networking affect results. No production capacity, multi-host scaling, leak or zero-overhead claims. Proxy uses normal access logging; middleware fixture disables optional library logging.",
	}}
	r.BinarySHA256 = map[string]string{}
	for _, binary := range []string{c.Backend, c.Proxy} {
		data, err := os.ReadFile(binary)
		if err != nil {
			return err
		}
		r.BinarySHA256[binary] = fmt.Sprintf("%x", sha256.Sum256(data))
	}
	deployments := []string{"direct", "proxy-fixed", "proxy-adaptive", "middleware-fixed", "middleware-adaptive"}
	for repeat := 1; repeat <= c.Repeats; repeat++ {
		for i := range deployments {
			name := deployments[(i+repeat-1)%len(deployments)]
			if err := ctx.Err(); err != nil {
				return err
			}
			fmt.Printf("[%d/%d] %s\n", repeat, c.Repeats, name)
			result, err := runDeployment(ctx, c, name, repeat)
			if err != nil {
				return fmt.Errorf("%s: %w (logs in %s)", name, err, c.Output)
			}
			r.Results = append(r.Results, result)
			for _, check := range result.Checks {
				if !check.Pass {
					r.Passed = false
					fmt.Println("  FAIL:", check.Name)
				}
			}
			r.Finished = time.Now().UTC()
			if err := writeReport(c.Output, r); err != nil {
				return err
			}
		}
	}
	fmt.Printf("Results: %s/results.json\n", c.Output)
	if !r.Passed {
		return fmt.Errorf("experiment checks failed; inspect results and generator delivery before interpreting comparisons")
	}
	fmt.Println("PASS: all experiment checks")
	return nil
}

func freeAddress() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := l.Addr().String()
	l.Close()
	return addr, nil
}

func runDeployment(ctx context.Context, c Config, name string, repeat int) (result Result, err error) {
	result = Result{Deployment: name, Repeat: repeat}
	dir := filepath.Join(c.Output, fmt.Sprintf("%02d-%s", repeat, name))
	if err = os.Mkdir(dir, 0755); err != nil {
		return
	}
	var processes []*process
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	var monitored <-chan struct{}
	defer func() {
		stopMonitor()
		if monitored != nil {
			<-monitored
		}
		for i := len(processes) - 1; i >= 0; i-- {
			result.Resources = append(result.Resources, processes[i].stop())
		}
	}()
	backendAddr, e := freeAddress()
	if e != nil {
		err = e
		return
	}
	backendURL := "http://" + backendAddr
	mode := "direct"
	if strings.HasPrefix(name, "middleware-") {
		mode = strings.TrimPrefix(name, "middleware-")
	}
	p, e := startProcess(ctx, dir, "backend", c.Backend, "-listen", backendAddr, "-mode", mode)
	if e != nil {
		err = e
		return
	}
	processes = append(processes, p)
	transport := &http.Transport{MaxIdleConns: c.MaxInFlight, MaxIdleConnsPerHost: c.MaxInFlight, MaxConnsPerHost: c.MaxInFlight, IdleConnTimeout: 10 * time.Second, ResponseHeaderTimeout: 2 * time.Second}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if err = waitReady(ctx, client, backendURL); err != nil {
		return
	}
	target := backendURL
	if strings.HasPrefix(name, "proxy-") {
		addr, e := freeAddress()
		if e != nil {
			err = e
			return
		}
		target = "http://" + addr
		service := map[string]any{"upstream_url": backendURL, "max_concurrent": 6, "reserved_critical": 1}
		if name == "proxy-adaptive" {
			service["mode"] = "adaptive"
			service["adaptive"] = map[string]any{"min_concurrent": 2, "initial_concurrent": 6, "interval": "500ms", "min_samples": 10, "target_latency": "100ms"}
		}
		cfg := map[string]any{"listen_addr": addr, "response_header_timeout": "2s", "services": map[string]any{"benchmark": service}, "routes": []any{
			map[string]any{"path_prefix": "/critical", "service": "benchmark", "priority": "critical"},
			map[string]any{"path_prefix": "/background", "service": "benchmark", "priority": "background"},
		}}
		data, _ := json.MarshalIndent(cfg, "", "  ")
		path := filepath.Join(dir, "proxy.json")
		if err = os.WriteFile(path, data, 0644); err != nil {
			return
		}
		p, e = startProcess(ctx, dir, "proxy", c.Proxy, "-config", path, "-env-file", "")
		if e != nil {
			err = e
			return
		}
		processes = append(processes, p)
		if err = waitReady(ctx, client, target); err != nil {
			return
		}
	}
	monitored = monitor(monitorCtx, processes, filepath.Join(dir, "resources.csv"))
	_, _ = load(ctx, client, target, c.BaselineRate, time.Second, c.MaxInFlight)
	for _, phase := range []struct {
		name           string
		capacity, rate int
	}{{"baseline", 12, c.BaselineRate}, {"pressure", 2, c.PressureRate}, {"recovery", 12, c.PressureRate}} {
		if err = control(ctx, client, backendURL, phase.capacity); err != nil {
			return
		}
		traffic, elapsed := load(ctx, client, target, phase.rate, c.Duration, c.MaxInFlight)
		if err = ctx.Err(); err != nil {
			return
		}
		item := Phase{Name: phase.name, Capacity: phase.capacity, Rate: phase.rate, OfferedSeconds: c.Duration.Seconds(), ElapsedSeconds: elapsed, Traffic: traffic}
		var state struct {
			Active int `json:"active"`
			Peak   int `json:"peak_active"`
		}
		data, e := get(ctx, client, backendURL+"/state")
		if e != nil {
			err = e
			return
		}
		if err = json.Unmarshal(data, &state); err != nil {
			return
		}
		if state.Active != 0 {
			err = fmt.Errorf("backend still has %d active requests after drain", state.Active)
			return
		}
		item.PeakBackendActive = state.Peak
		if name != "direct" {
			data, e = get(ctx, client, target+"/metrics")
			if e != nil {
				err = e
				return
			}
			if err = os.WriteFile(filepath.Join(dir, phase.name+".prom"), data, 0644); err != nil {
				return
			}
			item.EndLimit = metric(data, "gatewise_concurrency_limit", "")
			item.Decreases = metric(data, "gatewise_limit_adjustments_total", `direction="decrease"`)
			item.Increases = metric(data, "gatewise_limit_adjustments_total", `direction="increase"`)
		}
		result.Phases = append(result.Phases, item)
		fmt.Printf("  %-8s critical OK %4d reject %4d p95 %6.1fms | background OK %4d reject %4d | limit %.0f\n", item.Name, traffic["critical"].OK, traffic["critical"].Rejected, traffic["critical"].SuccessLatency.P95, traffic["background"].OK, traffic["background"].Rejected, item.EndLimit)
	}
	result.Checks = checks(result, c.MaxDropPercent)
	return
}

func control(ctx context.Context, client *http.Client, base string, capacity int) error {
	data, _ := json.Marshal(map[string]int{"capacity": capacity})
	r, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/control", bytes.NewReader(data))
	r.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(r)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 204 {
		return fmt.Errorf("control returned %d", resp.StatusCode)
	}
	return nil
}

func get(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	r, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	resp, err := client.Do(r)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("%s returned %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 1<<20))
}

func metric(data []byte, name, label string) float64 {
	total := 0.0
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, name+"{") || (label != "" && !strings.Contains(line, label)) {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) == 2 {
			v, _ := strconv.ParseFloat(parts[1], 64)
			total += v
		}
	}
	return total
}

func checks(r Result, maxDropPercent float64) []Check {
	accounting, delivery, healthy, bounds := true, true, true, true
	for _, p := range r.Phases {
		for _, t := range p.Traffic {
			accounting = accounting && t.Scheduled == t.Sent+t.Dropped && t.Sent == t.OK+t.Rejected+t.Errors
			delivery = delivery && t.Scheduled > 0 && 100*float64(t.Dropped)/float64(t.Scheduled) <= maxDropPercent
			healthy = healthy && t.Errors == 0 && t.OK > 0
			if r.Deployment != "direct" {
				bounds = bounds && t.LimitMin >= 2 && t.LimitMax <= 6
			}
		}
	}
	out := []Check{{"all scheduled and HTTP outcomes accounted for", accounting}, {fmt.Sprintf("generator dropped at most %.2f%% of schedules per priority/phase", maxDropPercent), delivery}, {"both priorities complete work without unexpected errors in every phase", healthy}}
	pressure := r.Phases[1]
	rejected := pressure.Traffic["critical"].Rejected + pressure.Traffic["background"].Rejected
	if r.Deployment == "direct" {
		out = append(out, Check{"unguarded baseline has no admission rejections", rejected == 0})
	} else {
		out = append(out, Check{"observed admission limits remain within configured bounds", bounds}, Check{"overload produces deliberate 503 shedding", rejected > 0}, Check{"backend work never exceeds maximum admission limit", r.Phases[2].PeakBackendActive <= 6})
		if strings.HasSuffix(r.Deployment, "adaptive") {
			out = append(out, Check{"adaptive limit decreases under pressure", pressure.Decreases > 0}, Check{"adaptive capacity grows during recovery", r.Phases[2].Increases > pressure.Increases && r.Phases[2].EndLimit > pressure.EndLimit})
		}
	}
	return out
}
