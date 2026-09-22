package algorithm

import (
	"fmt"
	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/util"
	"math"
	"sort"
	"time"
)

const coveredThreshold = 0.5

type ScoreResult struct {
	CoverageScore  float64
	Paths          []dto.CoveragePathResponse
	UncoveredPaths []dto.CoveragePathResponse
	Steps          []dto.ScoreStepResponse
	RiskBefore     string
	RiskAfter      string
}

func CalculateScore(graph Graph, scenario SnapshotScenario, safeguards []SnapshotSafeguard, rejected []RejectedSafeguard) ScoreResult {
	ordered := append([]SnapshotSafeguard(nil), safeguards...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].IndependenceKey == ordered[j].IndependenceKey {
			return ordered[i].ID < ordered[j].ID
		}
		return ordered[i].IndependenceKey < ordered[j].IndependenceKey
	})
	combined := 0.0
	remainingProbability := 1.0
	steps := make([]dto.ScoreStepResponse, 0, len(ordered)+len(rejected)+2)
	stepNumber := 1
	for _, safeguard := range ordered {
		before := 1 - remainingProbability
		remainingProbability *= 1 - safeguard.Effectiveness
		combined = 1 - remainingProbability
		steps = append(steps, dto.ScoreStepResponse{
			Step: stepNumber, Rule: "independent-layer-combination",
			Input:        fmt.Sprintf("safeguard=%d key=%s effectiveness=%.4f", safeguard.ID, safeguard.IndependenceKey, safeguard.Effectiveness),
			Contribution: round4(combined - before), RunningScore: round4(combined * 100),
			Explanation: "Independent effectiveness is combined as 1 - product(1 - effectiveness).",
		})
		stepNumber++
	}
	for _, item := range rejected {
		steps = append(steps, dto.ScoreStepResponse{
			Step: stepNumber, Rule: "eligibility-filter", Input: fmt.Sprintf("safeguard=%d", item.ID),
			Contribution: 0, RunningScore: round4(combined * 100), Explanation: item.Reason,
		})
		stepNumber++
	}
	paths := make([]dto.CoveragePathResponse, 0, len(graph.Paths))
	ids := make([]uint, 0, len(ordered))
	keys := make([]string, 0, len(ordered))
	for _, safeguard := range ordered {
		ids = append(ids, safeguard.ID)
		keys = append(keys, safeguard.IndependenceKey)
	}
	covered := combined >= coveredThreshold
	for _, path := range graph.Paths {
		reason := fmt.Sprintf("combined independent protection %.2f%% meets %.0f%% threshold", combined*100, coveredThreshold*100)
		if !covered {
			reason = fmt.Sprintf("combined independent protection %.2f%% is below %.0f%% threshold", combined*100, coveredThreshold*100)
		}
		pathResult := dto.CoveragePathResponse{
			PathID: path.ID, NodeCode: path.NodeCode, Cause: path.Cause, Consequence: path.Consequence,
			SafeguardIDs: append([]uint(nil), ids...), IndependenceKeys: append([]string(nil), keys...),
			CombinedProtection: round4(combined), Covered: covered, Reason: reason,
		}
		paths = append(paths, pathResult)
	}
	uncovered := make([]dto.CoveragePathResponse, 0)
	for _, path := range paths {
		if !path.Covered {
			uncovered = append(uncovered, path)
		}
	}
	coverageScore := round4(combined * 100)
	initialRisk := scenario.Likelihood * scenario.Severity
	residualRisk := int(math.Ceil(float64(initialRisk) * (1 - combined)))
	if residualRisk < 1 {
		residualRisk = 1
	}
	steps = append(steps, dto.ScoreStepResponse{
		Step: stepNumber, Rule: "residual-risk", Input: fmt.Sprintf("initial=%d protection=%.4f", initialRisk, combined),
		Contribution: 0, RunningScore: coverageScore,
		Explanation: fmt.Sprintf("Residual risk index is ceil(%d * (1 - %.4f)) = %d.", initialRisk, combined, residualRisk),
	})
	return ScoreResult{
		CoverageScore: coverageScore, Paths: paths, UncoveredPaths: uncovered, Steps: steps,
		RiskBefore: dto.RiskRank(initialRisk), RiskAfter: dto.RiskRank(residualRisk),
	}
}
func round4(value float64) float64 { return math.Round(value*10000) / 10000 }

// FailureProjection replays the coverage score at every safeguard verification
// expiry date between the frozen reference time and the configured window. The
// result is derived solely from the frozen snapshot, so historical look-back and
// version comparison always reproduce the same prediction.
type FailureProjection struct {
	Projection dto.FailureProjectionResponse
	JSON       string
}

// ProjectFailures recomputes coverage against the verification expiry dates of
// the safeguards eligible at the frozen reference time and identifies the first
// date on which the residual risk rank rises. A nil result is returned for
// frozen inputs that carry no failure window.
func ProjectFailures(snapshot Snapshot, graph Graph, baseline IndependenceResult, baselineScore ScoreResult) (*FailureProjection, error) {
	if snapshot.FailureWindowDays <= 0 {
		return nil, nil
	}
	windowEnd := snapshot.ReferenceTime.AddDate(0, 0, snapshot.FailureWindowDays)
	eventsByTime := make(map[string][]uint)
	for _, safeguard := range baseline.Retained {
		if safeguard.LastVerifiedAt == nil || safeguard.TestIntervalDays <= 0 {
			continue
		}
		expires := safeguard.LastVerifiedAt.AddDate(0, 0, safeguard.TestIntervalDays).UTC().Truncate(time.Second)
		// Eligibility is inclusive of the expiry instant; the layer fails on the
		// day after it. Expiry before the freeze point is excluded because such a
		// layer never participates in the baseline score; expiry on or after the
		// window end never changes the result inside the window.
		if expires.Before(snapshot.ReferenceTime) || !expires.Before(windowEnd) {
			continue
		}
		key := expires.Format(time.RFC3339)
		eventsByTime[key] = append(eventsByTime[key], safeguard.ID)
	}
	eventTimes := make([]time.Time, 0, len(eventsByTime))
	for key := range eventsByTime {
		parsed, _ := time.Parse(time.RFC3339, key)
		eventTimes = append(eventTimes, parsed)
	}
	sort.Slice(eventTimes, func(i, j int) bool { return eventTimes[i].Before(eventTimes[j]) })

	referenceRank := baselineScore.RiskAfter
	response := dto.FailureProjectionResponse{
		WindowDays:        snapshot.FailureWindowDays,
		WindowEnd:         windowEnd,
		BaselineRiskAfter: referenceRank,
		BaselineCoverage:  baselineScore.CoverageScore,
		NoEscalation:      true,
		Events:            []dto.FailureEventResponse{},
	}
	currentScore := baselineScore
	escalated := false
	for _, at := range eventTimes {
		ids := append([]uint(nil), eventsByTime[at.Format(time.RFC3339)]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		// Probe eligibility just after the expiry instant, which is exactly the
		// state the frozen inputs predict for the following verification day.
		probe := at.Add(time.Second)
		independence := ResolveIndependence(snapshot.Safeguards, probe)
		score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
		rankRises := rankLevel(score.RiskAfter) > rankLevel(referenceRank)
		eventResponse := dto.FailureEventResponse{
			ExpiresAt:            at,
			DaysFromReference:    int(at.Sub(snapshot.ReferenceTime).Hours() / 24),
			ExpiringSafeguardIDs: ids,
			CoverageScore:        score.CoverageScore,
			RiskRankBefore:       referenceRank,
			RiskRankAfter:        score.RiskAfter,
			RiskRankRises:        rankRises,
			UncoveredPaths:       append([]dto.CoveragePathResponse(nil), score.UncoveredPaths...),
		}
		response.Events = append(response.Events, eventResponse)
		if rankRises && !escalated {
			escalated = true
			response.NoEscalation = false
			response.EarliestEscalation = &eventResponse
		}
		currentScore = score
	}
	response.WindowEndCoverage = currentScore.CoverageScore
	response.WindowEndRiskAfter = currentScore.RiskAfter

	projectionJSON, err := util.CanonicalJSON(response)
	if err != nil {
		return nil, err
	}
	return &FailureProjection{Projection: response, JSON: projectionJSON}, nil
}

func rankLevel(rank string) int {
	switch rank {
	case "critical":
		return 3
	case "high":
		return 2
	case "medium":
		return 1
	default:
		return 0
	}
}
