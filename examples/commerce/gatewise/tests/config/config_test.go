package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad(t *testing.T) {
	validConfig := `{"services":{"test":{"upstream_url":"http://localhost:9090"}},"routes":[{"path_prefix":"/","service":"test"}]}`
	for _, tc := range []struct {
		name, body string
		valid      bool
	}{
		{"defaults", validConfig, true},
		{"missing routes", `{}`, false},
		{"top-level upstream rejected", `{"upstream_url":"http://localhost:9090"}`, false},
		{"scheme", strings.Replace(validConfig, "http://localhost:9090", "file:///tmp/demo", 1), false},
		{"duration", strings.TrimSuffix(validConfig, "}") + `,"dial_timeout":"0s"}`, false},
		{"unknown field", strings.TrimSuffix(validConfig, "}") + `,"typo":1}`, false},
		{"extra value", validConfig + ` {}`, false},
		{"invalid port", strings.Replace(validConfig, "9090", "99999", 1), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(p, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load(p)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%v, err=%v", tc.valid, err)
			}
			if tc.valid && (c.ListenAddr != "127.0.0.1:8080" || c.DialTimeout <= 0) {
				t.Fatalf("missing defaults: %+v", c)
			}
		})
	}
}
