package metrics

import (
	"github.com/HN026/gatewise/operation"
	"github.com/prometheus/client_golang/prometheus"
)

type queueDescriptors struct{ queueConfig, queueMax, queueHighWater, queueDepth, queueEvents, queueDuration, queueReserved, queueMaxWait, queueAging *prometheus.Desc }

func newQueueDescriptors(desc func(string, string, ...string) *prometheus.Desc) queueDescriptors {
	return queueDescriptors{
		queueReserved: desc("queue_reserved_slots", "Waiting capacity protected from lower tiers.", "priority"), queueMaxWait: desc("queue_max_wait_seconds", "Maximum queue waiting time; caller deadline may be earlier."), queueAging: desc("queue_aging_after_seconds", "Age at which FIFO selection precedes unaged priority order."),
		queueConfig: desc("queue_info", "Enabled bounded priority queue."), queueMax: desc("queue_capacity", "Maximum waiting requests; excludes executing work."), queueHighWater: desc("queue_high_watermark", "Largest observed total waiting count."), queueDepth: desc("queue_waiting", "Currently waiting requests; holds no execution permit.", "priority"), queueEvents: desc("queue_events_total", "Queue arrivals and terminal outcomes, including full/reserved refusal outside the queue.", "priority", "event"), queueDuration: desc("queue_wait_duration_seconds", "Time spent by requests that exited the queue, including admitted, expired, canceled and policy-rejected work.", "priority"),
	}
}
func (c *collector) describeQueue(out chan<- *prometheus.Desc) {
	for _, d := range []*prometheus.Desc{c.queueReserved, c.queueMaxWait, c.queueAging, c.queueConfig, c.queueMax, c.queueHighWater, c.queueDepth, c.queueEvents, c.queueDuration} {
		out <- d
	}
}
func (c *collector) collectQueue(out chan<- prometheus.Metric, s operation.Statistics) {
	q := s.Queue
	if !q.Enabled {
		return
	}
	gauge := func(d *prometheus.Desc, n float64, labels ...string) {
		out <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, n, labels...)
	}
	gauge(c.queueConfig, 1)
	gauge(c.queueMaxWait, q.MaxWait.Seconds())
	gauge(c.queueAging, q.AgingAfter.Seconds())
	gauge(c.queueMax, float64(q.MaxWaiting))
	gauge(c.queueHighWater, float64(q.HighWater))
	for _, p := range q.Tiers {
		label := string(p.Priority)
		gauge(c.queueReserved, float64(q.Reservations[p.Priority]), label)
		gauge(c.queueDepth, float64(p.Waiting), label)
		counts := []uint64{p.Enqueued, p.Admitted, p.Full, p.Reserved, p.Timeout, p.Deadline, p.Canceled, p.PolicyRejected}
		for i, event := range []string{"enqueued", "admitted", "full", "reserved", "timeout", "deadline", "canceled", "policy_rejected"} {
			out <- prometheus.MustNewConstMetric(c.queueEvents, prometheus.CounterValue, float64(counts[i]), label, event)
		}
		buckets := map[float64]uint64{}
		for i, b := range s.DurationBounds {
			buckets[b] = p.WaitBuckets[i]
		}
		out <- prometheus.MustNewConstHistogram(c.queueDuration, p.WaitCount, p.WaitSum, buckets, label)
	}
}
