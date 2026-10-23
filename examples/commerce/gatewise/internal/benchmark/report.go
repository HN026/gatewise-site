package benchmark

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
)

func writeReport(dir string, report Report) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, "results.json.tmp")
	if err = os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	if err = os.Rename(tmp, filepath.Join(dir, "results.json")); err != nil {
		return err
	}
	f, err := os.Create(filepath.Join(dir, "summary.csv"))
	if err != nil {
		return err
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{"deployment", "repeat", "phase", "priority", "scheduled", "sent", "dropped", "ok", "rejected_503", "errors", "goodput_rps_including_drain", "success_p50_ms", "success_p95_ms", "success_p99_ms", "rejection_p95_ms", "dispatch_lag_p99_ms", "over_100ms_successes", "end_limit", "within_100ms_goodput_rps"})
	integer := strconv.Itoa
	number := func(v float64) string { return strconv.FormatFloat(v, 'f', 3, 64) }
	for _, r := range report.Results {
		for _, p := range r.Phases {
			for _, priority := range []string{"critical", "background"} {
				t := p.Traffic[priority]
				_ = w.Write([]string{r.Deployment, integer(r.Repeat), p.Name, priority, integer(t.Scheduled), integer(t.Sent), integer(t.Dropped), integer(t.OK), integer(t.Rejected), integer(t.Errors), number(t.Goodput), number(t.SuccessLatency.P50), number(t.SuccessLatency.P95), number(t.SuccessLatency.P99), number(t.RejectionLatency.P95), number(t.DispatchLag.P99), integer(t.DeadlineMisses), number(p.EndLimit), number(t.WithinBudgetGoodput)})
			}
		}
	}
	w.Flush()
	return w.Error()
}
