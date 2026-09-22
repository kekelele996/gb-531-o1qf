package algorithm

import (
	"encoding/json"
	"fmt"
	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/util"
)

const SafetyBoundary = "Offline decision support only. Results cannot replace a licensed process-safety professional's judgment and cannot issue equipment control commands."

// LegacyVersion identifies evaluations produced before the failure window
// feature. Their snapshots stay replayable for the coverage score; the failure
// window projection is only persisted for Version (v1.1.0+) evaluations.
const LegacyVersion = "hazop-cover-v1.0.0"

type EvaluationResult struct {
	SnapshotJSON        string
	InputHash           string
	CoverageScore       float64
	UncoveredJSON       string
	DeduplicatedJSON    string
	ExplanationJSON     string
	FailureForecastJSON string
	RiskBefore          string
	RiskAfter           string
	Explanation         dto.EvaluationExplanation
	FailureForecast     FailureWindowForecast
}
type Evaluator struct{}

func NewEvaluator() *Evaluator { return &Evaluator{} }
func (e *Evaluator) Evaluate(snapshot Snapshot) (EvaluationResult, error) {
	if snapshot.AlgorithmVersion != Version && snapshot.AlgorithmVersion != LegacyVersion {
		return EvaluationResult{}, fmt.Errorf("algorithm version mismatch: got %q want %q", snapshot.AlgorithmVersion, Version)
	}
	if snapshot.Node.ID == 0 || snapshot.Scenario.ID == 0 {
		return EvaluationResult{}, fmt.Errorf("snapshot requires persisted node and scenario")
	}
	if snapshot.ReferenceTime.IsZero() {
		return EvaluationResult{}, fmt.Errorf("snapshot reference time is required")
	}
	legacySnapshot := snapshot.AlgorithmVersion == LegacyVersion
	// Legacy snapshots predate the failure window. Their frozen bytes (and
	// therefore input hash) are preserved on replay; current snapshots are
	// normalized to the canonical window before serialization.
	if !legacySnapshot {
		snapshot.FailureWindowDays = NormalizeWindowDays(snapshot.FailureWindowDays)
	}
	snapshotJSON, err := util.CanonicalJSON(snapshot)
	if err != nil {
		return EvaluationResult{}, fmt.Errorf("serialize evaluation snapshot: %w", err)
	}
	graph := BuildGraph(snapshot)
	independence := ResolveIndependence(snapshot.Safeguards, snapshot.ReferenceTime)
	score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
	explanation := dto.EvaluationExplanation{
		Summary: fmt.Sprintf("%d cause-to-consequence paths evaluated; %d paths are below the protection threshold.", len(score.Paths), len(score.UncoveredPaths)),
		Paths:   score.Paths, ScoreSteps: score.Steps, Deduplicated: independence.Deduplicated,
		BoundaryNote: SafetyBoundary, ReferenceTime: snapshot.ReferenceTime,
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
	forecast := ProjectFailureWindow(snapshot, snapshot.FailureWindowDays, score)
	forecastJSON := ""
	if !legacySnapshot {
		forecastJSON, err = util.CanonicalJSON(forecast)
		if err != nil {
			return EvaluationResult{}, fmt.Errorf("serialize failure window forecast: %w", err)
		}
	}
	return EvaluationResult{
		SnapshotJSON: snapshotJSON, InputHash: util.HashString(snapshotJSON), CoverageScore: score.CoverageScore,
		UncoveredJSON: uncoveredJSON, DeduplicatedJSON: deduplicatedJSON,
		ExplanationJSON: explanationJSON, FailureForecastJSON: forecastJSON,
		RiskBefore: score.RiskBefore, RiskAfter: score.RiskAfter,
		Explanation: explanation, FailureForecast: forecast,
	}, nil
}
func (e *Evaluator) Replay(snapshotJSON string, expectedHash string, expectedScore float64, expectedForecastJSON string) (bool, EvaluationResult, error) {
	if util.HashString(snapshotJSON) != expectedHash {
		return false, EvaluationResult{}, fmt.Errorf("stored snapshot hash does not match stored input hash")
	}
	var snapshot Snapshot
	if err := json.Unmarshal([]byte(snapshotJSON), &snapshot); err != nil {
		return false, EvaluationResult{}, fmt.Errorf("decode frozen snapshot: %w", err)
	}
	legacy := snapshot.AlgorithmVersion == LegacyVersion
	result, err := e.Evaluate(snapshot)
	if err != nil {
		return false, EvaluationResult{}, fmt.Errorf("replay evaluation: %w", err)
	}
	passed := result.CoverageScore == expectedScore
	if !legacy {
		// v1.1.0+ snapshots carry the failure window, so the recomputed hash and
		// forecast must match what was frozen when the evaluation ran.
		passed = passed && result.InputHash == expectedHash && result.FailureForecastJSON == expectedForecastJSON
	}
	return passed, result, nil
}
