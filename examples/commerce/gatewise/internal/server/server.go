package server

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"
)

// Run waits for active requests on cancellation, then force-closes after grace.
func Run(ctx context.Context, addr string, handler http.Handler, grace time.Duration, logger *slog.Logger) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second, ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError)}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	logger.Info("server listening", "address", ln.Addr().String())
	select {
	case err := <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		logger.Info("shutdown started", "grace", grace.String())
		shutdownCtx, cancel := context.WithTimeout(context.Background(), grace)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			_ = srv.Close()
			<-done
			return err
		}
		<-done
		logger.Info("shutdown complete")
		return nil
	}
}
