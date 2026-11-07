package benchmark

import (
	"context"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"sync"
	"time"
)

type sample struct {
	priority              string
	status, limit         int
	latency, arrival, lag float64
	err                   bool
}

// Summarize uses nearest-rank percentiles, in milliseconds.
func Summarize(values []float64) Latency {
	if len(values) == 0 {
		return Latency{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, v := range sorted {
		sum += v
	}
	percentile := func(p float64) float64 { return sorted[int(math.Ceil(p*float64(len(sorted))))-1] }
	return Latency{len(sorted), sum / float64(len(sorted)), percentile(.5), percentile(.95), percentile(.99)}
}

// load uses scheduled arrivals rather than a closed loop: slow responses do not
// lower offered RPS. Late schedules and a full generator are explicitly dropped.
func load(ctx context.Context, client *http.Client, base string, rate int, duration time.Duration, maxInFlight int) (map[string]Traffic, float64) {
	start := time.Now()
	total := int(duration.Seconds() * float64(rate))
	scheduled, dropped := map[string]int{}, map[string]int{}
	samples := make([]sample, 0, total)
	var mu sync.Mutex
	var wg sync.WaitGroup
	slots := make(chan struct{}, maxInFlight)
	interval := time.Second / time.Duration(rate)
	timer := time.NewTimer(0)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	for i := 0; i < total; i++ {
		if ctx.Err() != nil {
			break
		}
		priority := "background"
		if i%8 == 0 {
			priority = "critical"
		}
		scheduled[priority]++
		due := start.Add(time.Duration(i) * interval)
		if wait := time.Until(due); wait > 0 {
			timer.Reset(wait)
			select {
			case <-ctx.Done():
				dropped[priority]++
				continue
			case <-timer.C:
			}
		}
		// Do not issue a catch-up burst when the generator falls behind.
		if time.Since(due) > 5*time.Millisecond {
			dropped[priority]++
			continue
		}
		select {
		case slots <- struct{}{}:
		default:
			dropped[priority]++
			continue
		}
		wg.Add(1)
		go func(priority string, due time.Time) {
			defer wg.Done()
			defer func() { <-slots }()
			dispatch := time.Now()
			req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+priority+"/work", nil)
			resp, err := client.Do(req)
			s := sample{priority: priority, err: err != nil, lag: float64(dispatch.Sub(due)) / float64(time.Millisecond)}
			if err == nil {
				s.status = resp.StatusCode
				s.limit, _ = strconv.Atoi(resp.Header.Get("X-Gatewise-Limit"))
				_, readErr := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				s.err = readErr != nil
			}
			s.latency = float64(time.Since(dispatch)) / float64(time.Millisecond)
			s.arrival = float64(time.Since(due)) / float64(time.Millisecond)
			mu.Lock()
			samples = append(samples, s)
			mu.Unlock()
		}(priority, due)
	}
	wg.Wait()
	elapsed := time.Since(start).Seconds()
	result := map[string]Traffic{}
	for _, priority := range []string{"critical", "background"} {
		t := Traffic{Scheduled: scheduled[priority], Dropped: dropped[priority]}
		var success, rejected, arrivals, lags []float64
		for _, s := range samples {
			if s.priority != priority {
				continue
			}
			t.Sent++
			arrivals = append(arrivals, s.arrival)
			lags = append(lags, s.lag)
			if s.limit > 0 {
				if t.LimitMin == 0 || s.limit < t.LimitMin {
					t.LimitMin = s.limit
				}
				if s.limit > t.LimitMax {
					t.LimitMax = s.limit
				}
			}
			switch {
			case s.err:
				t.Errors++
			case s.status >= 200 && s.status < 300:
				t.OK++
				success = append(success, s.latency)
				if s.latency > 100 {
					t.DeadlineMisses++
				}
			case s.status == 503:
				t.Rejected++
				rejected = append(rejected, s.latency)
			default:
				t.Errors++
			}
		}
		t.SuccessLatency = Summarize(success)
		t.RejectionLatency = Summarize(rejected)
		t.ArrivalLatency = Summarize(arrivals)
		t.DispatchLag = Summarize(lags)
		t.Goodput = float64(t.OK) / elapsed
		t.WithinBudgetGoodput = float64(t.OK-t.DeadlineMisses) / elapsed
		t.OfferedGoodput = float64(t.OK) / duration.Seconds()
		result[priority] = t
	}
	return result, elapsed
}
