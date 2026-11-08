package decision_test

import (
	"encoding/json"
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/internal/proxy"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestProxyConfigOfflineAndRecovery(t *testing.T) {
	var pressure atomic.Bool
	var sourceCalls, upstreamCalls atomic.Int32
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceCalls.Add(1)
		v := .1
		if pressure.Load() {
			v = .9
		}
		_ = json.NewEncoder(w).Encode(decision.Observation{State: "measured", ObservedAt: time.Now(), Value: v})
	}))
	defer source.Close()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { upstreamCalls.Add(1); w.WriteHeader(204) }))
	defer upstream.Close()
	raw := map[string]any{"services": map[string]any{"orders": map[string]any{"upstream_url": upstream.URL, "max_concurrent": 4, "decisions": map[string]any{"mode": "enforce", "signals": []any{map[string]any{"name": "pool", "scope": map[string]string{"kind": "resource", "key": "checkout"}, "threshold": .8, "max_age": "100ms", "on_unknown": "reject", "pressure": map[string]bool{"reject": true}, "source": map[string]string{"url": source.URL, "interval": "10ms", "timeout": "5ms"}}}}}}, "routes": []any{map[string]string{"path_prefix": "/checkout", "service": "orders", "resource": "checkout"}, map[string]string{"path_prefix": "/read", "service": "orders"}}}
	file := filepath.Join(t.TempDir(), "config.json")
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(file, data, 0600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	if sourceCalls.Load() != 0 {
		t.Fatal("config validation called source")
	}
	h, closeIdle := proxy.New(c, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer closeIdle()
	s := httptest.NewServer(h)
	defer s.Close()
	check := func(path string, want int) {
		t.Helper()
		deadline := time.Now().Add(time.Second)
		for {
			res, err := http.Get(s.URL + path)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.Copy(io.Discard, res.Body)
			res.Body.Close()
			if res.StatusCode == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s got %d want %d", path, res.StatusCode, want)
			}
			time.Sleep(time.Millisecond)
		}
	}
	check("/checkout", 204)
	pressure.Store(true)
	check("/checkout", 503)
	before := upstreamCalls.Load()
	check("/checkout", 503)
	if upstreamCalls.Load() != before {
		t.Fatal("rejection forwarded")
	}
	check("/read", 204)
	check("/metrics", 200)
	check("/healthz", 200)
	pressure.Store(false)
	check("/checkout", 204)
}
