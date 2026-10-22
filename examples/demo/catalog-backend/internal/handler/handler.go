package handler

import (
	"encoding/json"
	"net/http"
	"time"
)

func New() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /hello", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]string{"service": "catalog", "message": "Hello from the catalog backend"})
	})
	mux.HandleFunc("GET /products", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"service": "catalog", "products": []map[string]any{{"id": "p1", "name": "Notebook"}, {"id": "p2", "name": "Pen"}}})
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
			respond(w, 200, map[string]string{"service": "catalog", "delay": delay.String()})
		}
	})
	return mux
}
func respond(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Backend-Service", "catalog")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
