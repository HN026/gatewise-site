package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/hunaseer/middleware-backend/internal/handler"
	"github.com/hunaseer/middleware-backend/internal/server"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:9092", "listen address")
	limit := flag.Int("max-concurrent", 2, "shared active request limit")
	reserved := flag.Int("reserved-critical", 1, "capacity reserved for critical handlers")
	enabled := flag.Bool("adaptive", false, "enable adaptive admission")
	minimum := flag.Int("min-concurrent", 0, "adaptive minimum; zero uses reservation+1")
	initial := flag.Int("initial-concurrent", 0, "adaptive starting limit; zero uses minimum")
	interval := flag.Duration("adaptive-interval", time.Second, "minimum feedback interval")
	samples := flag.Int("min-samples", 10, "completed samples needed before adjustment")
	target := flag.Duration("target-latency", 100*time.Millisecond, "default handler latency budget")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := middleware.ServiceConfig{Name: "middleware-demo", MaxConcurrent: *limit, ReservedCritical: *reserved, Logger: logger}
	if *enabled {
		cfg.Adaptive = &middleware.AdaptiveConfig{MinConcurrent: *minimum, InitialConcurrent: *initial, Interval: *interval, MinSamples: *samples, TargetLatency: *target}
	}
	guard, err := middleware.NewService(cfg)
	if err != nil {
		logger.Error("invalid middleware configuration", "error", err.Error())
		os.Exit(1)
	}
	registry := prometheus.NewRegistry()
	if err := metrics.Register(registry, guard); err != nil {
		logger.Error("metrics registration failed", "error", err.Error())
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *addr, handler.New(guard, promhttp.HandlerFor(registry, promhttp.HandlerOpts{})), 5*time.Second, logger); err != nil {
		logger.Error("middleware backend stopped", "error", err.Error())
		os.Exit(1)
	}
}
