package config_test

import (
	"github.com/HN026/gatewise/internal/config"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestQueueConfigurationAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gatewise.json")
	err := os.WriteFile(path, []byte(`{"read_timeout":"5s","services":{"orders":{"upstream_url":"http://127.0.0.1:9090","max_concurrent":4,"queue":{"max_waiting":8,"max_wait":"40ms","reserved":{"critical":2}}}},"routes":[{"path_prefix":"/","service":"orders"}]}`), 0600)
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil || c.Services["orders"].Queue.MaxWait != 40*time.Millisecond {
		t.Fatal(c, err)
	}
	t.Setenv("GATEWISE_READ_TIMEOUT", "")
	if _, err = config.Load(path); err == nil {
		t.Fatal("body buffering without upload timeout")
	}
	t.Setenv("GATEWISE_READ_TIMEOUT", "5s")
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_MAX_WAITING", "16")
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_AGING_AFTER", "10ms")
	c, err = config.Load(path)
	if err != nil || c.Services["orders"].Queue.MaxWaiting != 16 || c.Services["orders"].Queue.AgingAfter != 10*time.Millisecond {
		t.Fatal(c, err)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_RESERVED", `{"unknown":1}`)
	if _, err = config.Load(path); err == nil {
		t.Fatal("unknown priority accepted")
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_ENABLED", "false")
	c, err = config.Load(path)
	if err != nil || c.Services["orders"].Queue != nil {
		t.Fatal("disabled override", err)
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_ENABLED", "true")
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_RESERVED", `{"critical":17}`)
	if _, err = config.Load(path); err == nil {
		t.Fatal("oversubscribed reservation accepted")
	}
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_RESERVED", `{"critical":2}`)
	t.Setenv("GATEWISE_SERVICE_ORDERS_QUEUE_MAX_WAITING", "0")
	if _, err = config.Load(path); err == nil {
		t.Fatal("unbounded queue accepted")
	}
}
