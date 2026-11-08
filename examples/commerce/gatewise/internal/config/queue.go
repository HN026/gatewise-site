package config

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/HN026/gatewise/middleware"
	"github.com/HN026/gatewise/operation"
	"github.com/HN026/gatewise/priority"
)

type rawQueue struct {
	MaxBodyBytes int64                      `json:"max_body_bytes"`
	MaxWaiting   int                        `json:"max_waiting"`
	MaxWait      string                     `json:"max_wait"`
	AgingAfter   string                     `json:"aging_after"`
	Reserved     map[operation.Priority]int `json:"reserved"`
}

func parseQueue(name string, raw *rawQueue, lookup func(string) (string, bool), maximum, reserved int, priorities *priority.Config) (*middleware.QueueConfig, error) {
	prefix := "GATEWISE_SERVICE_" + strings.ToUpper(name) + "_QUEUE_"
	if value, ok := lookup(prefix + "ENABLED"); ok {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return nil, fmt.Errorf("service %s queue enabled must be boolean", name)
		}
		if !enabled {
			return nil, nil
		}
		if raw == nil {
			raw = &rawQueue{}
		}
	}
	for _, key := range []string{"MAX_WAITING", "MAX_WAIT", "AGING_AFTER", "RESERVED"} {
		if _, ok := lookup(prefix + key); ok && raw == nil {
			raw = &rawQueue{}
		}
	}
	if raw == nil {
		return nil, nil
	}
	copy := *raw
	raw = &copy
	if value, ok := lookup(prefix + "MAX_WAITING"); ok {
		n, err := strconv.Atoi(value)
		if err != nil {
			return nil, fmt.Errorf("service %s queue max_waiting must be integer", name)
		}
		raw.MaxWaiting = n
	}
	for _, f := range []struct {
		key string
		dst *string
	}{{"MAX_WAIT", &raw.MaxWait}, {"AGING_AFTER", &raw.AgingAfter}} {
		if v, ok := lookup(prefix + f.key); ok {
			if v == "" {
				return nil, fmt.Errorf("service %s queue duration must not be empty", name)
			}
			*f.dst = v
		}
	}
	if v, ok := lookup(prefix + "RESERVED"); ok {
		if err := json.Unmarshal([]byte(v), &raw.Reserved); err != nil {
			return nil, fmt.Errorf("service %s queue reserved must be a JSON object", name)
		}
	}
	q := &middleware.QueueConfig{MaxWaiting: raw.MaxWaiting, Reserved: raw.Reserved}
	var err error
	q.MaxWait, err = time.ParseDuration(raw.MaxWait)
	if err != nil {
		return nil, fmt.Errorf("service %s queue max_wait must be a positive duration", name)
	}
	if raw.AgingAfter != "" {
		q.AgingAfter, err = time.ParseDuration(raw.AgingAfter)
		if err != nil {
			return nil, fmt.Errorf("service %s queue aging_after must be a positive duration", name)
		}
	}
	if _, err = operation.New(operation.Config{MaxConcurrent: maximum, ReservedCritical: reserved, Priorities: priorities, Queue: q}); err != nil {
		return nil, fmt.Errorf("service %s: %w", name, err)
	}
	return q, nil
}

func parseQueueBody(name string, raw *rawQueue, lookup func(string) (string, bool), enabled bool) (int64, error) {
	if !enabled {
		return 0, nil
	}
	var n int64
	if raw != nil {
		n = raw.MaxBodyBytes
	}
	if value, ok := lookup("GATEWISE_SERVICE_" + strings.ToUpper(name) + "_QUEUE_MAX_BODY_BYTES"); ok {
		var err error
		n, err = strconv.ParseInt(value, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("queue max_body_bytes must be integer")
		}
	}
	if n < -1 || n > 1<<20 {
		return 0, fmt.Errorf("queue max_body_bytes must be -1 or 0..1048576")
	}
	return n, nil
}
