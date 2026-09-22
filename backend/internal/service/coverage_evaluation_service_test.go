package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"hazop-safeguard-coverage/backend/internal/algorithm"
	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/model"
	"hazop-safeguard-coverage/backend/internal/repository"
	"hazop-safeguard-coverage/backend/internal/util"
)

func newCoverageService(t *testing.T) (CoverageEvaluationService, *gormSeed, time.Time) {
	t.Helper()
	db := testDB(t)
	nodeRepo := repository.NewProcessNodeRepository(db)
	scenarioRepo := repository.NewDeviationScenarioRepository(db)
	safeguardRepo := repository.NewSafeguardRepository(db)
	evaluationRepo := repository.NewCoverageEvaluationRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	reference := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	node := model.ProcessNode{
		NodeCode: "W-1", Name: "Window Node", UnitName: "Window Unit", Medium: "oil",
		DesignPressure: 3, DesignTemperature: 180, OwnerTeam: "safety", Status: "active",
		CreatedAt: reference, UpdatedAt: reference,
	}
	if err := nodeRepo.Create(context.Background(), &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	scenario := model.DeviationScenario{
		ProcessNodeID: node.ID, Guideword: "more", Parameter: "temperature",
		Cause: "cooling lost", Consequence: "overpressure",
		Likelihood: 4, Severity: 4, ScenarioState: "analyzed", Version: 1,
		CreatedBy: 99, CreatedByName: "author", CreatedAt: reference, UpdatedAt: reference,
	}
	if err := scenarioRepo.Create(context.Background(), &scenario); err != nil {
		t.Fatalf("create scenario: %v", err)
	}
	safeguards := []model.Safeguard{
		{
			Name: "SIS trip", SafeguardType: "interlock", TargetScenarioID: scenario.ID,
			IndependenceKey: "SIS-W1", Effectiveness: 0.6, TestIntervalDays: 15,
			LastVerifiedAt: &reference, LifecycleState: "active", EvidenceNote: "cert",
			CreatedAt: reference, UpdatedAt: reference,
		},
		{
			Name: "Alarm", SafeguardType: "alarm", TargetScenarioID: scenario.ID,
			IndependenceKey: "ALM-W1", Effectiveness: 0.4, TestIntervalDays: 120,
			LastVerifiedAt: &reference, LifecycleState: "active", EvidenceNote: "cert",
			CreatedAt: reference, UpdatedAt: reference,
		},
	}
	for index := range safeguards {
		if err := safeguardRepo.Create(context.Background(), &safeguards[index]); err != nil {
			t.Fatalf("create safeguard: %v", err)
		}
	}
	service := NewCoverageEvaluationService(evaluationRepo, scenarioRepo, nodeRepo, safeguardRepo, auditRepo, algorithm.NewEvaluator()).(*coverageEvaluationService).
		withClock(func() time.Time { return reference })
	return service, &gormSeed{scenarioID: scenario.ID}, reference
}

type gormSeed struct{ scenarioID uint }

func TestCoverageRunRejectsWindowOutsideRange(t *testing.T) {
	service, seed, _ := newCoverageService(t)
	actor := util.Actor{UserID: 7, Username: "engineer", Role: "process_engineer", RequestID: "req-window-bad"}
	for _, days := range []int{-1, 0, 366, 500} {
		// Zero falls back to the default and is accepted; explicit out-of-range
		// values are the only rejected payloads.
		if days == 0 {
			continue
		}
		_, _, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
			ScenarioID: seed.scenarioID, FailureWindowDays: days,
		}, "key-window-bad-0001", actor)
		var appErr *util.AppError
		if !errors.As(err, &appErr) || appErr.Status != 400 {
			t.Fatalf("days=%d should be rejected with 400, got %v", days, err)
		}
	}
}

func TestCoverageRunDefaultWindowAndForecastPersistence(t *testing.T) {
	service, seed, reference := newCoverageService(t)
	actor := util.Actor{UserID: 7, Username: "engineer", Role: "process_engineer", RequestID: "req-window-run"}
	response, duplicate, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID,
	}, "key-window-default-01", actor)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if duplicate {
		t.Fatal("first run must not be reported as duplicate")
	}
	if response.FailureWindowDays != algorithm.DefaultFailureWindowDays {
		t.Fatalf("default window = %d, want %d", response.FailureWindowDays, algorithm.DefaultFailureWindowDays)
	}
	// The day-15 SIS expiry removes 0.6 protection, leaving 0.4 (< threshold);
	// residual risk rises from low to medium and the path becomes uncovered.
	if !response.FailureForecast.EscalationWithin || response.FailureForecast.FirstEscalation == nil {
		t.Fatalf("expected escalation forecast, got %#v", response.FailureForecast)
	}
	first := response.FailureForecast.FirstEscalation
	if first.HorizonDays != 15 || first.RiskRankAfter != "medium" {
		t.Fatalf("unexpected first escalation: %#v", first)
	}
	if len(first.UncoveredPaths) != 1 {
		t.Fatalf("expected one gap path at escalation, got %d", len(first.UncoveredPaths))
	}
	if !reference.AddDate(0, 0, 15).Equal(first.ExpiresAt) {
		t.Fatalf("escalation date = %s, want %s", first.ExpiresAt, reference.AddDate(0, 0, 15))
	}
	stored, err := service.Get(context.Background(), response.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if stored.FailureForecast.WindowDays != algorithm.DefaultFailureWindowDays || !stored.FailureForecast.EscalationWithin {
		t.Fatalf("forecast must be restored from frozen storage: %#v", stored.FailureForecast)
	}
}

func TestCoverageRunIdempotencyIsWindowScoped(t *testing.T) {
	service, seed, _ := newCoverageService(t)
	actor := util.Actor{UserID: 7, Username: "engineer", Role: "process_engineer", RequestID: "req-window-idem"}
	key := "key-window-scoped-01"
	first, duplicate, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID, FailureWindowDays: 30,
	}, key, actor)
	if err != nil || duplicate {
		t.Fatalf("first run: duplicate=%t err=%v", duplicate, err)
	}
	replayed, duplicate, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID, FailureWindowDays: 30,
	}, key, actor)
	if err != nil || !duplicate || replayed.ID != first.ID {
		t.Fatalf("same key+window must return stored run: duplicate=%t ids=%d/%d err=%v", duplicate, first.ID, replayed.ID, err)
	}
	other, duplicate, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID, FailureWindowDays: 90,
	}, key, actor)
	if err != nil || duplicate || other.ID == first.ID {
		t.Fatalf("same key with different window must create a distinct run: duplicate=%t ids=%d/%d err=%v", duplicate, first.ID, other.ID, err)
	}
}

func TestCoverageComparisonIncludesEscalationHorizons(t *testing.T) {
	service, seed, _ := newCoverageService(t)
	actor := util.Actor{UserID: 7, Username: "engineer", Role: "process_engineer", RequestID: "req-window-cmp"}
	short, _, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID, FailureWindowDays: 10,
	}, "key-window-compare-1", actor)
	if err != nil {
		t.Fatalf("short run: %v", err)
	}
	long, _, err := service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: seed.scenarioID, FailureWindowDays: 60,
	}, "key-window-compare-2", actor)
	if err != nil {
		t.Fatalf("long run: %v", err)
	}
	comparison, err := service.Compare(context.Background(), short.ID, long.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if comparison.BaseEscalationDays != nil {
		t.Fatalf("10-day window must have no escalation, got %v", *comparison.BaseEscalationDays)
	}
	if comparison.OtherEscalationDays == nil || *comparison.OtherEscalationDays != 15 {
		t.Fatalf("60-day window must escalate at day 15, got %v", comparison.OtherEscalationDays)
	}
	if !comparison.EscalationChanged {
		t.Fatal("comparison must flag that the escalation horizon differs")
	}
}
