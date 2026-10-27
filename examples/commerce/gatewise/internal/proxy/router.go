package proxy

import (
	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/internal/config"
	"github.com/HN026/gatewise/middleware"
	"net/http"
	"sort"
	"strings"
)

type backend struct {
	handler http.Handler
	guard   *middleware.Service
}

type routeHandler struct {
	prefix  string
	handler http.Handler
}

// Longest segment-prefix wins, independently of config order.
func newRouter(routes []config.Route, backends map[string]backend) http.Handler {
	entries := make([]routeHandler, 0, len(routes))
	for _, r := range routes {
		b := backends[r.Service]
		priority := r.Priority
		if priority == "" {
			priority = admission.Critical
		}
		var options []middleware.HandlerOption
		if r.Resource != "" {
			options = append(options, middleware.WithResource(r.Resource))
		}
		if r.LatencyTarget > 0 {
			options = append(options, middleware.WithLatencyTarget(r.LatencyTarget))
		}
		h := b.guard.Middleware(middleware.Priority(priority), b.handler, options...)
		if r.StripPrefix {
			h = http.StripPrefix(r.Prefix, h)
		}
		entries = append(entries, routeHandler{prefix: r.Prefix, handler: h})
	}
	sort.Slice(entries, func(i, j int) bool { return len(entries[i].prefix) > len(entries[j].prefix) })
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for _, entry := range entries {
			if entry.prefix == "/" || r.URL.Path == entry.prefix || strings.HasPrefix(r.URL.Path, entry.prefix+"/") {
				entry.handler.ServeHTTP(w, r)
				return
			}
		}

		writeJSON(w, http.StatusNotFound, map[string]string{"error": "route_not_found"})
	})
}
