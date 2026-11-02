package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

type Config struct {
	QueueWaiting, QueueReserved                                     int
	QueueWait, QueueAging                                           time.Duration
	AdaptiveProfile                                                 string
	RouteBudgets                                                    bool
	OperationsMode, ReplicaURL                                      string
	DecisionMode                                                    string
	ResourceMode                                                    string
	SharingCeiling                                                  int
	PriorityTrusted                                                 string
	Priorities                                                      bool
	CatalogURL                                                      string
	TenantLimit, TenantMaxTracked                                   int
	TenantIdleTTL                                                   time.Duration
	Listen, AdminListen, DatabaseURL, Mode, Role                    string
	PoolSize, MaxConcurrent, Reserved, Minimum, Initial, MinSamples int
	Interval, Target, QueryTimeout                                  time.Duration
}

func Load(args []string) (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL")}
	f := flag.NewFlagSet("commerce-backend", flag.ContinueOnError)
	env := func(k, d string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return d
	}
	f.IntVar(&c.QueueWaiting, "queue-waiting", 0, "optional waiting capacity; zero disables")
	f.IntVar(&c.QueueReserved, "queue-reserved", 0, "waiting slots protected for critical requests")
	f.DurationVar(&c.QueueWait, "queue-wait", 40*time.Millisecond, "maximum queue wait")
	f.DurationVar(&c.QueueAging, "queue-aging", 20*time.Millisecond, "queue aging threshold")
	f.StringVar(&c.AdaptiveProfile, "adaptive-profile", "legacy", "legacy or stable")
	f.BoolVar(&c.RouteBudgets, "route-budgets", false, "example report latency budget500ms")
	f.StringVar(&c.OperationsMode, "operations", "", "operation demo: disabled, fixed, pool or balanced")
	f.StringVar(&c.ReplicaURL, "replica-url", "", "physical standby database URL")
	f.StringVar(&c.ResourceMode, "resources", "", "real resource regulators: disabled, enforce or observe")
	f.StringVar(&c.DecisionMode, "decisions", "", "controlled decision providers: enforce or observe")
	f.StringVar(&c.PriorityTrusted, "priority-trusted-proxies", "127.0.0.1/32,::1/128", "demo trusted immediate peers; empty trusts none")
	f.BoolVar(&c.Priorities, "priorities", false, "enable example ordered tiers and trusted loopback propagation")
	f.StringVar(&c.CatalogURL, "catalog-url", "", "optional downstream catalog base URL")
	f.StringVar(&c.Listen, "listen", env("COMMERCE_LISTEN_ADDR", "127.0.0.1:9195"), "business HTTP address")
	f.StringVar(&c.AdminListen, "admin-listen", env("COMMERCE_ADMIN_ADDR", "127.0.0.1:9196"), "loopback-only benchmark controls")
	f.StringVar(&c.Mode, "mode", env("COMMERCE_MODE", "direct"), "direct, fixed or adaptive")
	f.StringVar(&c.Role, "role", env("COMMERCE_ROLE", "orders"), "orders, catalog or inventory")
	f.IntVar(&c.SharingCeiling, "tenant-sharing-ceiling", 0, "opt-in tenant borrowing ceiling; tenant-limit becomes a share")
	f.IntVar(&c.TenantLimit, "tenant-limit", 0, "optional per-tenant concurrency limit; demo loopback identity only")
	f.IntVar(&c.TenantMaxTracked, "tenant-max-tracked", 32, "example tenant registry bound")
	f.DurationVar(&c.TenantIdleTTL, "tenant-idle-ttl", 2*time.Second, "example idle tenant TTL")
	f.IntVar(&c.PoolSize, "db-pool", 4, "maximum SQL connections")
	f.IntVar(&c.MaxConcurrent, "max-concurrent", 12, "example admission ceiling")
	f.IntVar(&c.Reserved, "reserved-critical", 4, "example critical reservation")
	f.IntVar(&c.Minimum, "min-concurrent", 5, "example adaptive minimum")
	f.IntVar(&c.Initial, "initial-concurrent", 12, "example starting limit")
	f.IntVar(&c.MinSamples, "min-samples", 10, "completed samples per evaluation")
	f.DurationVar(&c.Interval, "adaptive-interval", 500*time.Millisecond, "feedback interval")
	f.DurationVar(&c.Target, "target-latency", 100*time.Millisecond, "example handler budget")
	f.DurationVar(&c.QueryTimeout, "query-timeout", time.Second, "transaction deadline including pool wait")
	if err := f.Parse(args); err != nil {
		return c, err
	}
	if c.ResourceMode != "" && c.ResourceMode != "disabled" && c.ResourceMode != "enforce" && c.ResourceMode != "observe" {
		return c, fmt.Errorf("invalid resource mode")
	}
	if c.ResourceMode != "" && c.DecisionMode != "" {
		return c, fmt.Errorf("run resource and controlled-signal scenarios separately")
	}
	if c.DecisionMode != "" && c.DecisionMode != "enforce" && c.DecisionMode != "observe" {
		return c, fmt.Errorf("invalid decision mode")
	}
	if c.Role != "orders" && c.Role != "catalog" && c.Role != "inventory" {
		return c, fmt.Errorf("invalid role")
	}
	if c.OperationsMode != "" && (c.Role != "orders" || c.ReplicaURL == "" || c.OperationsMode != "disabled" && c.OperationsMode != "fixed" && c.OperationsMode != "pool" && c.OperationsMode != "balanced") {
		return c, fmt.Errorf("operations require orders role, replica URL and disabled/fixed/pool/balanced mode")
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	h, _, err := net.SplitHostPort(c.AdminListen)
	if err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
		return c, fmt.Errorf("admin-listen must bind numeric loopback")
	}
	if c.Mode != "direct" && c.Mode != "fixed" && c.Mode != "adaptive" && c.Mode != "semaphore" {
		return c, fmt.Errorf("invalid mode")
	}
	if c.Mode == "semaphore" && c.MaxConcurrent < 1 {
		return c, fmt.Errorf("reference semaphore requires a positive limit")
	}
	if c.SharingCeiling < 0 || c.SharingCeiling > 0 && c.TenantLimit <= 0 {
		return c, fmt.Errorf("sharing needs a positive tenant share and ceiling")
	}
	if c.TenantLimit < 0 {
		return c, fmt.Errorf("tenant-limit cannot be negative")
	}
	if c.PoolSize < 1 || c.PoolSize > 128 || c.QueryTimeout <= 0 {
		return c, fmt.Errorf("invalid database pool or timeout")
	}
	return c, nil
}
