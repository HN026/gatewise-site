// Package handler provides the standalone demo service HTTP endpoints.
package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// New registers the demo endpoints. It has no dependency on Gatewise.
func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Demo-Backend", "gatewise-demo")
		respond(w, 200, map[string]string{"message": "Hello from the demo backend"})
	})
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
		if err != nil {
			respond(w, 413, map[string]string{"error": "body_too_large"})
			return
		}
		respond(w, 200, map[string]any{"method": r.Method, "path": r.URL.Path, "query": r.URL.Query(), "body": string(body), "headers": r.Header, "host": r.Host})
	})
	mux.HandleFunc("GET /slow", func(w http.ResponseWriter, r *http.Request) {
		delay := 3 * time.Second
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
			respond(w, 200, map[string]string{"message": "Slow response completed", "delay": delay.String()})
		}
	})
	return mux
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
