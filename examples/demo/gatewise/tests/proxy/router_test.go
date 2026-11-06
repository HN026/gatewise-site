package proxy_test

import (
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

func TestRouting(t *testing.T) {
	backend := func(name string) *httptest.Server {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = io.WriteString(w, name+" "+r.URL.RequestURI()) }))
		t.Cleanup(s.Close)
		return s
	}
	users, catalog := backend("users"), backend("catalog")
	u, _ := url.Parse(users.URL)
	c, _ := url.Parse(catalog.URL + "/api")
	cfg := config.Config{Services: map[string]config.Service{"users": {Upstream: u, MaxConcurrent: 100}, "catalog": {Upstream: c, MaxConcurrent: 100}}, Routes: []config.Route{
		{Prefix: "/users", Service: "users", StripPrefix: true},
		{Prefix: "/catalog", Service: "catalog", StripPrefix: true},
		{Prefix: "/users/special", Service: "catalog", StripPrefix: true},
		{Prefix: "/preserved", Service: "users"},
	}, DialTimeout: time.Second, ResponseHeaderTimeout: time.Second}
	h, closeIdle := gatewiseproxy.New(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)))
	s := httptest.NewServer(h)
	defer s.Close()
	defer closeIdle()
	for _, tc := range []struct {
		path, body string
		status     int
	}{
		{"/users/hello", "users /hello", 200},
		{"/catalog/products?tag=a&tag=b", "catalog /api/products?tag=a&tag=b", 200},
		{"/users/special/hello", "catalog /api/hello", 200},
		{"/preserved/hello", "users /preserved/hello", 200},
		{"/users/a%2Fb?x=%2F", "users /a%2Fb?x=%2F", 200},
		{"/users", "users /", 200},
		{"/users-other/hello", "{\"error\":\"route_not_found\"}\n", 404},
		{"/missing", "{\"error\":\"route_not_found\"}\n", 404},
	} {
		t.Run(tc.path, func(t *testing.T) {
			res, err := http.Get(s.URL + tc.path)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(res.Body)
			res.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tc.status || string(body) != tc.body {
				t.Fatalf("got %d %q; want %d %q", res.StatusCode, body, tc.status, tc.body)
			}
		})
	}
}
