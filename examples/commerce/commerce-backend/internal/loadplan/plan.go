// Package loadplan makes offered traffic independent of policy and goroutine scheduling.
package loadplan

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/rand"
	"sort"
)

type Arrival struct {
	Seconds  float64 `json:"seconds"`
	Role     string  `json:"role"`
	Critical bool    `json:"critical"`
}

// New preserves the legacy periodic plan when seed is zero. A nonzero seed
// selects sorted uniform arrival times (Poisson arrivals conditioned on count)
// with independently shuffled exact service and priority totals.
func New(count int, seconds, startRPS, endRPS float64, businessSlots int, seed int64) []Arrival {
	out := make([]Arrival, count)
	roles := []string{"orders", "catalog", "orders", "inventory", "orders"}
	for i := range out {
		due := float64(i) / startRPS
		slope := (endRPS - startRPS) / seconds
		if math.Abs(slope) > 1e-9 {
			due = 2 * float64(i) / (startRPS + math.Sqrt(startRPS*startRPS+2*slope*float64(i)))
		}
		out[i] = Arrival{due, roles[(i/5)%5], i%5 < businessSlots}
	}
	if seed == 0 {
		return out
	}
	rng := rand.New(rand.NewSource(seed))
	times := make([]float64, count)
	for i := range times {
		times[i] = rng.Float64() * seconds
	}
	sort.Float64s(times)
	// Quantile inversion also supports a linearly varying offered rate.
	for i, u := range times {
		mass := u / seconds * float64(count)
		slope := (endRPS - startRPS) / seconds
		due := mass / startRPS
		if math.Abs(slope) > 1e-9 {
			due = 2 * mass / (startRPS + math.Sqrt(startRPS*startRPS+2*slope*mass))
		}
		out[i].Seconds = due
	}
	service := make([]string, count)
	priority := make([]bool, count)
	for i := range out {
		service[i] = roles[i%5]
		priority[i] = out[i].Critical
	}
	rng.Shuffle(count, func(i, j int) { service[i], service[j] = service[j], service[i] })
	rng.Shuffle(count, func(i, j int) { priority[i], priority[j] = priority[j], priority[i] })
	for i := range out {
		out[i].Role = service[i]
		out[i].Critical = priority[i]
	}
	return out
}
func Digest(plan []Arrival) string {
	data, _ := json.Marshal(plan)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
