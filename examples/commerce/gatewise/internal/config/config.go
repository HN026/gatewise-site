package config

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/HN026/gatewise/internal/admission"
	"github.com/HN026/gatewise/middleware"
)

const DefaultMaxConcurrent = 100

type Service struct {
	Upstream         *url.URL
	MaxConcurrent    int
	ReservedCritical int
	Adaptive         *middleware.AdaptiveConfig
}

type Route struct {
	Prefix        string
	Service       string
	StripPrefix   bool
	Priority      admission.Priority
	LatencyTarget time.Duration
}
type Config struct {
	ListenAddr            string
	Services              map[string]Service
	Routes                []Route
	DialTimeout           time.Duration
	ResponseHeaderTimeout time.Duration
	ShutdownTimeout       time.Duration
}
type rawConfig struct {
	ListenAddr            string `json:"listen_addr"`
	DialTimeout           string `json:"dial_timeout"`
	ResponseHeaderTimeout string `json:"response_header_timeout"`
	ShutdownTimeout       string `json:"shutdown_timeout"`
	Services              map[string]struct {
		UpstreamURL      string       `json:"upstream_url"`
		Mode             string       `json:"mode"`
		Adaptive         *rawAdaptive `json:"adaptive"`
		MaxConcurrent    *int         `json:"max_concurrent"`
		ReservedCritical int          `json:"reserved_critical"`
	} `json:"services"`
	Routes []struct {
		Prefix        string             `json:"path_prefix"`
		Service       string             `json:"service"`
		StripPrefix   bool               `json:"strip_prefix"`
		Priority      admission.Priority `json:"priority"`
		LatencyTarget string             `json:"latency_target"`
	} `json:"routes"`
}

func Load(filename string) (Config, error) { return LoadWithEnv(filename, "") }

// LoadWithEnv resolves process environment > dotenv > JSON > defaults.
func LoadWithEnv(filename, envFile string) (Config, error) {
	values, err := readEnvFile(envFile)
	if err != nil {
		return Config{}, err
	}
	lookup := func(key string) (string, bool) {
		if value, ok := os.LookupEnv(key); ok {
			return value, true
		}
		value, ok := values[key]
		return value, ok
	}
	f, err := os.Open(filename)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()
	raw := rawConfig{ListenAddr: "127.0.0.1:8080", DialTimeout: "2s", ResponseHeaderTimeout: "2s", ShutdownTimeout: "5s"}
	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&raw); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return Config{}, fmt.Errorf("config must contain exactly one JSON object")
	}
	for _, field := range []struct {
		key string
		dst *string
	}{
		{"GATEWISE_LISTEN_ADDR", &raw.ListenAddr},
		{"GATEWISE_DIAL_TIMEOUT", &raw.DialTimeout}, {"GATEWISE_RESPONSE_HEADER_TIMEOUT", &raw.ResponseHeaderTimeout}, {"GATEWISE_SHUTDOWN_TIMEOUT", &raw.ShutdownTimeout},
	} {
		if value, ok := lookup(field.key); ok {
			*field.dst = value
		}
	}
	_, port, err := net.SplitHostPort(raw.ListenAddr)
	if err != nil {
		return Config{}, fmt.Errorf("listen_addr must be host:port")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("listen_addr port must be between 1 and 65535")
	}
	c := Config{ListenAddr: raw.ListenAddr, Services: make(map[string]Service)}
	for _, field := range []struct {
		name, value string
		dst         *time.Duration
	}{
		{"dial_timeout", raw.DialTimeout, &c.DialTimeout}, {"response_header_timeout", raw.ResponseHeaderTimeout, &c.ResponseHeaderTimeout}, {"shutdown_timeout", raw.ShutdownTimeout, &c.ShutdownTimeout},
	} {
		d, e := time.ParseDuration(field.value)
		if e != nil || d <= 0 {
			return Config{}, fmt.Errorf("%s must be a positive duration such as 2s", field.name)
		}
		*field.dst = d
	}

	namePattern := regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
	for name, service := range raw.Services {
		if !namePattern.MatchString(name) {
			return Config{}, fmt.Errorf("service names must match [a-z][a-z0-9_]*")
		}
		value := service.UpstreamURL
		if override, ok := lookup("GATEWISE_SERVICE_" + strings.ToUpper(name) + "_URL"); ok {
			value = override
		}
		target, e := resolveURL("service "+name, value, lookup)
		if e != nil {
			return Config{}, e
		}
		limit := DefaultMaxConcurrent
		if service.MaxConcurrent != nil {
			limit = *service.MaxConcurrent
		}
		if override, ok := lookup("GATEWISE_SERVICE_" + strings.ToUpper(name) + "_MAX_CONCURRENT"); ok {
			var parseErr error
			limit, parseErr = strconv.Atoi(override)
			if parseErr != nil {
				return Config{}, fmt.Errorf("service %s max_concurrent must be a positive integer", name)
			}
		}
		if limit <= 0 {
			return Config{}, fmt.Errorf("service %s max_concurrent must be a positive integer", name)
		}
		reserved := service.ReservedCritical
		if override, ok := lookup("GATEWISE_SERVICE_" + strings.ToUpper(name) + "_RESERVED_CRITICAL"); ok {
			var parseErr error
			reserved, parseErr = strconv.Atoi(override)
			if parseErr != nil {
				return Config{}, fmt.Errorf("service %s reserved_critical must be an integer between 0 and max_concurrent", name)
			}
		}
		if reserved < 0 || reserved > limit {
			return Config{}, fmt.Errorf("service %s reserved_critical must be between 0 and max_concurrent", name)
		}
		a, err := parseAdaptive(name, service.Mode, service.Adaptive, lookup, limit, reserved)
		if err != nil {
			return Config{}, err
		}
		c.Services[name] = Service{Upstream: target, MaxConcurrent: limit, ReservedCritical: reserved, Adaptive: a}
	}
	prefixes := map[string]bool{}
	for _, r := range raw.Routes {
		if r.Prefix == "" || !strings.HasPrefix(r.Prefix, "/") || path.Clean(r.Prefix) != r.Prefix || strings.ContainsAny(r.Prefix, "?#%\\") || (r.Prefix == "/healthz" || r.Prefix == "/metrics") {
			return Config{}, fmt.Errorf("invalid path_prefix %q: use a clean absolute path without trailing slash; /healthz and /metrics are reserved", r.Prefix)
		}
		if prefixes[r.Prefix] {
			return Config{}, fmt.Errorf("duplicate path_prefix %q", r.Prefix)
		}
		prefixes[r.Prefix] = true
		if _, ok := c.Services[r.Service]; !ok {
			return Config{}, fmt.Errorf("route %q references unknown service %q", r.Prefix, r.Service)
		}
		if r.Prefix == "/" && r.StripPrefix {
			return Config{}, fmt.Errorf("root route cannot strip its prefix")
		}
		priority := r.Priority
		if priority == "" {
			priority = admission.Critical
		}
		if priority != admission.Critical && priority != admission.Background {
			return Config{}, fmt.Errorf("route %q priority must be critical or background", r.Prefix)
		}
		var latencyTarget time.Duration
		if r.LatencyTarget != "" {
			var err error
			latencyTarget, err = time.ParseDuration(r.LatencyTarget)
			if err != nil || latencyTarget <= 0 {
				return Config{}, fmt.Errorf("route %q latency_target must be positive", r.Prefix)
			}
		}
		c.Routes = append(c.Routes, Route{Prefix: r.Prefix, Service: r.Service, StripPrefix: r.StripPrefix, Priority: priority, LatencyTarget: latencyTarget})
	}
	if len(c.Routes) == 0 {
		return Config{}, fmt.Errorf("configure at least one route and its named service")
	}
	return c, nil
}

var placeholder = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

func resolveURL(name, value string, lookup func(string) (string, bool)) (*url.URL, error) {
	var missing string
	value = placeholder.ReplaceAllStringFunc(value, func(token string) string {
		key := token[2 : len(token)-1]
		v, ok := lookup(key)
		if !ok || v == "" {
			missing = key
		}
		return v
	})
	if missing != "" {
		return nil, fmt.Errorf("%s: environment variable %s is unset or empty", name, missing)
	}
	u, err := url.Parse(value)
	if err != nil || u == nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return nil, fmt.Errorf("%s must be an absolute HTTP(S) URL without credentials, query, or fragment", name)
	}
	if p := u.Port(); p != "" {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return nil, fmt.Errorf("%s port must be between 1 and 65535", name)
		}
	}
	return u, nil
}
