package middleware

import (
	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/operation"
	"time"
)

type AdaptiveConfig = operation.AdaptiveConfig
type ServiceState = operation.ServiceState
type ControllerState = operation.ControllerState
type handlerOptions struct {
	targetLatency time.Duration
	resource      string
}

// WithResource selects a server-controlled resource scope, never a caller header.
// Scoped ceilings still compare the shared Service in-flight count.
func WithResource(name string) HandlerOption {
	if !decision.ValidName(name) {
		panic("gatewise: invalid resource name")
	}
	return func(o *handlerOptions) { o.resource = name }
}

type HandlerOption func(*handlerOptions)

// WithLatencyTarget gives this handler its own budget so naturally slower work
// is not compared directly to fast handlers sharing the same service capacity.
func WithLatencyTarget(target time.Duration) HandlerOption {
	if target <= 0 {
		panic("gatewise: latency target must be positive")
	}
	return func(o *handlerOptions) { o.targetLatency = target }
}
