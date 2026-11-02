package middleware_test

import (
	"context"
	"errors"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPQueueTimeoutCancellationAndMetrics(t *testing.T) {
	guard, err := middleware.NewService(middleware.ServiceConfig{Name: "queue", MaxConcurrent: 1, Queue: &middleware.QueueConfig{MaxWaiting: 2, MaxWait: 80 * time.Millisecond}})
	if err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	handler := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { entered <- struct{}{}; <-release; w.WriteHeader(204) }))
	server := httptest.NewServer(handler)
	defer server.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	first := make(chan error, 1)
	go func() {
		res, err := http.Get(server.URL)
		if res != nil {
			res.Body.Close()
		}
		first <- err
	}()
	<-entered
	res, err := http.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 503 {
		t.Fatal(res.StatusCode)
	}
	stream, _ := http.NewRequest("POST", server.URL, strings.NewReader("stream"))
	stream.ContentLength = -1
	streamed, err := http.DefaultClient.Do(stream)
	if err != nil {
		t.Fatal(err)
	}
	streamed.Body.Close()
	if streamed.StatusCode != 503 || streamed.Header.Get("X-Gatewise-Queue-Eligible") != "false" {
		t.Fatal("streaming request waited")
	}
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, "POST", server.URL, strings.NewReader(`{"test":true}`))
	canceled := make(chan error, 1)
	go func() { _, err := http.DefaultClient.Do(req); canceled <- err }()
	until := time.Now().Add(time.Second)
	for guard.QueueSnapshot().Waiting != 1 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if !errors.Is(<-canceled, context.Canceled) {
		t.Fatal("client cancellation")
	}
	until = time.Now().Add(time.Second)
	for guard.QueueSnapshot().Waiting != 0 && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if guard.Snapshot().Active != 1 {
		t.Fatal("executing request released")
	}
	close(release)
	if err = <-first; err != nil {
		t.Fatal(err)
	}
	registry := prometheus.NewRegistry()
	if err = metrics.Register(registry, guard); err != nil {
		t.Fatal(err)
	}
	out := httptest.NewRecorder()
	promhttp.HandlerFor(registry, promhttp.HandlerOpts{}).ServeHTTP(out, httptest.NewRequest("GET", "/metrics", nil))
	text := out.Body.String()
	for _, want := range []string{"gatewise_queue_capacity{service=\"queue\"} 2", "gatewise_queue_events_total{event=\"timeout\",priority=\"critical\",service=\"queue\"} 1", "gatewise_queue_events_total{event=\"canceled\",priority=\"critical\",service=\"queue\"} 1", "gatewise_queue_wait_duration_seconds_count"} {
		if !strings.Contains(text, want) {
			t.Fatal("missing metric", want, text)
		}
	}
}
