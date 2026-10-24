package proxy_test

import (
	"context"
	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/internal/proxy"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProxyTenantIsolationAndPeerTrust(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Tenant") == "a" {
			close(entered)
			<-release
		}
		w.WriteHeader(204)
	}))
	defer up.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	body := `{"services":{"orders":{"upstream_url":"` + up.URL + `","max_concurrent":4,"tenants":{"max_concurrent":1,"header":"X-Tenant","trusted_proxies":["127.0.0.1/32"]}}},"routes":[{"path_prefix":"/orders","service":"orders"}]}`
	if e := os.WriteFile(path, []byte(body), 0600); e != nil {
		t.Fatal(e)
	}
	c, e := config.Load(path)
	if e != nil {
		t.Fatal(e)
	}
	handler, cleanup := proxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer cleanup()
	forged := httptest.NewRequest("GET", "/orders", nil)
	forged.RemoteAddr = "192.0.2.1:123"
	forged.Header.Set("X-Tenant", "a")
	forged.Header.Set("X-Forwarded-For", "127.0.0.1")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, forged)
	if rr.Code != 403 {
		t.Fatal("forwarded IP bypassed trust", rr.Code)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	done := make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer close(done)
		r, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/orders", nil)
		r.Header.Set("X-Tenant", "a")
		response, err := server.Client().Do(r)
		if err == nil {
			response.Body.Close()
		}
	}()
	<-entered
	for _, tc := range []struct {
		id     string
		status int
	}{{"a", 503}, {"b", 204}} {
		r, _ := http.NewRequest("GET", server.URL+"/orders", nil)
		r.Header.Set("X-Tenant", tc.id)
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		response.Body.Close()
		if response.StatusCode != tc.status {
			t.Fatal(response.StatusCode)
		}
		if tc.status == 503 && !strings.Contains(string(data), "tenant_concurrency_limit") {
			t.Fatal(string(data))
		}
	}
	close(release)
	<-done
}
