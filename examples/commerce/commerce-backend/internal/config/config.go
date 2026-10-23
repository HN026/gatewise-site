package config

import (
	"flag"
	"fmt"
	"net"
	"os"
	"time"
)

type Config struct {
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
	f.StringVar(&c.Listen, "listen", env("COMMERCE_LISTEN_ADDR", "127.0.0.1:9195"), "business HTTP address")
	f.StringVar(&c.AdminListen, "admin-listen", env("COMMERCE_ADMIN_ADDR", "127.0.0.1:9196"), "loopback-only benchmark controls")
	f.StringVar(&c.Mode, "mode", env("COMMERCE_MODE", "direct"), "direct, fixed or adaptive")
	f.StringVar(&c.Role, "role", env("COMMERCE_ROLE", "orders"), "orders, catalog or inventory")
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
	if c.Role != "orders" && c.Role != "catalog" && c.Role != "inventory" {
		return c, fmt.Errorf("invalid role")
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	h, _, err := net.SplitHostPort(c.AdminListen)
	if err != nil || net.ParseIP(h) == nil || !net.ParseIP(h).IsLoopback() {
		return c, fmt.Errorf("admin-listen must bind numeric loopback")
	}
	if c.Mode != "direct" && c.Mode != "fixed" && c.Mode != "adaptive" {
		return c, fmt.Errorf("invalid mode")
	}
	if c.PoolSize < 1 || c.PoolSize > 128 || c.QueryTimeout <= 0 {
		return c, fmt.Errorf("invalid database pool or timeout")
	}
	return c, nil
}
