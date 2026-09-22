package algorithm

import (
	"testing"
	"time"

	"hazop-safeguard-coverage/backend/internal/model"
)

func TestProjectFailureWindowMarksEarliestRiskEscalation(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	// Two independent active layers. Combined protection is
	// 1-(1-0.6)*(1-0.4) = 0.76; residual risk of a 4*4=16 scenario is
	// ceil(16*0.24)=4 (low). Losing the 0.6 layer at day 10 drops protection
	// to 0.4 (below the 0.5 covered threshold): residual = ceil(16*0.6)=10
	// (medium) -> escalation with an uncovered gap path.
	node := model.ProcessNode{ID: 3, NodeCode: "T-3", Name: "Tower"}
	scenario := model.DeviationScenario{
		ID: 3, Guideword: "more", Parameter: "level",
		Cause: "level control fails", Consequence: "overflow",
		Likelihood: 4, Severity: 4, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 11, IndependenceKey: "SIS-A", Effectiveness: 0.6, TestIntervalDays: 10, LastVerifiedAt: &reference, LifecycleState: "active"},
		{ID: 12, IndependenceKey: "ALM-B", Effectiveness: 0.4, TestIntervalDays: 60, LastVerifiedAt: &reference, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 90)
	evaluator := NewEvaluator()
	result, err := evaluator.Evaluate(snapshot)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	forecast := result.FailureForecast
	if forecast.WindowDays != 90 {
		t.Fatalf("window days = %d, want 90", forecast.WindowDays)
	}
	if !forecast.EscalationWithin || forecast.FirstEscalation == nil {
		t.Fatalf("expected an escalation within 90 days, got %#v", forecast)
	}
	first := forecast.FirstEscalation
	if first.HorizonDays != 10 {
		t.Fatalf("first escalation horizon = %d, want 10", first.HorizonDays)
	}
	if first.RiskRankAfter != "medium" || !first.RiskRankIncreased {
		t.Fatalf("unexpected first escalation event: %#v", first)
	}
	if len(first.UncoveredPaths) != 1 || first.UncoveredPaths[0].Covered {
		t.Fatalf("expected an uncovered gap path at escalation: %#v", first.UncoveredPaths)
	}
	if len(forecast.Events) != 2 {
		t.Fatalf("expected two expiry events, got %d", len(forecast.Events))
	}
}

func TestProjectFailureWindowReportsNoneWhenNoEscalation(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	node := model.ProcessNode{ID: 4, NodeCode: "P-4", Name: "Pump"}
	scenario := model.DeviationScenario{
		ID: 4, Guideword: "no", Parameter: "flow",
		Cause: "pump trip", Consequence: "short stoppage",
		Likelihood: 2, Severity: 2, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 21, IndependenceKey: "PSV-C", Effectiveness: 0.9, TestIntervalDays: 20, LastVerifiedAt: &reference, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 30)
	result, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if result.FailureForecast.EscalationWithin || result.FailureForecast.FirstEscalation != nil {
		t.Fatalf("expected no escalation in window, got %#v", result.FailureForecast)
	}
	if len(result.FailureForecast.Events) != 1 {
		t.Fatalf("expected the expiry to still be projected, got %d events", len(result.FailureForecast.Events))
	}
}

func TestProjectFailureWindowExcludesExpiriesOutsideWindow(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	node := model.ProcessNode{ID: 5, NodeCode: "H-5", Name: "Heater"}
	scenario := model.DeviationScenario{
		ID: 5, Guideword: "more", Parameter: "temperature",
		Cause: "fuel surge", Consequence: "tube damage",
		Likelihood: 3, Severity: 3, ScenarioState: "draft", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 31, IndependenceKey: "SIS-D", Effectiveness: 0.7, TestIntervalDays: 100, LastVerifiedAt: &reference, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 30)
	result, err := NewEvaluator().Evaluate(snapshot)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(result.FailureForecast.Events) != 0 || result.FailureForecast.EscalationWithin {
		t.Fatalf("expiry outside window must not create an event: %#v", result.FailureForecast)
	}
}

func TestFailureWindowIsPartOfSnapshotHashAndIdempotent(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	node := model.ProcessNode{ID: 6, NodeCode: "D-6", Name: "Drum"}
	scenario := model.DeviationScenario{
		ID: 6, Guideword: "less", Parameter: "pressure",
		Cause: "leak", Consequence: "ingress",
		Likelihood: 3, Severity: 3, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 41, IndependenceKey: "SIS-E", Effectiveness: 0.6, TestIntervalDays: 45, LastVerifiedAt: &reference, LifecycleState: "active"},
	}
	evaluator := NewEvaluator()
	first, err := evaluator.Evaluate(NewSnapshot(node, scenario, safeguards, reference, 30))
	if err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	again, err := evaluator.Evaluate(NewSnapshot(node, scenario, safeguards, reference, 30))
	if err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
	if first.InputHash != again.InputHash || first.FailureForecastJSON != again.FailureForecastJSON {
		t.Fatal("identical window runs must reproduce identical hash and forecast")
	}
	longer, err := evaluator.Evaluate(NewSnapshot(node, scenario, safeguards, reference, 90))
	if err != nil {
		t.Fatalf("longer-window evaluate: %v", err)
	}
	if longer.InputHash == first.InputHash {
		t.Fatal("changing the failure window must change the frozen input hash")
	}
	if len(longer.FailureForecast.Events) != 1 || len(first.FailureForecast.Events) != 0 {
		t.Fatalf("the day-45 expiry should only appear in the 90-day window: first=%d longer=%d",
			len(first.FailureForecast.Events), len(longer.FailureForecast.Events))
	}
	passed, _, err := evaluator.Replay(first.SnapshotJSON, first.InputHash, first.CoverageScore, first.FailureForecastJSON)
	if err != nil || !passed {
		t.Fatalf("replay must restore the frozen forecast: passed=%t err=%v", passed, err)
	}
}

func TestNormalizeWindowDays(t *testing.T) {
	t.Parallel()
	cases := map[int]int{0: DefaultFailureWindowDays, -3: DefaultFailureWindowDays, 1: 1, 365: 365, 366: 365, 1000: 365}
	for input, want := range cases {
		if got := NormalizeWindowDays(input); got != want {
			t.Fatalf("NormalizeWindowDays(%d) = %d, want %d", input, got, want)
		}
	}
}
