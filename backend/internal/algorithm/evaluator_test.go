package algorithm

import (
	"testing"
	"time"

	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/model"
)

func TestEvaluatorIsDeterministicAndDeduplicatesIndependenceKeys(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	valid := reference.AddDate(0, 0, -10)
	expired := reference.AddDate(0, 0, -400)
	node := model.ProcessNode{ID: 7, NodeCode: "R-7", Name: "Reactor"}
	scenario := model.DeviationScenario{
		ID: 9, Guideword: "more", Parameter: "temperature",
		Cause: "cooling loss; runaway reaction", Consequence: "overpressure; release",
		Likelihood: 4, Severity: 5, ScenarioState: "analyzed", Version: 2,
	}
	safeguards := []model.Safeguard{
		{ID: 3, IndependenceKey: "SIS-A", Effectiveness: 0.4, TestIntervalDays: 365, LastVerifiedAt: &valid, LifecycleState: "active"},
		{ID: 2, IndependenceKey: "SIS-A", Effectiveness: 0.8, TestIntervalDays: 365, LastVerifiedAt: &valid, LifecycleState: "active"},
		{ID: 4, IndependenceKey: "PSV-B", Effectiveness: 0.9, TestIntervalDays: 365, LastVerifiedAt: &expired, LifecycleState: "expired"},
	}
	evaluator := NewEvaluator()
	first, err := evaluator.Evaluate(NewSnapshot(node, scenario, safeguards, reference, 30))
	if err != nil {
		t.Fatalf("first evaluation failed: %v", err)
	}
	second, err := evaluator.Evaluate(NewSnapshot(node, scenario, safeguards, reference, 30))
	if err != nil {
		t.Fatalf("second evaluation failed: %v", err)
	}
	if first.InputHash != second.InputHash || first.ExplanationJSON != second.ExplanationJSON {
		t.Fatal("same frozen input produced different output")
	}
	if first.CoverageScore != 80 {
		t.Fatalf("coverage score = %v, want 80", first.CoverageScore)
	}
	if len(first.Explanation.Deduplicated) != 1 || first.Explanation.Deduplicated[0].KeptID != 2 {
		t.Fatalf("unexpected deduplication: %#v", first.Explanation.Deduplicated)
	}
	passed, replayed, err := evaluator.Replay(first.SnapshotJSON, first.InputHash, first.CoverageScore, first.ProjectionJSON)
	if err != nil || !passed || replayed.CoverageScore != first.CoverageScore {
		t.Fatalf("replay failed: passed=%t score=%v err=%v", passed, replayed.CoverageScore, err)
	}
}

func TestEvaluatorDetectsUnprotectedPaths(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 8, 22, 10, 0, 0, 0, time.UTC)
	node := model.ProcessNode{ID: 1, NodeCode: "V-1", Name: "Vessel"}
	scenario := model.DeviationScenario{
		ID: 1, Guideword: "more", Parameter: "pressure",
		Cause: "blocked outlet", Consequence: "rupture", Likelihood: 3, Severity: 5,
		ScenarioState: "draft", Version: 1,
	}
	result, err := NewEvaluator().Evaluate(NewSnapshot(node, scenario, nil, reference, 30))
	if err != nil {
		t.Fatalf("evaluation failed: %v", err)
	}
	if result.CoverageScore != 0 || len(result.Explanation.Paths) != 1 || result.Explanation.Paths[0].Covered {
		t.Fatalf("expected one unprotected path, got %#v", result.Explanation.Paths)
	}
}

func TestProjectFailuresMarksEarliestRiskEscalation(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	// One layer of 0.6 effectiveness expires in 10 days. Initial risk 20 is
	// critical; with protection residual risk is medium (ceil(20*0.4)=8), so its
	// expiry must lift the rank back to critical within the default window.
	verified := reference.AddDate(0, 0, -20)
	node := model.ProcessNode{ID: 3, NodeCode: "R-3", Name: "Reactor"}
	scenario := model.DeviationScenario{
		ID: 5, Guideword: "more", Parameter: "pressure",
		Cause: "cooling loss", Consequence: "overpressure",
		Likelihood: 4, Severity: 5, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 11, Name: "SIS trip", SafeguardType: "interlock", IndependenceKey: "SIS-3",
			Effectiveness: 0.6, TestIntervalDays: 30, LastVerifiedAt: &verified, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 30)
	graph := BuildGraph(snapshot)
	independence := ResolveIndependence(snapshot.Safeguards, reference)
	score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
	if score.RiskAfter != "medium" {
		t.Fatalf("baseline residual rank = %q, want medium", score.RiskAfter)
	}
	projection, err := ProjectFailures(snapshot, graph, independence, score)
	if err != nil {
		t.Fatalf("project failures: %v", err)
	}
	if projection == nil {
		t.Fatal("expected a projection for a 30 day window")
	}
	response := projection.Projection
	if response.NoEscalation || response.EarliestEscalation == nil {
		t.Fatalf("expected an escalation within the window: %#v", response)
	}
	first := response.EarliestEscalation
	wantDate := reference.AddDate(0, 0, 10)
	if !first.ExpiresAt.Equal(wantDate) {
		t.Fatalf("earliest escalation date = %s, want %s", first.ExpiresAt, wantDate)
	}
	if first.DaysFromReference != 10 {
		t.Fatalf("days from reference = %d, want 10", first.DaysFromReference)
	}
	if first.RiskRankBefore != "medium" || first.RiskRankAfter != "critical" || !first.RiskRankRises {
		t.Fatalf("unexpected escalation ranks: %q -> %q rises=%t", first.RiskRankBefore, first.RiskRankAfter, first.RiskRankRises)
	}
	if first.CoverageScore != 0 {
		t.Fatalf("coverage after expiry = %v, want 0", first.CoverageScore)
	}
	if len(first.UncoveredPaths) != 1 {
		t.Fatalf("gap paths at escalation = %d, want 1", len(first.UncoveredPaths))
	}
	if first.UncoveredPaths[0].Cause != "cooling loss" || first.UncoveredPaths[0].Consequence != "overpressure" {
		t.Fatalf("unexpected gap path: %#v", first.UncoveredPaths[0])
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %d, want 1", len(response.Events))
	}
	if response.WindowEndRiskAfter != "critical" || response.WindowEndCoverage != 0 {
		t.Fatalf("unexpected window end state: rank=%s score=%v", response.WindowEndRiskAfter, response.WindowEndCoverage)
	}
}

func TestProjectFailuresReportsNoneWhenRankStaysStable(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	// 0.9 effectiveness holds initial risk 4 at residual 1 (low); losing it
	// returns to low as well, so no rank escalation may be reported.
	verified := reference.AddDate(0, 0, -5)
	node := model.ProcessNode{ID: 4, NodeCode: "V-4", Name: "Vessel"}
	scenario := model.DeviationScenario{
		ID: 6, Guideword: "less", Parameter: "flow",
		Cause: "pump trip", Consequence: "dry running",
		Likelihood: 1, Severity: 4, ScenarioState: "draft", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 12, Name: "Relief valve", SafeguardType: "relief", IndependenceKey: "PSV-4",
			Effectiveness: 0.9, TestIntervalDays: 20, LastVerifiedAt: &verified, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 30)
	graph := BuildGraph(snapshot)
	independence := ResolveIndependence(snapshot.Safeguards, reference)
	score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
	projection, err := ProjectFailures(snapshot, graph, independence, score)
	if err != nil {
		t.Fatalf("project failures: %v", err)
	}
	if projection == nil {
		t.Fatal("expected a projection for a 30 day window")
	}
	response := projection.Projection
	if !response.NoEscalation || response.EarliestEscalation != nil {
		t.Fatalf("expected no escalation, got %#v", response.EarliestEscalation)
	}
	if len(response.Events) != 1 {
		t.Fatalf("events = %d, want the expiry even without a rank change", len(response.Events))
	}
	if response.Events[0].RiskRankRises {
		t.Fatal("expiry event must not be flagged as a rank rise")
	}
}

func TestProjectFailuresPicksEarliestAcrossExpiryDates(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	verifiedEarly := reference.AddDate(0, 0, -40)
	verifiedLater := reference.AddDate(0, 0, -20)
	node := model.ProcessNode{ID: 5, NodeCode: "R-5", Name: "Column"}
	scenario := model.DeviationScenario{
		ID: 7, Guideword: "more", Parameter: "level",
		Cause: "inlet surge", Consequence: "overflow",
		Likelihood: 4, Severity: 5, ScenarioState: "verified", Version: 2,
	}
	// Independent keys A (0.4) and B (0.5) combine to 0.7, so baseline residual
	// risk ceil(20*0.3)=6 is medium. At day 10 only B remains: residual
	// ceil(20*0.5)=10 stays medium, while at day 30 it returns to critical.
	safeguards := []model.Safeguard{
		{ID: 13, Name: "First layer", SafeguardType: "alarm", IndependenceKey: "A",
			Effectiveness: 0.4, TestIntervalDays: 50, LastVerifiedAt: &verifiedEarly, LifecycleState: "active"},
		{ID: 14, Name: "Second layer", SafeguardType: "interlock", IndependenceKey: "B",
			Effectiveness: 0.5, TestIntervalDays: 50, LastVerifiedAt: &verifiedLater, LifecycleState: "active"},
	}
	snapshot := NewSnapshot(node, scenario, safeguards, reference, 60)
	graph := BuildGraph(snapshot)
	independence := ResolveIndependence(snapshot.Safeguards, reference)
	score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
	projection, err := ProjectFailures(snapshot, graph, independence, score)
	if err != nil {
		t.Fatalf("project failures: %v", err)
	}
	response := projection.Projection
	if len(response.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(response.Events))
	}
	if response.NoEscalation || response.EarliestEscalation == nil {
		t.Fatal("expected the day-30 expiry to escalate the rank")
	}
	if got := response.EarliestEscalation.DaysFromReference; got != 30 {
		t.Fatalf("earliest escalation offset = %d, want 30", got)
	}
	if response.EarliestEscalation.RiskRankAfter != "critical" {
		t.Fatalf("rank after escalation = %q, want critical", response.EarliestEscalation.RiskRankAfter)
	}
	// The day-10 event exists and records the score drop to B alone without a
	// rank rise.
	if response.Events[0].DaysFromReference != 10 || response.Events[0].RiskRankRises ||
		response.Events[0].CoverageScore != 50 {
		t.Fatalf("unexpected first event: %#v", response.Events[0])
	}
}

func TestProjectFailuresIsDeterministicAndExcludesEventsBeyondWindow(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	verified := reference.AddDate(0, 0, -300)
	node := model.ProcessNode{ID: 6, NodeCode: "T-6", Name: "Tank"}
	scenario := model.DeviationScenario{
		ID: 8, Guideword: "more", Parameter: "temperature",
		Cause: "fire exposure", Consequence: "rupture",
		Likelihood: 2, Severity: 4, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 15, Name: "Deluge", SafeguardType: "containment", IndependenceKey: "DLG-6",
			Effectiveness: 0.7, TestIntervalDays: 365, LastVerifiedAt: &verified, LifecycleState: "active"},
	}
	first := NewSnapshot(node, scenario, safeguards, reference, 30)
	second := NewSnapshot(node, scenario, safeguards, reference, 30)
	run := func(snapshot Snapshot) (dto.FailureProjectionResponse, string) {
		graph := BuildGraph(snapshot)
		independence := ResolveIndependence(snapshot.Safeguards, reference)
		score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
		projection, err := ProjectFailures(snapshot, graph, independence, score)
		if err != nil {
			t.Fatalf("project failures: %v", err)
		}
		return projection.Projection, projection.JSON
	}
	projectionA, jsonA := run(first)
	_, jsonB := run(second)
	if jsonA != jsonB {
		t.Fatal("failure projection must be deterministic for the same frozen snapshot")
	}
	if !projectionA.NoEscalation || len(projectionA.Events) != 0 {
		t.Fatalf("expiry at day 65 must not appear in a 30 day window: %#v", projectionA)
	}
	if !projectionA.WindowEnd.Equal(reference.AddDate(0, 0, 30)) {
		t.Fatalf("window end = %s", projectionA.WindowEnd)
	}
}

func TestProjectFailuresWindowEndBoundary(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	// Verification 40 days ago with a 50 day interval => expiry at day 10.
	verified := reference.AddDate(0, 0, -40)
	node := model.ProcessNode{ID: 8, NodeCode: "B-8", Name: "Boundary"}
	scenario := model.DeviationScenario{
		ID: 9, Guideword: "more", Parameter: "pressure",
		Cause: "blocked outlet", Consequence: "rupture",
		Likelihood: 4, Severity: 5, ScenarioState: "analyzed", Version: 1,
	}
	safeguards := []model.Safeguard{
		{ID: 16, Name: "Boundary layer", SafeguardType: "relief", IndependenceKey: "BDY-8",
			Effectiveness: 0.6, TestIntervalDays: 50, LastVerifiedAt: &verified, LifecycleState: "active"},
	}
	build := func(windowDays int) dto.FailureProjectionResponse {
		snapshot := NewSnapshot(node, scenario, safeguards, reference, windowDays)
		graph := BuildGraph(snapshot)
		independence := ResolveIndependence(snapshot.Safeguards, reference)
		score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
		projection, err := ProjectFailures(snapshot, graph, independence, score)
		if err != nil || projection == nil {
			t.Fatalf("project failures for window %d: %v", windowDays, err)
		}
		return projection.Projection
	}
	// Expiry exactly on the window end (day 10, window 10) is not an in-window
	// failure, so no escalation event is recorded.
	atBoundary := build(10)
	if len(atBoundary.Events) != 0 || !atBoundary.NoEscalation {
		t.Fatalf("window-end expiry must stay outside the window: %#v", atBoundary.Events)
	}
	// One more day of look-ahead includes the day-10 expiry.
	inside := build(11)
	if len(inside.Events) != 1 || inside.NoEscalation ||
		inside.EarliestEscalation == nil || inside.EarliestEscalation.DaysFromReference != 10 {
		t.Fatalf("day-10 expiry must be included in an 11 day window: %#v", inside.Events)
	}
}

func TestProjectFailuresDisabledWithoutWindow(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 9, 22, 8, 0, 0, 0, time.UTC)
	snapshot := Snapshot{
		AlgorithmVersion: Version, ReferenceTime: reference, FailureWindowDays: 0,
		Node:     SnapshotNode{ID: 1, NodeCode: "N-1"},
		Scenario: SnapshotScenario{ID: 1, Likelihood: 1, Severity: 1},
	}
	projection, err := ProjectFailures(snapshot, Graph{}, IndependenceResult{}, ScoreResult{})
	if err != nil {
		t.Fatalf("project failures: %v", err)
	}
	if projection != nil {
		t.Fatal("no projection should be produced when the snapshot carries no window")
	}
}
