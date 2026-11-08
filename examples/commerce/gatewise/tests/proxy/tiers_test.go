package proxy_test

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/internal/proxy"
	"github.com/HN026/gatewise/priority"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestProxyPriorityTrustAndPropagation(t *testing.T) {
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(r.Header.Get(priority.Header))) }))
	defer up.Close()
	target, _ := url.Parse(up.URL)
	c := config.Config{Services: map[string]config.Service{"app": {Upstream: target, MaxConcurrent: 4, Priorities: &priority.Config{Tiers: []priority.Tier{{Name: "urgent", Reserved: 1}, {Name: "batch"}}, TrustedProxies: []string{"127.0.0.1/32"}}}}, Routes: []config.Route{{Prefix: "/", Service: "app", Priority: admission.Priority("batch")}}}
	h, close := proxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer close()
	for _, peer := range []string{"127.0.0.1:9", "192.0.2.1:9"} {
		r := httptest.NewRequest("GET", "http://proxy/", nil)
		r.RemoteAddr = peer
		r.Header.Set(priority.Header, "v1;urgent;-")
		r.Header.Set("X-Forwarded-For", "127.0.0.1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if strings.HasPrefix(peer, "127.") {
			if w.Code != 200 || w.Body.String() != "v1;urgent;-" {
				t.Fatal(w.Code, w.Body.String())
			}
		} else if w.Code != 403 {
			t.Fatal(w.Code)
		}
	}
}
