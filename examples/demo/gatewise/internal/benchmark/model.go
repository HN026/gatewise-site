package benchmark

import "time"

type Config struct {
	Backend        string        `json:"backend_binary"`
	Proxy          string        `json:"proxy_binary"`
	Output         string        `json:"output_directory"`
	Repeats        int           `json:"repeats"`
	Duration       time.Duration `json:"phase_duration_ns"`
	BaselineRate   int           `json:"baseline_rps"`
	PressureRate   int           `json:"pressure_rps"`
	MaxInFlight    int           `json:"generator_max_in_flight"`
	MaxDropPercent float64       `json:"max_dropped_schedule_percent"`
}

type Latency struct {
	Count int     `json:"count"`
	Mean  float64 `json:"mean_ms"`
	P50   float64 `json:"p50_ms"`
	P95   float64 `json:"p95_ms"`
	P99   float64 `json:"p99_ms"`
}

type Traffic struct {
	Scheduled           int     `json:"scheduled"`
	Sent                int     `json:"sent"`
	Dropped             int     `json:"dropped_schedules"`
	OK                  int     `json:"successful_2xx"`
	Rejected            int     `json:"rejected_503"`
	Errors              int     `json:"transport_or_unexpected_status_errors"`
	DeadlineMisses      int     `json:"successful_over_100ms"`
	SuccessLatency      Latency `json:"success_latency"`
	RejectionLatency    Latency `json:"rejection_latency"`
	ArrivalLatency      Latency `json:"scheduled_arrival_to_completion"`
	DispatchLag         Latency `json:"dispatch_lag"`
	Goodput             float64 `json:"successful_per_second"`
	WithinBudgetGoodput float64 `json:"successful_within_100ms_per_second"`
	OfferedGoodput      float64 `json:"successful_per_offered_second"`
	LimitMin            int     `json:"observed_header_limit_min"`
	LimitMax            int     `json:"observed_header_limit_max"`
}

type Phase struct {
	Name              string             `json:"name"`
	Capacity          int                `json:"backend_capacity"`
	Rate              int                `json:"offered_rps"`
	OfferedSeconds    float64            `json:"offered_seconds"`
	ElapsedSeconds    float64            `json:"elapsed_including_drain_seconds"`
	Traffic           map[string]Traffic `json:"traffic"`
	EndLimit          float64            `json:"end_limit"`
	Decreases         float64            `json:"cumulative_limit_decreases"`
	Increases         float64            `json:"cumulative_limit_increases"`
	PeakBackendActive int                `json:"cumulative_peak_backend_active"`
}

type Resource struct {
	Role              string  `json:"role"`
	PID               int     `json:"pid"`
	CPUSeconds        float64 `json:"cpu_seconds_entire_child_lifetime"`
	ObservedPeakRSSKB int64   `json:"observed_peak_rss_kib"`
}

type Check struct {
	Name string `json:"name"`
	Pass bool   `json:"pass"`
}
type Result struct {
	Deployment string     `json:"deployment"`
	Repeat     int        `json:"repeat"`
	Phases     []Phase    `json:"phases"`
	Resources  []Resource `json:"resources"`
	Checks     []Check    `json:"checks"`
}

type Report struct {
	Started      time.Time         `json:"started_at"`
	Finished     time.Time         `json:"finished_at"`
	GoVersion    string            `json:"go_version"`
	OS           string            `json:"os"`
	Arch         string            `json:"arch"`
	CPUs         int               `json:"logical_cpus"`
	Hostname     string            `json:"hostname"`
	Config       Config            `json:"config"`
	BinarySHA256 map[string]string `json:"binary_sha256"`
	Methodology  []string          `json:"methodology"`
	Results      []Result          `json:"results"`
	Passed       bool              `json:"passed"`
}
