package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/HN026/gatewise/middleware"
)

func New(guard *middleware.Service, metrics http.Handler) http.Handler {
	business := http.NewServeMux()
	business.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]string{"service": "middleware-demo", "message": "Hello from direct middleware"})
	})
	business.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			respond(w, 413, map[string]string{"error": "body_too_large"})
			return
		}
		respond(w, 200, map[string]any{"service": "middleware-demo", "method": r.Method, "path": r.URL.Path, "query": r.URL.Query(), "body": string(body)})
	})
	business.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		delay := time.Second
		if raw := r.URL.Query().Get("delay"); raw != "" {
			var err error
			delay, err = time.ParseDuration(raw)
			if err != nil || delay < 0 || delay > 30*time.Second {
				respond(w, 400, map[string]string{"error": "delay must be between 0s and 30s"})
				return
			}
		}
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-r.Context().Done():
			return
		case <-timer.C:
			respond(w, 200, map[string]string{"service": "middleware-demo", "delay": delay.String()})
		}
	})
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { respond(w, 200, map[string]string{"status": "ok"}) })
	// Both wrappers deliberately share the same Service instance.
	mux.Handle("/critical/", http.StripPrefix("/critical", guard.Middleware(middleware.Critical, business)))
	mux.Handle("/reports/", http.StripPrefix("/reports", guard.Middleware(middleware.Critical, business, middleware.WithLatencyTarget(time.Second))))
	mux.Handle("/background/", http.StripPrefix("/background", guard.Middleware(middleware.Background, business)))
	return mux
}

func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
