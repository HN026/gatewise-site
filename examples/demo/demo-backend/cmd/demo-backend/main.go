package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hunaseer/demo-backend/internal/handler"
	"github.com/hunaseer/demo-backend/internal/server"
)

func main() {
	addr := flag.String("listen", "127.0.0.1:9090", "listen address")
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	mux := handler.New()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := server.Run(ctx, *addr, mux, 5*time.Second, logger); err != nil {
		logger.Error("demo backend stopped", "error", err.Error())
		os.Exit(1)
	}
}
