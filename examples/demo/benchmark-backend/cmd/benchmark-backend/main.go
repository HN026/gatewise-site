package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hunaseer/benchmark-backend/internal/workload"
	"github.com/HN026/gatewise/metrics"
	"github.com/HN026/gatewise/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:9190", "loopback listen address; :0 selects a free port")
	mode := flag.String("mode", "direct", "direct, fixed or adaptive")
	capacity := flag.Int("capacity", 12, "synthetic contention threshold")
	base := flag.Duration("base-latency", 20*time.Millisecond, "base work duration")
	penalty := flag.Duration("contention-penalty", 50*time.Millisecond, "additional duration per excess active request")
	maximum := flag.Duration("max-latency", 400*time.Millisecond, "maximum work duration")
	flag.Parse()
	if err := run(*listen, *mode, *capacity, *base, *penalty, *maximum); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(addr, mode string, capacity int, base, penalty, maximum time.Duration) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("benchmark backend must listen on a loopback IP")
	}
	if capacity < 1 || base <= 0 || penalty < 0 || maximum < base {
		return fmt.Errorf("invalid workload configuration")
	}
	if mode != "direct" && mode != "fixed" && mode != "adaptive" {
		return fmt.Errorf("invalid mode %q", mode)
	}
	s := workload.New(capacity, base, penalty, maximum)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	mux.HandleFunc("POST /control", s.Control)
	mux.HandleFunc("GET /state", s.State)
	var critical, background http.Handler = http.HandlerFunc(s.Work), http.HandlerFunc(s.Work)
	if mode != "direct" {
		cfg := middleware.ServiceConfig{Name: "benchmark", MaxConcurrent: 6, ReservedCritical: 1}
		if mode == "adaptive" {
			cfg.Adaptive = &middleware.AdaptiveConfig{MinConcurrent: 2, InitialConcurrent: 6, Interval: 500 * time.Millisecond, MinSamples: 10, TargetLatency: 100 * time.Millisecond}
		}
		guard, err := middleware.NewService(cfg)
		if err != nil {
			return err
		}
		registry := prometheus.NewRegistry()
		if err := metrics.Register(registry, guard); err != nil {
			return err
		}
		mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
		critical = guard.Middleware(middleware.Critical, critical)
		background = guard.Middleware(middleware.Background, background)
	}
	mux.Handle("GET /critical/work", critical)
	mux.Handle("GET /background/work", background)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	fmt.Println("http://" + listener.Addr().String())
	server := &http.Server{Handler: mux, ReadHeaderTimeout: 2 * time.Second, IdleTimeout: 30 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err := <-done:
		if err != http.ErrServerClosed {
			return err
		}
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		return server.Shutdown(shutdown)
	}
	return nil
}
