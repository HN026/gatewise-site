package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestAdaptiveProfileJSONAndEnvironment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewise.json")
	err := os.WriteFile(path, []byte(`{"services":{"orders":{"upstream_url":"http://127.0.0.1:9090","max_concurrent":12,"reserved_critical":4,"mode":"adaptive","adaptive":{"profile":"stable"}}},"routes":[{"path_prefix":"/","service":"orders","priority":"critical"}]}`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil || c.Services["orders"].Adaptive.Profile != "stable" {
		t.Fatal(c, err)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_ADAPTIVE_PROFILE", "legacy")
	c, err = config.Load(path)
	if err != nil || c.Services["orders"].Adaptive.Profile != "legacy" {
		t.Fatal(c, err)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_ADAPTIVE_PROFILE", "invalid")
	if _, err = config.Load(path); err == nil {
		t.Fatal("invalid profile override accepted")
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_ADAPTIVE_PROFILE", "")
	if _, err = config.Load(path); err == nil {
		t.Fatal("empty override accepted")
	}
}
