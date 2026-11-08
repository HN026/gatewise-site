package config

import (
	"fmt"
	"github.com/HN026/gatewise/middleware"
	"strconv"
	"strings"
	"time"
)

type rawSharing struct {
	MaxConcurrent int            `json:"max_concurrent"`
	Limits        map[string]int `json:"limits"`
	DemandTTL     string         `json:"demand_ttl"`
}
type rawTenant struct {
	Sharing        *rawSharing             `json:"sharing"`
	MaxConcurrent  int                     `json:"max_concurrent"`
	Limits         map[string]int          `json:"limits"`
	MaxTracked     int                     `json:"max_tracked"`
	IdleTTL        string                  `json:"idle_ttl"`
	Header         string                  `json:"header"`
	TrustedProxies []string                `json:"trusted_proxies"`
	MissingPolicy  middleware.TenantPolicy `json:"missing_policy"`
	OverflowPolicy middleware.TenantPolicy `json:"overflow_policy"`
}

func parseTenant(name string, raw *rawTenant, lookup func(string) (string, bool), maximum int) (*middleware.TenantConfig, error) {
	var r rawTenant
	if raw != nil {
		r = *raw
	}
	enabled := raw != nil
	prefix := "GATEWISE_SERVICE_" + strings.ToUpper(name) + "_TENANT_"
	for _, v := range []struct {
		key string
		dst *int
	}{{"MAX_CONCURRENT", &r.MaxConcurrent}, {"MAX_TRACKED", &r.MaxTracked}} {
		if value, ok := lookup(prefix + v.key); ok {
			enabled = true
			n, e := strconv.Atoi(value)
			if e != nil {
				return nil, fmt.Errorf("service %s tenant %s must be an integer", name, strings.ToLower(v.key))
			}
			*v.dst = n
		}
	}
	for _, v := range []struct {
		key string
		dst *string
	}{{"IDLE_TTL", &r.IdleTTL}, {"HEADER", &r.Header}} {
		if value, ok := lookup(prefix + v.key); ok {
			enabled = true
			*v.dst = value
		}
	}
	if value, ok := lookup(prefix + "TRUSTED_PROXIES"); ok {
		enabled = true
		r.TrustedProxies = nil
		for _, cidr := range strings.Split(value, ",") {
			r.TrustedProxies = append(r.TrustedProxies, strings.TrimSpace(cidr))
		}
	}
	if value, ok := lookup(prefix + "MISSING_POLICY"); ok {
		enabled = true
		r.MissingPolicy = middleware.TenantPolicy(value)
	}
	if value, ok := lookup(prefix + "OVERFLOW_POLICY"); ok {
		enabled = true
		r.OverflowPolicy = middleware.TenantPolicy(value)
	}
	if v, ok := lookup(prefix + "SHARING_MAX_CONCURRENT"); ok {
		enabled = true
		if r.Sharing == nil {
			r.Sharing = &rawSharing{}
		}
		n, e := strconv.Atoi(v)
		if e != nil {
			return nil, fmt.Errorf("sharing max_concurrent must be an integer")
		}
		r.Sharing.MaxConcurrent = n
	}
	if v, ok := lookup(prefix + "SHARING_DEMAND_TTL"); ok {
		enabled = true
		if r.Sharing == nil {
			r.Sharing = &rawSharing{}
		}
		r.Sharing.DemandTTL = v
	}
	if !enabled {
		return nil, nil
	}
	resolver, e := middleware.TrustedTenantHeader(r.Header, r.TrustedProxies)
	if e != nil {
		return nil, fmt.Errorf("service %s tenants: %w", name, e)
	}
	ttl := 5 * time.Minute
	if r.IdleTTL != "" {
		ttl, e = time.ParseDuration(r.IdleTTL)
		if e != nil || ttl <= 0 {
			return nil, fmt.Errorf("service %s tenant idle_ttl must be positive", name)
		}
	}
	c := &middleware.TenantConfig{MaxConcurrent: r.MaxConcurrent, MaxTracked: r.MaxTracked, IdleTTL: ttl, Limits: r.Limits, MissingPolicy: r.MissingPolicy, OverflowPolicy: r.OverflowPolicy, Resolver: resolver}
	if r.Sharing != nil {
		d := 500 * time.Millisecond
		if r.Sharing.DemandTTL != "" {
			d, e = time.ParseDuration(r.Sharing.DemandTTL)
			if e != nil || d <= 0 {
				return nil, fmt.Errorf("sharing demand_ttl must be positive")
			}
		}
		c.Sharing = &middleware.TenantSharingConfig{MaxConcurrent: r.Sharing.MaxConcurrent, Limits: r.Sharing.Limits, DemandTTL: d}
	}
	if _, e = middleware.NewService(middleware.ServiceConfig{MaxConcurrent: maximum, Tenants: c}); e != nil {
		return nil, fmt.Errorf("service %s tenants: %w", name, e)
	}
	return c, nil
}
