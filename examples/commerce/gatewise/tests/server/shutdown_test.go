package server_test

import (
	"context"
	"errors"
	"github.com/HN026/gatewise/internal/server"
	"github.com/HN026/gatewise/middleware"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"
)

type listening struct{ ready chan string }

func (h listening) Enabled(context.Context, slog.Level) bool { return true }
func (h listening) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "address" {
			h.ready <- a.Value.String()
		}
		return true
	})
	return nil
}
func (h listening) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h listening) WithGroup(string) slog.Handler      { return h }

func TestShutdownRespectsActualHandlerLifetime(t *testing.T) {
	for _, force := range []bool{false, true} {
		name := "graceful"
		if force {
			name = "grace-expires"
		}
		t.Run(name, func(t *testing.T) {
			ready := make(chan string, 1)
			entered := make(chan struct{})
			release := make(chan struct{})
			finished := make(chan struct{})
			guard, err := middleware.NewService(middleware.ServiceConfig{Name: "shutdown", MaxConcurrent: 1})
			if err != nil {
				t.Fatal(err)
			}
			h := guard.Middleware(middleware.Critical, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				close(entered)
				defer close(finished)
				<-release
				w.WriteHeader(204)
			}))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			grace := time.Second
			if force {
				grace = 40 * time.Millisecond
			}
			done := make(chan error, 1)
			go func() { done <- server.Run(ctx, "127.0.0.1:0", h, grace, slog.New(listening{ready})) }()
			var address string
			select {
			case address = <-ready:
			case <-time.After(3 * time.Second):
				t.Fatal("listener not ready")
			}
			requestDone := make(chan error, 1)
			go func() {
				r, e := http.Get("http://" + address)
				if e == nil {
					_, _ = io.Copy(io.Discard, r.Body)
					r.Body.Close()
					if r.StatusCode != 204 {
						e = errors.New("unexpected response")
					}
				}
				requestDone <- e
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("handler did not start")
			}
			cancel()
			if force {
				select {
				case err := <-done:
					if !errors.Is(err, context.DeadlineExceeded) {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("shutdown did not respect grace")
				}
				if guard.Snapshot().Active != 1 {
					t.Fatal("forced connection close freed still-running handler")
				}
				close(release)
			} else {
				close(release)
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("graceful shutdown stuck")
				}
			}
			select {
			case <-finished:
			case <-time.After(time.Second):
				t.Fatal("handler did not finish")
			}
			select {
			case err := <-requestDone:
				if !force && err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("client did not finish")
			}
			until := time.Now().Add(time.Second)
			for guard.Snapshot().Active != 0 && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if guard.Snapshot().Active != 0 {
				t.Fatal("permit retained after handler return")
			}
		})
	}
}
