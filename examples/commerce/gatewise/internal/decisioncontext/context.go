// Package decisioncontext owns the private context key shared by adapters.
package decisioncontext

import "context"

type key struct{}

// WithOwned stores an internally owned immutable result. Public entry points
// must copy mutable slices before calling this; public reads always detach them.
func WithOwned(ctx context.Context, result any) context.Context {
	return context.WithValue(ctx, key{}, result)
}
func Value(ctx context.Context) any { return ctx.Value(key{}) }
