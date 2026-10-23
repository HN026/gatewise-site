package config_test

import (
	"encoding/json"
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestAdaptiveConfigAndOverrides(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	service := map[string]any{"upstream_url": "http://localhost:9090", "max_concurrent": 6, "reserved_critical": 1, "mode": "adaptive", "adaptive": map[string]any{"min_concurrent": 2, "initial_concurrent": 4, "interval": "500ms", "min_samples": 4, "target_latency": "100ms"}}
	raw := map[string]any{"services": map[string]any{"users": service}, "routes": []map[string]any{{"path_prefix": "/users", "service": "users", "latency_target": "1s"}}}
	load := func() (config.Config, error) {
		t.Helper()
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, b, 0600); err != nil {
			t.Fatal(err)
		}
		return config.Load(filename)
	}
	c, err := load()
	if err != nil || c.Services["users"].Adaptive == nil || c.Services["users"].Adaptive.InitialConcurrent != 4 || c.Routes[0].LatencyTarget.Seconds() != 1 {
		t.Fatalf("adaptive parse: %+v %v", c, err)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_ADAPTIVE_INITIAL_CONCURRENT", "5")
	c, err = load()
	if err != nil || c.Services["users"].Adaptive.InitialConcurrent != 5 {
		t.Fatal("adaptive environment override failed", err)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_ADAPTIVE_INITIAL_CONCURRENT", "1")
	if _, err = load(); err == nil {
		t.Fatal("initial limit below minimum accepted")
	}
	t.Setenv("GATEWISE_SERVICE_USERS_MODE", "fixed")
	c, err = load()
	if err != nil || c.Services["users"].Adaptive != nil {
		t.Fatal("fixed comparison mode failed", err)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_MODE", "unknown")
	if _, err = load(); err == nil {
		t.Fatal("invalid mode accepted")
	}
}
