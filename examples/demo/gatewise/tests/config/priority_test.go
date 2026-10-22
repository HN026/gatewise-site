package config_test

import (
	"encoding/json"
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestPriorityConfiguration(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "config.json")
	load := func(reserved int, priority string) (config.Config, error) {
		t.Helper()
		raw := map[string]any{"services": map[string]any{"users": map[string]any{"upstream_url": "http://localhost:9090", "max_concurrent": 3, "reserved_critical": reserved}}, "routes": []map[string]any{{"path_prefix": "/users", "service": "users", "priority": priority}}}
		data, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, data, 0600); err != nil {
			t.Fatal(err)
		}
		return config.Load(filename)
	}
	c, err := load(1, "background")
	if err != nil || c.Services["users"].ReservedCritical != 1 || c.Routes[0].Priority != admission.Background {
		t.Fatalf("priority config: %+v %v", c, err)
	}
	c, err = load(0, "")
	if err != nil || c.Routes[0].Priority != admission.Critical {
		t.Fatalf("default priority: %+v %v", c, err)
	}
	for _, tc := range []struct {
		reserved int
		priority string
	}{{-1, "critical"}, {4, "critical"}, {1, "urgent"}} {
		if _, err := load(tc.reserved, tc.priority); err == nil {
			t.Fatal("invalid priority/reservation accepted")
		}
	}
	if _, err := load(3, "critical"); err != nil {
		t.Fatal("full reservation should be allowed", err)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_RESERVED_CRITICAL", "2")
	c, err = load(1, "critical")
	if err != nil || c.Services["users"].ReservedCritical != 2 {
		t.Fatalf("environment reservation: %+v %v", c, err)
	}
	for _, value := range []string{"-1", "4", "bad", ""} {
		t.Setenv("GATEWISE_SERVICE_USERS_RESERVED_CRITICAL", value)
		if _, err := load(1, "critical"); err == nil {
			t.Fatalf("accepted invalid reservation %q", value)
		}
	}
}
