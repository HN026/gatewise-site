package proxy_test

import (
	"context"
	"encoding/json"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/config"
	gatewiseproxy "github.com/HN026/gatewise/internal/proxy"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestPriorityAdmission(t *testing.T) {
	entered := make(chan struct{}, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/hold" {
			entered <- struct{}{}
			<-r.Context().Done()
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	defer upstream.Close()
	target, _ := url.Parse(upstream.URL)
	h, cleanup := gatewiseproxy.New(config.Config{
		Services: map[string]config.Service{"users": {Upstream: target, MaxConcurrent: 2, ReservedCritical: 1}, "catalog": {Upstream: target, MaxConcurrent: 1}},
		Routes: []config.Route{
			{Prefix: "/users", Service: "users", StripPrefix: true, Priority: admission.Critical},
			{Prefix: "/users/background", Service: "users", StripPrefix: true, Priority: admission.Background},
			{Prefix: "/alias", Service: "users", StripPrefix: true, Priority: admission.Background},
			{Prefix: "/catalog", Service: "catalog", StripPrefix: true},
		}, DialTimeout: time.Second, ResponseHeaderTimeout: 5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cleanup()
	proxy := httptest.NewServer(h)
	defer proxy.Close()
	client := &http.Client{Timeout: 3 * time.Second}
	var cancels []context.CancelFunc
	defer func() {
		for _, cancel := range cancels {
			cancel()
		}
	}()
	hold := func(path string) context.CancelFunc {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		cancels = append(cancels, cancel)
		req, _ := http.NewRequestWithContext(ctx, "GET", proxy.URL+path, nil)
		go func() {
			res, _ := client.Do(req)
			if res != nil {
				res.Body.Close()
			}
		}()
		select {
		case <-entered:
		case <-time.After(time.Second):
			cancel()
			t.Fatal("held request did not reach backend")
		}
		return cancel
	}
	check := func(path string, want int, wantReason string) {
		t.Helper()
		req, _ := http.NewRequest("GET", proxy.URL+path, nil)
		req.Header.Set("X-Priority", "critical") // A caller cannot elevate a configured background route.
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		if res.StatusCode != want {
			t.Fatalf("%s: got %d want %d", path, res.StatusCode, want)
		}
		if wantReason != "" {
			var body map[string]string
			if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["reason"] != wantReason || body["service"] != "users" {
				t.Fatalf("unexpected rejection: %v", body)
			}
			if path == "/alias/ok" && body["priority"] != "background" {
				t.Fatal("client header elevated priority")
			}
		}
	}
	cancelBackground := hold("/users/background/hold")
	check("/users/background/ok", 503, "critical_capacity_reserved")
	check("/alias/ok", 503, "critical_capacity_reserved")
	check("/users/ok", 200, "")
	cancelCritical := hold("/users/hold")
	check("/users/ok", 503, "concurrency_limit")
	check("/users/background/ok", 503, "concurrency_limit")
	check("/catalog/ok", 200, "")
	check("/healthz", 200, "")
	cancelCritical()
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
		if time.Now().After(deadline) {
			t.Fatal("critical cancellation leaked capacity")
		}
		time.Sleep(time.Millisecond)
	}
	check("/users/background/ok", 503, "critical_capacity_reserved")
	cancelBackground()
	deadline = time.Now().Add(2 * time.Second)
	for {
		res, err := client.Get(proxy.URL + "/users/background/ok")
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background cancellation leaked capacity")
		}
		time.Sleep(time.Millisecond)
	}
}
