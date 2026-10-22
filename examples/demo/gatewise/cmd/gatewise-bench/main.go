// gatewise-bench runs local, isolated M6 overload experiments.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/HN026/gatewise/internal/benchmark"
)

func main() {
	c := benchmark.Config{}
	flag.StringVar(&c.Backend, "backend-bin", "../benchmark-backend/bin/benchmark-backend", "independent synthetic backend binary")
	flag.StringVar(&c.Proxy, "proxy-bin", "bin/gatewise", "Gatewise proxy binary")
	flag.StringVar(&c.Output, "output", "benchmarks/results/"+time.Now().UTC().Format("20060102T150405Z"), "new directory for JSON, CSV, process logs and metrics")
	flag.IntVar(&c.Repeats, "repeats", 3, "repetitions of all five deployments")
	flag.DurationVar(&c.Duration, "phase-duration", 5*time.Second, "duration of each baseline, pressure and recovery phase; minimum 2s")
	flag.IntVar(&c.BaselineRate, "baseline-rps", 200, "scheduled baseline requests per second")
	flag.IntVar(&c.PressureRate, "pressure-rps", 400, "scheduled pressure and recovery requests per second")
	flag.IntVar(&c.MaxInFlight, "max-in-flight", 256, "generator ceiling; overflow is reported as dropped schedules")
	flag.Float64Var(&c.MaxDropPercent, "max-drop-percent", 1, "maximum dropped schedule percent per priority/phase; zero requires perfect delivery")
	flag.Parse()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := benchmark.Run(ctx, c); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
