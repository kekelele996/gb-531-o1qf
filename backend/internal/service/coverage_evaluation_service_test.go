package service

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"
	"time"

	"hazop-safeguard-coverage/backend/internal/algorithm"
	"hazop-safeguard-coverage/backend/internal/dto"
	"hazop-safeguard-coverage/backend/internal/model"
	"hazop-safeguard-coverage/backend/internal/repository"
	"hazop-safeguard-coverage/backend/internal/util"

	"gorm.io/gorm"
)

type coverageTestHarness struct {
	db         *gorm.DB
	service    CoverageEvaluationService
	safeguards repository.SafeguardRepository
	node       model.ProcessNode
	scenario   model.DeviationScenario
	now        time.Time
}

func newCoverageTestHarness(t *testing.T) coverageTestHarness {
	t.Helper()
	db := testDB(t)
	nodeRepo := repository.NewProcessNodeRepository(db)
	scenarioRepo := repository.NewDeviationScenarioRepository(db)
	safeguardRepo := repository.NewSafeguardRepository(db)
	evaluationRepo := repository.NewCoverageEvaluationRepository(db)
	auditRepo := repository.NewAuditRepository(db)
	now := time.Now().UTC().Truncate(time.Second)
	node := model.ProcessNode{
		NodeCode: "C-101", Name: "Coverage Node", UnitName: "Test Unit", Medium: "gas",
		DesignPressure: 2, DesignTemperature: 120, OwnerTeam: "test", Status: "active",
		CreatedAt: now, UpdatedAt: now,
	}
	if err := nodeRepo.Create(context.Background(), &node); err != nil {
		t.Fatalf("create node: %v", err)
	}
	scenario := model.DeviationScenario{
		ProcessNodeID: node.ID, Guideword: "more", Parameter: "pressure",
		Cause: "gas outlet blocked", Consequence: "shell rupture and release",
		Likelihood: 4, Severity: 5, ScenarioState: "analyzed", Version: 1,
		CreatedBy: 7, CreatedByName: "engineer", CreatedAt: now, UpdatedAt: now,
	}
	if err := scenarioRepo.Create(context.Background(), &scenario); err != nil {
		t.Fatalf("create scenario: %v", err)
	}
	svc := NewCoverageEvaluationService(
		evaluationRepo, scenarioRepo, nodeRepo, safeguardRepo, auditRepo, algorithm.NewEvaluator(),
	)
	return coverageTestHarness{
		db: db, service: svc, safeguards: safeguardRepo,
		node: node, scenario: scenario, now: now,
	}
}

func (h coverageTestHarness) addSafeguard(t *testing.T, id uint, key string, effectiveness float64, expiresInDays int) {
	t.Helper()
	verified := h.now.AddDate(0, 0, expiresInDays-90)
	safeguard := model.Safeguard{
		ID: id, Name: "Window safeguard " + key, SafeguardType: "interlock",
		TargetScenarioID: h.scenario.ID, IndependenceKey: key,
		Effectiveness: effectiveness, TestIntervalDays: 90, LastVerifiedAt: &verified,
		LifecycleState: "active", EvidenceNote: "window test",
		CreatedAt: h.now, UpdatedAt: h.now,
	}
	if err := h.safeguards.Create(context.Background(), &safeguard); err != nil {
		t.Fatalf("create safeguard: %v", err)
	}
}

func coverageActor(requestID string) util.Actor {
	return util.Actor{UserID: 9, Username: "engineer", Role: "process_engineer", RequestID: requestID}
}

func TestCoverageRunWithFailureWindowStoresProjection(t *testing.T) {
	h := newCoverageTestHarness(t)
	h.addSafeguard(t, 1, "SIS-WIN-1", 0.6, 10)
	response, duplicate, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 30,
	}, "idem-window-0001", coverageActor("req-window-1"))
	if err != nil {
		t.Fatalf("run evaluation: %v", err)
	}
	if duplicate {
		t.Fatal("first run must not be reported as a duplicate")
	}
	if response.FailureWindowDays != 30 {
		t.Fatalf("failure_window_days = %d, want 30", response.FailureWindowDays)
	}
	var projection dto.FailureProjectionResponse
	if err := json.Unmarshal(response.FailureProjection, &projection); err != nil {
		t.Fatalf("decode projection: %v", err)
	}
	if projection.NoEscalation || projection.EarliestEscalation == nil {
		t.Fatalf("expected earliest escalation, got %#v", projection)
	}
	first := projection.EarliestEscalation
	if first.DaysFromReference != 10 {
		t.Fatalf("earliest escalation offset = %d, want 10", first.DaysFromReference)
	}
	if first.RiskRankBefore != "medium" || first.RiskRankAfter != "critical" {
		t.Fatalf("ranks at escalation = %s -> %s", first.RiskRankBefore, first.RiskRankAfter)
	}
	if len(first.UncoveredPaths) != 1 {
		t.Fatalf("gap paths = %d, want 1", len(first.UncoveredPaths))
	}
	if first.UncoveredPaths[0].Cause != "gas outlet blocked" {
		t.Fatalf("unexpected gap path cause: %q", first.UncoveredPaths[0].Cause)
	}
	if response.Explanation.FailureWindowDays != 30 {
		t.Fatal("explanation must carry the frozen failure window days")
	}
	if response.InputHash == "" {
		t.Fatal("input hash must be populated")
	}
}

func TestCoverageRunFailureWindowDefaultsAndValidation(t *testing.T) {
	h := newCoverageTestHarness(t)
	defaulted, duplicate, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID,
	}, "idem-window-0002", coverageActor("req-window-2"))
	if err != nil || duplicate {
		t.Fatalf("default window run failed: duplicate=%t err=%v", duplicate, err)
	}
	if defaulted.FailureWindowDays != 30 {
		t.Fatalf("default window = %d, want 30", defaulted.FailureWindowDays)
	}
	for _, days := range []int{-1, 366, 1000} {
		_, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
			ScenarioID: h.scenario.ID, FailureWindowDays: days,
		}, "idem-window-bad-"+strconv.Itoa(days), coverageActor("req-window-bad"))
		var appErr *util.AppError
		if !errors.As(err, &appErr) || appErr.Status != 400 {
			t.Fatalf("days=%d must return 400, got %v", days, err)
		}
	}
}

func TestCoverageRunIdempotencyRestoresFrozenProjection(t *testing.T) {
	h := newCoverageTestHarness(t)
	h.addSafeguard(t, 1, "SIS-WIN-2", 0.8, 8)
	request := dto.RunCoverageEvaluationRequest{ScenarioID: h.scenario.ID, FailureWindowDays: 45}
	first, duplicate, err := h.service.Run(context.Background(), request, "idem-window-0003", coverageActor("req-window-3"))
	if err != nil || duplicate {
		t.Fatalf("first run: duplicate=%t err=%v", duplicate, err)
	}
	replayed, duplicate, err := h.service.Run(context.Background(), request, "idem-window-0003", coverageActor("req-window-3"))
	if err != nil || !duplicate {
		t.Fatalf("repeated key must return duplicate: duplicate=%t err=%v", duplicate, err)
	}
	if replayed.ID != first.ID || replayed.InputHash != first.InputHash ||
		string(replayed.FailureProjection) != string(first.FailureProjection) {
		t.Fatal("idempotent replay must restore the same frozen prediction")
	}
}

func TestCoverageRunWindowChangesInputHash(t *testing.T) {
	h := newCoverageTestHarness(t)
	h.addSafeguard(t, 1, "SIS-WIN-3", 0.6, 20)
	short, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 10,
	}, "idem-window-0006", coverageActor("req-window-6"))
	if err != nil {
		t.Fatalf("short window run: %v", err)
	}
	long, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 60,
	}, "idem-window-0007", coverageActor("req-window-7"))
	if err != nil {
		t.Fatalf("long window run: %v", err)
	}
	if short.InputHash == long.InputHash {
		t.Fatal("the failure window participates in the snapshot, so hashes must differ")
	}
}

func TestCoverageCompareIncludesFrozenWindows(t *testing.T) {
	h := newCoverageTestHarness(t)
	h.addSafeguard(t, 1, "SIS-WIN-4", 0.6, 12)
	shortWindow, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 10,
	}, "idem-window-0004", coverageActor("req-window-4"))
	if err != nil {
		t.Fatalf("short window run: %v", err)
	}
	longWindow, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 30,
	}, "idem-window-0005", coverageActor("req-window-5"))
	if err != nil {
		t.Fatalf("long window run: %v", err)
	}
	comparison, err := h.service.Compare(context.Background(), shortWindow.ID, longWindow.ID)
	if err != nil {
		t.Fatalf("compare: %v", err)
	}
	if comparison.BaseWindow.WindowDays != 10 || comparison.ComparedWindow.WindowDays != 30 {
		t.Fatalf("window days not restored in comparison: %#v / %#v", comparison.BaseWindow, comparison.ComparedWindow)
	}
	if !comparison.BaseWindow.NoEscalation || comparison.BaseWindow.EarliestEscalation != nil {
		t.Fatal("day-12 expiry must be outside the 10 day window and show no escalation")
	}
	if comparison.ComparedWindow.NoEscalation || comparison.ComparedWindow.EarliestEscalation == nil {
		t.Fatal("day-12 expiry must escalate inside the 30 day window")
	}
	if !comparison.FailureProjectionChanged {
		t.Fatal("projections with different windows must be flagged as changed")
	}
}

func TestCoverageReplayVerifiesStoredProjection(t *testing.T) {
	h := newCoverageTestHarness(t)
	h.addSafeguard(t, 1, "SIS-WIN-5", 0.6, 15)
	run, _, err := h.service.Run(context.Background(), dto.RunCoverageEvaluationRequest{
		ScenarioID: h.scenario.ID, FailureWindowDays: 30,
	}, "idem-window-0008", coverageActor("req-window-8"))
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	replayed, err := h.service.Replay(context.Background(), run.ID, coverageActor("req-window-replay"))
	if err != nil {
		t.Fatalf("replay must reproduce the frozen projection: %v", err)
	}
	if replayed.ReplayPassed == nil || !*replayed.ReplayPassed {
		t.Fatal("deterministic replay must pass for the stored failure window prediction")
	}
}
