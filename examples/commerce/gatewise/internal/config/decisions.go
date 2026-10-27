package config

import (
	"fmt"
	"github.com/HN026/gatewise/decision"
	"strings"
	"time"
)

type rawDecisions struct {
	Mode    decision.Mode `json:"mode"`
	Signals []struct {
		Name         string          `json:"name"`
		Scope        decision.Scope  `json:"scope"`
		Threshold    float64         `json:"threshold"`
		MaxAge       string          `json:"max_age"`
		OnUnknown    string          `json:"on_unknown"`
		UnknownLimit int             `json:"unknown_limit"`
		Pressure     decision.Effect `json:"pressure"`
		Source       *struct {
			URL      string `json:"url"`
			Interval string `json:"interval"`
			Timeout  string `json:"timeout"`
		} `json:"source"`
	} `json:"signals"`
}

// Parsing never starts collectors or accesses the network. check-config remains offline.
func parseDecisions(service string, raw *rawDecisions, lookup func(string) (string, bool), tenants bool) (*decision.Engine, []decision.Collector, error) {
	if raw == nil {
		return nil, nil, nil
	}
	mode := raw.Mode
	prefix := "GATEWISE_SERVICE_" + strings.ToUpper(service) + "_DECISION_"
	if v, ok := lookup(prefix + "MODE"); ok {
		mode = decision.Mode(v)
	}
	config := decision.Config{Mode: mode}
	var collectors []decision.Collector
	for _, s := range raw.Signals {
		age, err := time.ParseDuration(s.MaxAge)
		if err != nil {
			return nil, nil, fmt.Errorf("service %s: invalid signal max_age", service)
		}
		config.Rules = append(config.Rules, decision.Rule{Name: s.Name, Scope: s.Scope, Threshold: s.Threshold, MaxAge: age, OnUnknown: s.OnUnknown, UnknownLimit: s.UnknownLimit, Pressure: s.Pressure})
	}
	engine, err := decision.New(config)
	if err != nil {
		return nil, nil, fmt.Errorf("service %s: %w", service, err)
	}
	if engine.HasActorRules() && !tenants {
		return nil, nil, fmt.Errorf("service %s: actor rules require tenants", service)
	}
	for _, s := range raw.Signals {
		if s.Source == nil {
			return nil, nil, fmt.Errorf("service %s: proxy signals require a source", service)
		}
		address := s.Source.URL
		if v, ok := lookup(prefix + strings.ToUpper(s.Name) + "_URL"); ok {
			address = v
		}
		u, err := resolveURL("signal source", address, lookup)
		if err != nil {
			return nil, nil, err
		}
		interval, err := time.ParseDuration(s.Source.Interval)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid signal interval")
		}
		timeout, err := time.ParseDuration(s.Source.Timeout)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid signal timeout")
		}
		provider, err := decision.NewHTTPProvider(u.String())
		if err != nil {
			return nil, nil, err
		}
		// No connections exist at this point; no cleanup is necessary on parse failure.
		collectors = append(collectors, decision.Collector{Name: s.Name, Interval: interval, Timeout: timeout, Provider: provider})
	}
	if err := engine.ValidateCollectors(collectors); err != nil {
		return nil, nil, err
	}
	return engine, collectors, nil
}
