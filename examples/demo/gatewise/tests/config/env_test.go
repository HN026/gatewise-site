package config_test

import (
	"fmt"
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnvironmentPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	env := filepath.Join(dir, "local.env")
	if err := os.WriteFile(cfg, []byte(`{"listen_addr":"127.0.0.1:8000","dial_timeout":"1s","services":{"users":{"upstream_url":"${GATEWISE_SERVICE_USERS_URL}"}},"routes":[{"path_prefix":"/users","service":"users","strip_prefix":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(env, []byte("# local values\nexport GATEWISE_LISTEN_ADDR='127.0.0.1:8001' # comment\nGATEWISE_SERVICE_USERS_URL=\"http://127.0.0.1:9090\"\nGATEWISE_DIAL_TIMEOUT=3s # comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWISE_LISTEN_ADDR", "127.0.0.1:8002")
	c, err := config.LoadWithEnv(cfg, env)
	if err != nil {
		t.Fatal(err)
	}
	if c.ListenAddr != "127.0.0.1:8002" || c.DialTimeout != 3*time.Second || c.Services["users"].Upstream.String() != "http://127.0.0.1:9090" || c.ShutdownTimeout != 5*time.Second {
		t.Fatalf("wrong precedence: %+v", c)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_URL", "http://127.0.0.1:9190")
	c, err = config.LoadWithEnv(cfg, env)
	if err != nil || c.Services["users"].Upstream.Port() != "9190" {
		t.Fatalf("service override: %+v %v", c, err)
	}
	t.Setenv("GATEWISE_RESPONSE_HEADER_TIMEOUT", "")
	if _, err = config.LoadWithEnv(cfg, env); err == nil {
		t.Fatal("empty environment duration silently defaulted")
	}
}

func TestInvalidRoutesAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	for name, body := range map[string]string{
		"missing variable": `{"services":{"test":{"upstream_url":"${GATEWISE_TEST_MISSING_VARIABLE}"}},"routes":[{"path_prefix":"/","service":"test"}]}`,
		"unknown service":  `{"routes":[{"path_prefix":"/users","service":"missing"}]}`,
		"duplicate prefix": `{"services":{"a":{"upstream_url":"http://localhost:9090"}},"routes":[{"path_prefix":"/a","service":"a"},{"path_prefix":"/a","service":"a"}]}`,
		"trailing slash":   `{"services":{"a":{"upstream_url":"http://localhost:9090"}},"routes":[{"path_prefix":"/a/","service":"a"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			if err := os.WriteFile(cfg, []byte(body), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := config.Load(cfg); err == nil {
				t.Fatal("invalid configuration accepted")
			}
		})
	}
	if err := os.WriteFile(cfg, []byte(`{"services":{"test":{"upstream_url":"http://localhost:9090"}},"routes":[{"path_prefix":"/","service":"test"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{"not-an-assignment", `KEY="unclosed`, `KEY="value" garbage`} {
		env := filepath.Join(dir, "bad.env")
		if err := os.WriteFile(env, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := config.LoadWithEnv(cfg, env); err == nil {
			t.Fatal("invalid dotenv accepted")
		}
	}
	if _, err := config.LoadWithEnv(cfg, filepath.Join(dir, "missing.env")); err == nil {
		t.Fatal("explicit missing env file accepted")
	}
}

func TestServiceConcurrencyConfiguration(t *testing.T) {
	dir := t.TempDir()
	filename := filepath.Join(dir, "config.json")
	base := `{"services":{"users":{"upstream_url":"http://localhost:9090"%s}},"routes":[{"path_prefix":"/users","service":"users"}]}`
	write := func(field string) {
		t.Helper()
		if err := os.WriteFile(filename, []byte(fmt.Sprintf(base, field)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("")
	c, err := config.Load(filename)
	if err != nil || c.Services["users"].MaxConcurrent != config.DefaultMaxConcurrent {
		t.Fatalf("default limit: %+v %v", c, err)
	}
	write(`,"max_concurrent":2`)
	c, err = config.Load(filename)
	if err != nil || c.Services["users"].MaxConcurrent != 2 {
		t.Fatalf("JSON limit: %+v %v", c, err)
	}
	for _, value := range []string{"0", "-1", "1.5"} {
		write(`,"max_concurrent":` + value)
		if _, err := config.Load(filename); err == nil {
			t.Fatalf("accepted limit %s", value)
		}
	}
	write(`,"max_concurrent":2`)
	for _, value := range []string{"0", "-2", "bad", ""} {
		t.Setenv("GATEWISE_SERVICE_USERS_MAX_CONCURRENT", value)
		if _, err := config.Load(filename); err == nil {
			t.Fatalf("accepted environment limit %q", value)
		}
	}
	envfile := filepath.Join(dir, "test.env")
	if err := os.WriteFile(envfile, []byte("GATEWISE_SERVICE_USERS_MAX_CONCURRENT=3\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWISE_SERVICE_USERS_MAX_CONCURRENT", "4")
	c, err = config.LoadWithEnv(filename, envfile)
	if err != nil || c.Services["users"].MaxConcurrent != 4 {
		t.Fatalf("environment limit: %+v %v", c, err)
	}
}
