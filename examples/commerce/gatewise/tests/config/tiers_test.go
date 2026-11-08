package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestTierConfiguration(t *testing.T) {
	p := filepath.Join(t.TempDir(), "config.json")
	valid := `{"services":{"orders":{"upstream_url":"http://127.0.0.1:9090","max_concurrent":8,"priorities":{"tiers":[{"name":"urgent","reserved":2},{"name":"normal","reserved":1},{"name":"batch","reserved":0}],"cohorts":{"buckets":128,"rotation":"2s","seed":"test-only-seed-123"}}}},"routes":[{"path_prefix":"/","service":"orders","priority":"normal"}]}`
	os.WriteFile(p, []byte(valid), 0600)
	c, e := config.Load(p)
	if e != nil || c.Services["orders"].Priorities == nil {
		t.Fatal(e)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_PRIORITY_COHORT_SEED", "short")
	if _, e = config.Load(p); e == nil {
		t.Fatal("bad seed accepted")
	}
}
