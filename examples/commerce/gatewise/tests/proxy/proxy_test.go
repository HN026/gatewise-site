package proxy_test

import (
	gatewiseproxy "github.com/HN026/gatewise/internal/proxy"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/HN026/gatewise/internal/config"
)

func startProxy(t *testing.T, upstream string, timeout time.Duration) *httptest.Server {
	t.Helper()
	u, err := url.Parse(upstream)
	if err != nil {
		t.Fatal(err)
	}
	h, closeIdle := gatewiseproxy.New(config.Config{Services: map[string]config.Service{"test": {Upstream: u, MaxConcurrent: 100}}, Routes: []config.Route{{Prefix: "/", Service: "test"}}, DialTimeout: time.Second, ResponseHeaderTimeout: timeout}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := httptest.NewServer(h)
	t.Cleanup(func() { s.Close(); closeIdle() })
	return s
}

func TestProxy(t *testing.T) {
	t.Run("forwarding", func(t *testing.T) {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, _ := io.ReadAll(r.Body)
			if r.Method != "POST" || r.URL.RequestURI() != "/echo?tag=a&tag=b" || string(body) != "hello" || r.Header.Get("X-Example") != "preserved" {
				t.Errorf("request changed: %s %s %q", r.Method, r.URL.RequestURI(), body)
			}
			if r.Header.Get("X-Forwarded-For") == "spoofed" || r.Header.Get("Forwarded") != "" || r.Header.Get("X-Remove") != "" {
				t.Error("untrusted/hop-by-hop headers forwarded")
			}
			w.Header().Set("X-Upstream", "preserved")
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write(body)
		}))
		defer up.Close()
		s := startProxy(t, up.URL, time.Second)
		req, _ := http.NewRequest("POST", s.URL+"/echo?tag=a&tag=b", strings.NewReader("hello"))
		req.Header.Set("X-Example", "preserved")
		req.Header.Set("X-Forwarded-For", "spoofed")
		req.Header.Set("Forwarded", "for=spoofed")
		req.Header.Set("Connection", "X-Remove")
		req.Header.Set("X-Remove", "secret")
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 201 || string(body) != "hello" || res.Header.Get("X-Upstream") != "preserved" {
			t.Fatalf("unexpected response: %d %q", res.StatusCode, body)
		}
	})
	t.Run("unavailable backend and independent health", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		addr := ln.Addr().String()
		_ = ln.Close()
		s := startProxy(t, "http://"+addr, time.Second)
		for path, want := range map[string]int{"/hello": 502, "/healthz": 200} {
			res, err := http.Get(s.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := io.ReadAll(res.Body)
			res.Body.Close()
			if res.StatusCode != want {
				t.Fatalf("%s: got %d: %s", path, res.StatusCode, body)
			}
		}
	})
	t.Run("header timeout", func(t *testing.T) {
		up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
		defer up.Close()
		s := startProxy(t, up.URL, 30*time.Millisecond)
		res, err := http.Get(s.URL + "/slow")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		body, _ := io.ReadAll(res.Body)
		if res.StatusCode != 504 || !strings.Contains(string(body), "upstream_timeout") {
			t.Fatalf("got %d: %s", res.StatusCode, body)
		}
	})
}
