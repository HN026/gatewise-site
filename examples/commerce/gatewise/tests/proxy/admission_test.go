package proxy_test

import (
	"context"
	gatewiseproxy "github.com/HN026/gatewise/internal/proxy"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/HN026/gatewise/internal/config"
)

// One real HTTP scenario covers shared limits, independent services, cancellation
// release, error release, and health bypass. Channels coordinate actual admission.
func TestServiceAdmission(t *testing.T) {
	entered := make(chan struct{}, 4)
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/hold" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	c := config.Config{Services: map[string]config.Service{
		"users":   {Upstream: target, MaxConcurrent: 1},
		"catalog": {Upstream: target, MaxConcurrent: 1},
	}, Routes: []config.Route{
		{Prefix: "/users", Service: "users", StripPrefix: true},
		{Prefix: "/alias", Service: "users", StripPrefix: true},
		{Prefix: "/catalog", Service: "catalog", StripPrefix: true},
	}, DialTimeout: time.Second, ResponseHeaderTimeout: time.Second}
	h, closeIdle := gatewiseproxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer closeIdle()
	proxy := httptest.NewServer(h)
	defer proxy.Close()
	client := &http.Client{Timeout: 2 * time.Second}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", proxy.URL+"/users/hold", nil)
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		res, _ := client.Do(request)
		if res != nil {
			res.Body.Close()
		}
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not start")
	}
	for _, path := range []string{"/users/ok", "/alias/ok"} {
		start := time.Now()
		res, err := client.Get(proxy.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 503 || !strings.Contains(string(body), "service_overloaded") || time.Since(start) > 500*time.Millisecond {
			t.Fatalf("expected prompt 503, got %d %s", res.StatusCode, body)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("rejected work reached backend")
	}
	for _, path := range []string{"/catalog/ok", "/healthz", "/metrics"} {
		res, err := client.Get(proxy.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("%s got %d", path, res.StatusCode)
		}
	}
	cancel()
	select {
	case <-finished:
	case <-time.After(2 * time.Second):
		t.Fatal("client cancellation stuck")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		res, err := client.Get(proxy.URL + "/users/ok")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 200 {
			break
		}
		if res.StatusCode != 503 || time.Now().After(deadline) {
			t.Fatal("canceled work leaked capacity")
		}
		time.Sleep(time.Millisecond)
	}

	// Repeated failed connections must also release the only slot.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable, _ := url.Parse("http://" + ln.Addr().String())
	ln.Close()
	c.Services["users"] = config.Service{Upstream: unavailable, MaxConcurrent: 1}
	failed, cleanup := gatewiseproxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cleanup()
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		failed.ServeHTTP(w, httptest.NewRequest("GET", "/users/hello", nil))
		if w.Code != 502 {
			t.Fatalf("error leaked capacity: got %d", w.Code)
		}
	}
	// A header timeout is another completed attempt, not a permanently occupied slot.
	c.Services["users"] = config.Service{Upstream: target, MaxConcurrent: 1}
	c.ResponseHeaderTimeout = 20 * time.Millisecond
	timed, cleanupTimed := gatewiseproxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cleanupTimed()
	for i := 0; i < 3; i++ {
		w := httptest.NewRecorder()
		timed.ServeHTTP(w, httptest.NewRequest("GET", "/users/hold", nil))
		if w.Code != 504 {
			t.Fatalf("timeout leaked capacity: got %d", w.Code)
		}
	}
	w := httptest.NewRecorder()
	timed.ServeHTTP(w, httptest.NewRequest("GET", "/users/ok", nil))
	if w.Code != 200 {
		t.Fatalf("no capacity after timeouts: %d", w.Code)
	}

}

// A truncated backend body makes the real reverse proxy panic with ErrAbortHandler.
// The following request proves the service's only admission slot was released.
func TestPanicReleasesSlot(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/panic" {
			conn, buffer, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			defer conn.Close()
			_, _ = buffer.WriteString("HTTP/1.1 200 OK\r\nContent-Length: 100\r\n\r\nshort")
			_ = buffer.Flush()
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	h, closeIdle := singleService(t, upstream.URL)
	defer closeIdle()
	request := httptest.NewRequest("GET", "/panic", nil)
	request = request.WithContext(context.WithValue(request.Context(), http.ServerContextKey, &http.Server{}))
	func() {
		defer func() {
			if got := recover(); got != http.ErrAbortHandler {
				t.Errorf("expected ErrAbortHandler, got %v", got)
			}
		}()
		h.ServeHTTP(httptest.NewRecorder(), request)
	}()
	result := httptest.NewRecorder()
	h.ServeHTTP(result, httptest.NewRequest("GET", "/ok", nil))
	if result.Code != 200 || result.Body.String() != "ok" {
		t.Fatalf("panic leaked capacity: %d %q", result.Code, result.Body.String())
	}
}

func TestSlotHeldUntilHandlerReturns(t *testing.T) {
	entered, finish, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int64
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		if calls.Add(1) == 1 {
			close(entered)
			<-finish
		}
		_, _ = io.WriteString(w, "complete body")
	}))
	defer upstream.Close()
	h, closeIdle := singleService(t, upstream.URL)
	defer closeIdle()
	go func() { defer close(done); h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
	<-entered
	rejected := httptest.NewRecorder()
	h.ServeHTTP(rejected, httptest.NewRequest("GET", "/", nil))
	if rejected.Code != 503 {
		t.Errorf("response headers released slot early: %d", rejected.Code)
	}
	close(finish)
	<-done
	accepted := httptest.NewRecorder()
	h.ServeHTTP(accepted, httptest.NewRequest("GET", "/", nil))
	if accepted.Code != 200 || accepted.Body.String() != "complete body" {
		t.Fatalf("body completion did not release slot: %d", accepted.Code)
	}
}

func singleService(t *testing.T, targetURL string) (http.Handler, func()) {
	t.Helper()
	target, err := url.Parse(targetURL)
	if err != nil {
		t.Fatal(err)
	}
	return gatewiseproxy.New(config.Config{
		Services:    map[string]config.Service{"test": {Upstream: target, MaxConcurrent: 1}},
		Routes:      []config.Route{{Prefix: "/", Service: "test"}},
		DialTimeout: time.Second, ResponseHeaderTimeout: time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}
