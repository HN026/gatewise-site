package decision

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Provider interface {
	Sample(context.Context) (Observation, error)
}
type ProviderFunc func(context.Context) (Observation, error)

func (f ProviderFunc) Sample(ctx context.Context) (Observation, error) { return f(ctx) }

type Collector struct {
	Name              string
	Interval, Timeout time.Duration
	Provider          Provider
}

func (e *Engine) ValidateCollectors(collectors []Collector) error {
	seen := map[string]bool{}
	for _, c := range collectors {
		i, ok := e.indices[c.Name]
		if !ok || seen[c.Name] || c.Provider == nil {
			return fmt.Errorf("decision: collectors need unique configured signals and a provider")
		}
		seen[c.Name] = true
		if c.Interval < 10*time.Millisecond || c.Interval > time.Hour || c.Timeout <= 0 || c.Timeout > c.Interval || e.slots[i].rule.MaxAge < c.Interval+c.Timeout {
			return fmt.Errorf("decision: invalid collector interval/timeout; max age must cover both")
		}
	}
	return nil
}

// Run owns background sampling until cancellation. At most one sample per signal
// can be in flight, including across Run restarts. A provider that ignores its
// context can leave one worker blocked; it cannot create unbounded replacement workers.
func (e *Engine) Run(ctx context.Context, collectors []Collector) error {
	if err := e.ValidateCollectors(collectors); err != nil {
		return err
	}
	e.mu.Lock()
	if e.running {
		e.mu.Unlock()
		return fmt.Errorf("decision: collectors already running")
	}
	e.running = true
	e.mu.Unlock()
	defer func() { e.mu.Lock(); e.running = false; e.mu.Unlock() }()
	var wg sync.WaitGroup
	for _, c := range collectors {
		wg.Add(1)
		go func(c Collector) { defer wg.Done(); e.collect(ctx, c) }(c)
	}
	<-ctx.Done()
	wg.Wait()
	return nil
}
func (e *Engine) failure(name string, kind int) {
	e.mu.Lock()
	e.slots[e.indices[name]].failures[kind]++
	e.mu.Unlock()
	_ = e.Unavailable(name)
}
func (e *Engine) collect(ctx context.Context, c Collector) {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		s := &e.slots[e.indices[c.Name]]
		busy := s.collecting
		if !busy {
			s.collecting = true
		}
		e.mu.Unlock()
		if busy {
			e.failure(c.Name, 3)
			timer.Reset(c.Interval)
			continue
		}
		call, cancel := context.WithTimeout(ctx, c.Timeout)
		type outcome struct {
			o        Observation
			err      error
			panicked bool
		}
		results := make(chan outcome, 1)
		go func() {
			v := outcome{}
			func() {
				defer func() {
					if recover() != nil {
						v.panicked = true
					}
				}()
				v.o, v.err = c.Provider.Sample(call)
			}()
			e.mu.Lock()
			e.slots[e.indices[c.Name]].collecting = false
			e.mu.Unlock()
			results <- v
		}()
		select {
		case <-ctx.Done():
			cancel()
			return
		case <-call.Done():
			if ctx.Err() != nil {
				cancel()
				return
			}
			e.failure(c.Name, 1)
		case v := <-results:
			if call.Err() != nil {
				e.failure(c.Name, 1)
			} else if v.panicked {
				e.failure(c.Name, 2)
			} else if v.err != nil {
				e.failure(c.Name, 0)
			} else {
				_ = e.Publish(c.Name, v.o)
			}
		}
		cancel()
		timer.Reset(c.Interval)
	}
}
