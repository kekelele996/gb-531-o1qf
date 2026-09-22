package algorithm

import (
	"encoding/json"
	"fmt"
	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/util"
)

const SafetyBoundary = "Offline decision support only. Results cannot replace a licensed process-safety professional's judgment and cannot issue equipment control commands."

type EvaluationResult struct {
	SnapshotJSON      string
	InputHash         string
	CoverageScore     float64
	UncoveredJSON     string
	DeduplicatedJSON  string
	ExplanationJSON   string
	ProjectionJSON    string
	FailureWindowDays int
	RiskBefore        string
	RiskAfter         string
	Explanation       dto.EvaluationExplanation
	Projection        *dto.FailureProjectionResponse
}
type Evaluator struct{}

func NewEvaluator() *Evaluator { return &Evaluator{} }
func (e *Evaluator) Evaluate(snapshot Snapshot) (EvaluationResult, error) {
	if snapshot.AlgorithmVersion != Version {
		return EvaluationResult{}, fmt.Errorf("algorithm version mismatch: got %q want %q", snapshot.AlgorithmVersion, Version)
	}
	if snapshot.Node.ID == 0 || snapshot.Scenario.ID == 0 {
		return EvaluationResult{}, fmt.Errorf("snapshot requires persisted node and scenario")
	}
	if snapshot.ReferenceTime.IsZero() {
		return EvaluationResult{}, fmt.Errorf("snapshot reference time is required")
	}
	snapshotJSON, err := util.CanonicalJSON(snapshot)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("serialize evaluation snapshot: %w", err)
	}
	graph := BuildGraph(snapshot)
	independence := ResolveIndependence(snapshot.Safeguards, snapshot.ReferenceTime)
	score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
	projection, err := ProjectFailures(snapshot, graph, independence, score)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("project future safeguard failures: %w", err)
	}
	projectionJSON := "{}"
	if projection != nil {
		projectionJSON = projection.JSON
	}
	explanation := dto.EvaluationExplanation{
		Summary: fmt.Sprintf("%d cause-to-consequence paths evaluated; %d paths are below the protection threshold.", len(score.Paths), len(score.UncoveredPaths)),
		Paths:   score.Paths, ScoreSteps: score.Steps, Deduplicated: independence.Deduplicated,
		BoundaryNote: SafetyBoundary, ReferenceTime: snapshot.ReferenceTime,
		FailureWindowDays: snapshot.FailureWindowDays,
	}
	uncoveredJSON, err := util.CanonicalJSON(score.UncoveredPaths)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("serialize uncovered paths: %w", err)
	}
	deduplicatedJSON, err := util.CanonicalJSON(independence.Deduplicated)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("serialize deduplicated safeguards: %w", err)
	}
	explanationJSON, err := util.CanonicalJSON(explanation)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("serialize scoring explanation: %w", err)
	}
	result := EvaluationResult{
		SnapshotJSON: snapshotJSON, InputHash: util.HashString(snapshotJSON), CoverageScore: score.CoverageScore,
		UncoveredJSON: uncoveredJSON, DeduplicatedJSON: deduplicatedJSON,
		ExplanationJSON: explanationJSON, ProjectionJSON: projectionJSON,
		RiskBefore: score.RiskBefore, RiskAfter: score.RiskAfter,
		Explanation: explanation, FailureWindowDays: snapshot.FailureWindowDays,
	}
	if projection != nil {
		projectionCopy := projection.Projection
		result.Projection = &projectionCopy
	}
	return result, nil
}
func (e *Evaluator) Replay(snapshotJSON string, expectedHash string, expectedScore float64, expectedProjectionJSON string) (bool, EvaluationResult, error) {
	if util.HashString(snapshotJSON) != expectedHash {
		return false, EvaluationResult{}, fmt.Errorf("stored snapshot hash does not match stored input hash")
	}
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return false, EvaluationResult{}, fmt.Errorf("decode frozen snapshot: %w", err)
	}
	result, err := e.Evaluate(snapshot)
	if err != nil {
		return false, EvaluationResult{}, fmt.Errorf("replay evaluation: %w", err)
	}
	passed := result.InputHash == expectedHash && result.CoverageScore == expectedScore
	// Evaluations frozen before the failure-window feature have no window in
	// their snapshot and no stored projection; the historical prediction must
	// remain replayable even though it never carried one.
	if passed && snapshot.FailureWindowDays > 0 && expectedProjectionJSON != "" && expectedProjectionJSON != "{}" {
		passed = result.ProjectionJSON == expectedProjectionJSON
	}
	return passed, result, nil
}
