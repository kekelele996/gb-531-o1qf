package algorithm

import (
	"sort"
	"time"

	"hazop-safeguard-coverage/backend/internal/dto"
)

const (
	// DefaultFailureWindowDays is applied when a run does not specify a window.
	DefaultFailureWindowDays = 30
	// MinFailureWindowDays / MaxFailureWindowDays bound the accepted forecast horizon.
	MinFailureWindowDays = 1
	MaxFailureWindowDays = 365
)

// FailureWindowEvent is a deterministic projection of coverage at one expiry date.
type FailureWindowEvent struct {
	HorizonDays         int                        `json:"horizon_days"`
	ExpiresAt           time.Time                  `json:"expires_at"`
	ExpiredSafeguardIDs []uint                     `json:"expired_safeguard_ids"`
	CoverageScore       float64                    `json:"coverage_score"`
	RiskRankAfter       string                     `json:"risk_rank_after"`
	RiskRankIncreased   bool                       `json:"risk_rank_increased"`
	UncoveredPaths      []dto.CoveragePathResponse `json:"uncovered_paths"`
}

// FailureWindowForecast is computed purely from the frozen snapshot. Replaying
// the same snapshot always reproduces the same forecast.
type FailureWindowForecast struct {
	WindowDays       int                  `json:"window_days"`
	WindowEnd        time.Time            `json:"window_end"`
	Events           []FailureWindowEvent `json:"events"`
	FirstEscalation  *FailureWindowEvent  `json:"first_escalation,omitempty"`
	EscalationWithin bool                 `json:"escalation_within_window"`
	ForecastNote     string               `json:"forecast_note"`
}

// NormalizeWindowDays applies the default window and clamps the horizon to the
// supported 1..365 day range.
func NormalizeWindowDays(days int) int {
	if days <= 0 {
		return DefaultFailureWindowDays
	}
	if days > MaxFailureWindowDays {
		return MaxFailureWindowDays
	}
	return days
}

// ProjectFailureWindow recomputes coverage at every distinct verification
// expiry date of the layers that are eligible at the frozen reference time, up
// to reference + windowDays. It returns the earliest event at which the
// residual risk rank rises above the baseline rank, or an explicit "no
// escalation" forecast when nothing changes inside the window.
func ProjectFailureWindow(snapshot Snapshot, windowDays int, baseline ScoreResult) FailureWindowForecast {
	windowDays = NormalizeWindowDays(windowDays)
	windowEnd := snapshot.ReferenceTime.AddDate(0, 0, windowDays)
	forecast := FailureWindowForecast{
		WindowDays: windowDays, WindowEnd: windowEnd.UTC().Truncate(time.Second),
		Events:       []FailureWindowEvent{},
		ForecastNote: "Offline projection from the frozen snapshot; it cannot issue equipment control commands.",
	}
	baselineRank := baseline.RiskAfter
	type expiryGroup struct {
		at           time.Time
		safeguardIDs []uint
	}
	groups := map[int64]*expiryGroup{}
	for _, safeguard := range snapshot.Safeguards {
		if ineligibleReason(safeguard, snapshot.ReferenceTime) != "" {
			continue
		}
		if safeguard.LastVerifiedAt == nil || safeguard.TestIntervalDays <= 0 {
			continue
		}
		expires := safeguard.LastVerifiedAt.AddDate(0, 0, safeguard.TestIntervalDays).UTC().Truncate(time.Second)
		// Eligibility at the reference time uses the exact timestamp; the
		// projection horizon is measured in calendar dates, so events are
		// filtered by date rather than elapsed time.
		if calendarDayDifference(snapshot.ReferenceTime, expires) < MinFailureWindowDays || calendarDayDifference(snapshot.ReferenceTime, expires) > windowDays {
			continue
		}
		key := expires.Unix()
		group, ok := groups[key]
		if !ok {
			group = &expiryGroup{at: expires}
			groups[key] = group
		}
		group.safeguardIDs = append(group.safeguardIDs, safeguard.ID)
	}
	if len(groups) == 0 {
		return forecast
	}
	orderedKeys := make([]int64, 0, len(groups))
	for key := range groups {
		orderedKeys = append(orderedKeys, key)
	}
	sort.Slice(orderedKeys, func(i, j int) bool { return orderedKeys[i] < orderedKeys[j] })
	graph := BuildGraph(snapshot)
	for _, key := range orderedKeys {
		group := groups[key]
		// Expiry dates are calendar dates, so the horizon is the whole-day
		// difference rather than an elapsed-hour quotient.
		horizon := calendarDayDifference(snapshot.ReferenceTime, group.at)
		if horizon < MinFailureWindowDays {
			horizon = MinFailureWindowDays
		}
		independence := resolveIndependence(snapshot.Safeguards, group.at, true)
		score := CalculateScore(graph, snapshot.Scenario, independence.Retained, independence.Rejected)
		sort.Slice(group.safeguardIDs, func(i, j int) bool { return group.safeguardIDs[i] < group.safeguardIDs[j] })
		event := FailureWindowEvent{
			HorizonDays:         horizon,
			ExpiresAt:           group.at,
			ExpiredSafeguardIDs: group.safeguardIDs,
			CoverageScore:       score.CoverageScore,
			RiskRankAfter:       score.RiskAfter,
			RiskRankIncreased:   rankSeverity(score.RiskAfter) > rankSeverity(baselineRank),
			UncoveredPaths:      score.UncoveredPaths,
		}
		forecast.Events = append(forecast.Events, event)
		if event.RiskRankIncreased && forecast.FirstEscalation == nil {
			escalation := event
			forecast.FirstEscalation = &escalation
			forecast.EscalationWithin = true
		}
	}
	return forecast
}

func rankSeverity(rank string) int {
	switch rank {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	default:
		return 1
	}
}

// calendarDayDifference counts whole calendar dates between from and to in
// UTC, so a verification performed one day before the reference date still
// projects an expiry at the configured whole test-interval horizon.
func calendarDayDifference(from, to time.Time) int {
	fromDay := time.Date(from.Year(), from.Month(), from.Day(), 0, 0, 0, 0, time.UTC)
	toDay := time.Date(to.Year(), to.Month(), to.Day(), 0, 0, 0, 0, time.UTC)
	return int(toDay.Sub(fromDay).Hours() / 24)
}
