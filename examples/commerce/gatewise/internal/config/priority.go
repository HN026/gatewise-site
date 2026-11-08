package config

import (
	"fmt"
	"github.com/HN026/gatewise/priority"
	"strings"
	"time"
)

type rawPriority struct {
	Tiers          []priority.Tier `json:"tiers"`
	CriticalTier   string          `json:"critical_tier"`
	BackgroundTier string          `json:"background_tier"`
	TrustedProxies []string        `json:"trusted_proxies"`
	Cohorts        *struct {
		Buckets  int    `json:"buckets"`
		Rotation string `json:"rotation"`
		Seed     string `json:"seed"`
	} `json:"cohorts"`
}

func parsePriority(name string, r *rawPriority, lookup func(string) (string, bool), maximum, reserved int) (*priority.Config, error) {
	if r == nil {
		return nil, nil
	}
	if reserved != 0 {
		return nil, fmt.Errorf("service %s: use tier reservations with priorities", name)
	}
	c := &priority.Config{Tiers: r.Tiers, CriticalTier: r.CriticalTier, BackgroundTier: r.BackgroundTier, TrustedProxies: r.TrustedProxies}
	prefix := "GATEWISE_SERVICE_" + strings.ToUpper(name) + "_PRIORITY_"
	if v, ok := lookup(prefix + "TRUSTED_PROXIES"); ok {
		c.TrustedProxies = nil
		if v != "" {
			for _, cidr := range strings.Split(v, ",") {
				c.TrustedProxies = append(c.TrustedProxies, strings.TrimSpace(cidr))
			}
		}
	}
	if r.Cohorts != nil {
		rotation, e := time.ParseDuration(r.Cohorts.Rotation)
		if e != nil {
			return nil, fmt.Errorf("priority cohort rotation must be a duration")
		}
		seed := r.Cohorts.Seed
		if v, ok := lookup(prefix + "COHORT_SEED"); ok {
			seed = v
		}
		c.Cohorts = &priority.Cohorts{Buckets: r.Cohorts.Buckets, Rotation: rotation, Seed: seed}
	}
	if _, e := priority.New(*c, maximum); e != nil {
		return nil, fmt.Errorf("service %s: %w", name, e)
	}
	return c, nil
}
