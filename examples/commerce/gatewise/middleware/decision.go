package middleware

import (
	"github.com/HN026/gatewise/decision"
)

// DecisionState contains the last completed admission decision and bounded
// proposal counters. A limit proposal does not imply this request would reject.
type DecisionState struct {
	Enabled   bool
	Mode      decision.Mode
	Last      decision.Result
	Proposals [3]uint64 // allow, limit, reject; includes only evaluated signals.
	Budgets   []decision.BudgetSnapshot
	Signals   []decision.SignalSnapshot
}

func (s *Service) recordDecision(r decision.Result) {
	s.statistics.mu.Lock()
	defer s.statistics.mu.Unlock()
	s.statistics.lastDecision = r.Clone()
	if r.Evaluation.Mode != "" {
		i := 0
		if r.Evaluation.Action == "limit" {
			i = 1
		}
		if r.Evaluation.Action == "reject" {
			i = 2
		}
		s.statistics.proposals[i]++
	}
}
func (s *Service) DecisionSnapshot() DecisionState {
	if s.decisions == nil {
		return DecisionState{}
	}
	signals := s.decisions.Snapshot()
	budgets := s.decisions.BudgetSnapshot()
	s.statistics.mu.Lock()
	defer s.statistics.mu.Unlock()
	return DecisionState{Budgets: budgets, Enabled: true, Mode: s.decisions.Mode(), Last: s.statistics.lastDecision.Clone(), Proposals: s.statistics.proposals, Signals: signals}
}
