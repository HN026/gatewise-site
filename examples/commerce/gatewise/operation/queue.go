package operation

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/HN026/gatewise/decision"
	"github.com/HN026/gatewise/internal/admission"
)

// QueueConfig enables process-local waiting only for local execution-capacity
// refusals. Other policy refusals stay immediate. Limits count requests, not bytes.
// Reserved protects higher tiers' waiting slots from combined lower-tier occupancy.
// AgingAfter changes selection order only; it never overrides execution policy.
type QueueConfig struct {
	MaxWaiting int
	MaxWait    time.Duration
	AgingAfter time.Duration // zero defaults to half MaxWait (at least 1ns).
	Reserved   map[Priority]int
}

type QueuePriorityState struct {
	Priority                                                                        Priority
	Waiting                                                                         int
	Enqueued, Admitted, Full, Reserved, Timeout, Deadline, Canceled, PolicyRejected uint64
	WaitCount                                                                       uint64
	WaitSum                                                                         float64
	WaitBuckets                                                                     [11]uint64
}
type QueueState struct {
	Enabled                        bool
	MaxWaiting, Waiting, HighWater int
	MaxWait, AgingAfter            time.Duration
	Tiers                          []QueuePriorityState
	Reservations                   map[Priority]int
}
type waiter struct {
	p               *prepared
	since, deadline time.Time
	callerDeadline  bool
	ready           chan struct{}
	element         *list.Element
}
type waitingQueue struct {
	mu                     sync.Mutex
	config                 QueueConfig
	queues                 []list.List
	blocked                []uint64
	depth                  int
	tiers                  []QueuePriorityState
	ranks                  map[Priority]int
	reserved               []int
	highWater              int
	epoch, timerGeneration uint64
	timer                  *time.Timer
}

func newWaitingQueue(c *QueueConfig, s *Engine) (*waitingQueue, error) {
	if c == nil {
		return nil, nil
	}
	if c.MaxWaiting < 1 || c.MaxWaiting > 65536 || c.MaxWait <= 0 {
		return nil, fmt.Errorf("gatewise: queue needs MaxWaiting in 1..65536 and positive MaxWait")
	}
	cfg := *c
	if cfg.AgingAfter == 0 {
		cfg.AgingAfter = cfg.MaxWait / 2
		if cfg.AgingAfter == 0 {
			cfg.AgingAfter = 1
		}
	}
	if cfg.AgingAfter < 0 || cfg.AgingAfter > cfg.MaxWait {
		return nil, fmt.Errorf("gatewise: queue AgingAfter must be positive and not exceed MaxWait")
	}
	q := &waitingQueue{config: cfg, ranks: map[Priority]int{}, epoch: 1}
	names := []Priority{Critical, Background}
	if s.priorities != nil {
		names = nil
		for _, t := range s.priorities.Tiers() {
			names = append(names, Priority(t.Name))
		}
	}
	q.reserved = make([]int, len(names))
	q.queues = make([]list.List, len(names))
	q.blocked = make([]uint64, len(names))
	for i, p := range names {
		q.ranks[p] = i
		q.tiers = append(q.tiers, QueuePriorityState{Priority: p})
	}
	total := 0
	cfg.Reserved = map[Priority]int{}
	for p, n := range c.Reserved {
		i, ok := q.ranks[p]
		if !ok || n < 0 || n > c.MaxWaiting-total {
			return nil, fmt.Errorf("gatewise: queue reservations must name configured tiers and total at most MaxWaiting")
		}
		total += n
		q.reserved[i] = n
		cfg.Reserved[p] = n
	}
	q.config = cfg
	return q, nil
}

func (s *Engine) QueueEnabled() bool { return s.queue != nil }

func (s *Engine) QueueSnapshot() QueueState {
	if s.queue == nil {
		return QueueState{}
	}
	q := s.queue
	q.mu.Lock()
	defer q.mu.Unlock()
	reservations := map[Priority]int{}
	for p, n := range q.config.Reserved {
		reservations[p] = n
	}
	return QueueState{Enabled: true, MaxWaiting: q.config.MaxWaiting, MaxWait: q.config.MaxWait, AgingAfter: q.config.AgingAfter, Waiting: q.depth, HighWater: q.highWater, Tiers: append([]QueuePriorityState(nil), q.tiers...), Reservations: reservations}
}
func (q *waitingQueue) pulse(advance bool) {
	if advance {
		q.epoch++
	}
	if w := q.best(time.Now()); w != nil {
		select {
		case w.ready <- struct{}{}:
		default:
		}
	}
}
func (q *waitingQueue) wake() {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.depth > 0 {
		q.pulse(true)
	}
}

// A single short-lived retry timer also observes independently changing cached
// signals/limits. No permanent dispatcher or goroutine survives an empty queue.
func (q *waitingQueue) arm() {
	if q.timer != nil || q.depth == 0 {
		return
	}
	q.timerGeneration++
	generation := q.timerGeneration
	q.timer = time.AfterFunc(10*time.Millisecond, func() {
		q.mu.Lock()
		defer q.mu.Unlock()
		if generation != q.timerGeneration {
			return
		}
		q.timer = nil
		if q.depth > 0 {
			q.pulse(true)
			q.arm()
		}
	})
}
func (q *waitingQueue) remove(w *waiter, outcome string) {
	q.queues[q.ranks[w.p.request.Priority]].Remove(w.element)
	q.depth--
	v := &q.tiers[q.ranks[w.p.request.Priority]]
	v.Waiting--
	elapsed := time.Since(w.since).Seconds()
	v.WaitCount++
	v.WaitSum += elapsed
	for i, b := range durationBounds {
		if elapsed <= b {
			v.WaitBuckets[i]++
		}
	}
	switch outcome {
	case "admitted":
		v.Admitted++
	case "queue_timeout":
		v.Timeout++
	case "queue_deadline":
		v.Deadline++
	case "queue_canceled":
		v.Canceled++
	case "policy_rejected":
		v.PolicyRejected++
	}
	if q.depth == 0 && q.timer != nil {
		q.timer.Stop()
		q.timer = nil
		q.timerGeneration++
	}
	q.pulse(false)
}

// Each tier is FIFO. Selection examines only its oldest live entry, so a full
// queue does not broadcast to every caller or scan the entire queue on each wake.
func (q *waitingQueue) head(i int, now time.Time) *waiter {
	for e := q.queues[i].Front(); e != nil; e = e.Next() {
		w := e.Value.(*waiter)
		if w.p.ctx.Err() == nil && now.Before(w.deadline) {
			return w
		}
	}
	return nil
}
func (q *waitingQueue) best(now time.Time) *waiter {
	var best *waiter
	for i := range q.queues {
		if q.blocked[i] == q.epoch {
			continue
		}
		w := q.head(i, now)
		if w == nil {
			continue
		}
		if best == nil {
			best = w
			continue
		}
		aged, oldAged := now.Sub(w.since) >= q.config.AgingAfter, now.Sub(best.since) >= q.config.AgingAfter
		if aged && !oldAged || aged && oldAged && w.since.Before(best.since) {
			best = w
		}
	}
	return best
}
func (q *waitingQueue) slotReason(p Priority) string {
	if q.depth >= q.config.MaxWaiting {
		return "queue_full"
	}
	reserved := 0
	for boundary := 1; boundary <= q.ranks[p]; boundary++ {
		reserved += q.reserved[boundary-1]
		lower := 0
		for _, v := range q.tiers[boundary:] {
			lower += v.Waiting
		}
		if lower >= q.config.MaxWaiting-reserved {
			return "queue_reserved"
		}
	}
	return ""
}
func retryCapacity(err error) bool {
	var r *Rejection
	if !errors.As(err, &r) {
		return false
	}
	if e := r.Decision.Evaluation; e.Mode == decision.Enforce && e.SignalCeiling > 0 && e.SignalCeiling < e.BaselineLimit {
		return false
	}
	return r.Reason == "concurrency_limit" || r.Reason == "critical_capacity_reserved" || r.Reason == "tier_capacity_reserved"
}
func (s *Engine) queueFailure(p *prepared, reason string, previous error) error {
	trace := decision.NewResult(string(p.request.Priority), p.request.Resource)
	trace.Kind = p.request.Kind
	var last *Rejection
	if errors.As(previous, &last) {
		trace = last.Decision.Clone()
	}
	trace.Allowed = false
	trace.Stage = "capacity"
	trace.Reason = reason
	trace.Mark("capacity", reason)
	s.recordAdmissionFailure(p.request, trace, admission.Decision(reason))
	return &Rejection{Reason: reason, Decision: trace}
}
func (s *Engine) finalAttemptFailure(p *prepared, err error) error {
	var r *Rejection
	if errors.As(err, &r) {
		s.recordAdmissionFailure(p.request, r.Decision, admission.Decision(r.Reason))
	}
	return err
}
func (s *Engine) wait(p *prepared) (*Permit, error) {
	q := s.queue
	q.mu.Lock()
	if err := p.ctx.Err(); err != nil {
		q.mu.Unlock()
		return nil, err
	}
	now := time.Now()
	best := q.best(now)
	// Fresh higher-priority work can use free capacity ahead of unaged lower tiers.
	// Equal-priority arrivals never overtake an eligible queued predecessor.
	direct := q.depth == 0 || best == nil || now.Sub(best.since) < q.config.AgingAfter && q.ranks[p.request.Priority] < q.ranks[best.p.request.Priority]
	// A stale blocked marker never lets a new equal/lower tier steal an older slot.
	for i := 0; i <= q.ranks[p.request.Priority]; i++ {
		if q.head(i, now) != nil {
			direct = false
			break
		}
	}
	permit, err := s.attempt(p, false, direct)
	if err == nil {
		q.mu.Unlock()
		return permit, nil
	}
	if !retryCapacity(err) {
		q.mu.Unlock()
		return nil, s.finalAttemptFailure(p, err)
	}
	var last error = err
	if p.request.NoWait {
		q.mu.Unlock()
		return nil, s.finalAttemptFailure(p, last)
	}
	if reason := q.slotReason(p.request.Priority); reason != "" {
		v := &q.tiers[q.ranks[p.request.Priority]]
		if reason == "queue_full" {
			v.Full++
		} else {
			v.Reserved++
		}
		q.mu.Unlock()
		return nil, s.queueFailure(p, reason, last)
	}
	now = time.Now()
	w := &waiter{p: p, since: now, deadline: now.Add(q.config.MaxWait), ready: make(chan struct{}, 1)}
	if d, ok := p.ctx.Deadline(); ok && !d.After(w.deadline) {
		w.deadline = d
		w.callerDeadline = true
	}
	w.element = q.queues[q.ranks[p.request.Priority]].PushBack(w)
	q.depth++
	v := &q.tiers[q.ranks[p.request.Priority]]
	v.Waiting++
	v.Enqueued++
	if q.depth > q.highWater {
		q.highWater = q.depth
	}
	q.arm()
	q.pulse(false)
	timer := time.NewTimer(time.Until(w.deadline))
	defer timer.Stop()
	for {
		now = time.Now()
		err := p.ctx.Err()
		reason := ""
		if errors.Is(err, context.DeadlineExceeded) {
			reason = "queue_deadline"
		} else if err != nil {
			reason = "queue_canceled"
		} else if !now.Before(w.deadline) {
			if w.callerDeadline {
				reason = "queue_deadline"
				err = context.DeadlineExceeded
			} else {
				reason = "queue_timeout"
			}
		}
		if reason != "" {
			q.remove(w, reason)
			q.mu.Unlock()
			rejection := s.queueFailure(p, reason, last)
			if err != nil {
				return nil, err
			}
			return nil, rejection
		}
		if q.best(now) == w {
			permit, err := s.admit(p, false)
			if err == nil {
				wait := time.Since(w.since)
				q.remove(w, "admitted")
				permit.queueWait = wait
				q.mu.Unlock()
				return permit, nil
			}
			if !retryCapacity(err) {
				outcome := "policy_rejected"
				if errors.Is(err, context.Canceled) {
					outcome = "queue_canceled"
				} else if errors.Is(err, context.DeadlineExceeded) {
					outcome = "queue_deadline"
				}
				q.remove(w, outcome)
				q.mu.Unlock()
				if outcome != "policy_rejected" {
					s.queueFailure(p, outcome, last)
					return nil, err
				}
				return nil, s.finalAttemptFailure(p, err)
			}
			last = err
			q.blocked[q.ranks[p.request.Priority]] = q.epoch
			q.pulse(false)
		}
		q.mu.Unlock()
		select {
		case <-w.ready:
		case <-p.ctx.Done():
		case <-timer.C:
		}
		q.mu.Lock()
	}
}
