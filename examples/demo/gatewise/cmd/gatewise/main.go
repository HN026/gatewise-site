package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/internal/proxy"
	"github.com/HN026/gatewise/internal/server"
)

func main() {
	path := flag.String("config", "configs/local.json", "path to JSON configuration")
	envFile := flag.String("env-file", ".env", "dotenv path; empty disables loading")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(*path, *envFile, logger); err != nil {
		logger.Error("gatewise stopped", "error", err.Error())
		os.Exit(1)
	}
}
func run(path, envFile string, logger *slog.Logger) error {
	c, err := config.LoadWithEnv(path, envFile)
	if err != nil {
		return err
	}
	h, closeIdle := proxy.New(c, logger)
	defer closeIdle()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return server.Run(ctx, c.ListenAddr, h, c.ShutdownTimeout, logger)
}
