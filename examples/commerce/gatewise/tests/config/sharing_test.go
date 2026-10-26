package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
)

func TestSharingConfigurationAndOverrides(t *testing.T) {
	base := `{"services":{"orders":{"upstream_url":"http://127.0.0.1:1","max_concurrent":12,"tenants":{"max_concurrent":2,"idle_ttl":"2s","header":"X-Tenant","trusted_proxies":["127.0.0.1/32"],"sharing":{"max_concurrent":4,"demand_ttl":"500ms"}}}},"routes":[{"path_prefix":"/","service":"orders"}]}`
	p := filepath.Join(t.TempDir(), "config.json")
	os.WriteFile(p, []byte(base), 0600)
	c, e := config.Load(p)
	if e != nil || c.Services["orders"].Tenants.Sharing.MaxConcurrent != 4 {
		t.Fatal(e)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_SHARING_MAX_CONCURRENT", "6")
	c, e = config.Load(p)
	if e != nil || c.Services["orders"].Tenants.Sharing.MaxConcurrent != 6 {
		t.Fatal(e)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_SHARING_DEMAND_TTL", "3s")
	if _, e = config.Load(p); e == nil {
		t.Fatal("demand window exceeds registry TTL")
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_SHARING_DEMAND_TTL", "0s")
	if _, e = config.Load(p); e == nil {
		t.Fatal("zero duration accepted")
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_SHARING_DEMAND_TTL", "500ms")
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_SHARING_MAX_CONCURRENT", "1")
	if _, e = config.Load(p); e == nil {
		t.Fatal("ceiling below share accepted")
	}
}
