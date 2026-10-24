package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTenantConfigValidationAndEnvironment(t *testing.T) {
	base := `{"services":{"orders":{"upstream_url":"http://127.0.0.1:1","max_concurrent":4,"tenants":TENANT}},"routes":[{"path_prefix":"/orders","service":"orders"}]}`
	good := `{"max_concurrent":1,"max_tracked":3,"idle_ttl":"1s","header":"X-Tenant","trusted_proxies":["127.0.0.1/32"]}`
	path := filepath.Join(t.TempDir(), "config.json")
	write := func(value string) {
		t.Helper()
		if e := os.WriteFile(path, []byte(strings.Replace(base, "TENANT", value, 1)), 0600); e != nil {
			t.Fatal(e)
		}
	}
	write(good)
	c, e := config.Load(path)
	if e != nil || c.Services["orders"].Tenants == nil {
		t.Fatal(e)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_MAX_CONCURRENT", "2")
	c, e = config.Load(path)
	if e != nil || c.Services["orders"].Tenants.MaxConcurrent != 2 {
		t.Fatal(e)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_TENANT_MAX_CONCURRENT", "1")
	for _, bad := range []string{strings.Replace(good, `"max_tracked":3`, `"max_tracked":-1`, 1), strings.Replace(good, `"1s"`, `"0s"`, 1), strings.Replace(good, `"127.0.0.1/32"`, `"invalid"`, 1), strings.Replace(good, `"header":"X-Tenant"`, `"header":"Authorization"`, 1), strings.Replace(good, `"max_tracked":3`, `"missing_policy":"ignore"`, 1), strings.Replace(good, `"max_tracked":3`, `"limits":{"a":5}`, 1)} {
		write(bad)
		if _, e = config.Load(path); e == nil {
			t.Fatal("accepted invalid tenant configuration")
		}
	}
}
