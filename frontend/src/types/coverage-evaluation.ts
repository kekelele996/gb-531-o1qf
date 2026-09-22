import type { CoverageState } from './enums/coverage-state'

export interface PathEvidence {
  path_id?: string
  node_code?: string
  cause: string
  consequence: string
  safeguard_ids?: number[]
  safeguard_names?: string[]
  independence_keys?: string[]
  combined_protection?: number
  covered?: boolean
  protected?: boolean
  path_score?: number
  reason?: string
}

export interface ScoringStep {
  step?: number
  rule?: string
  input?: string
  contribution?: number
  running_score?: number
  explanation?: string
  label?: string
  value?: number | string
  detail?: string
}

export interface DeduplicatedSafeguard {
  independence_key: string
  kept_id: number
  ignored_ids: number[]
  reason: string
}

export interface FailureEvent {
  expires_at: string
  days_from_reference: number
  expiring_safeguard_ids: number[]
  coverage_score: number
  risk_rank_before: string
  risk_rank_after: string
  risk_rank_rises: boolean
  uncovered_paths: PathEvidence[]
}

export interface FailureProjection {
  window_days: number
  window_end: string
  baseline_coverage_score: number
  baseline_rank_after: string
  window_end_coverage_score: number
  window_end_rank_after: string
  no_escalation: boolean
  earliest_escalation?: FailureEvent
  events: FailureEvent[]
}

export interface FailureWindowSummary {
  window_days: number
  window_end?: string
  no_escalation: boolean
  earliest_escalation?: FailureEvent
  window_end_coverage_score: number
  window_end_rank_after: string
}

export interface EvaluationComparison {
  base_id: number
  compared_id: number
  score_delta: number
  uncovered_path_delta: number
  risk_rank_changed: boolean
  input_changed: boolean
  base_window: FailureWindowSummary
  compared_window: FailureWindowSummary
  failure_projection_changed: boolean
}

export interface EvaluationExplanation {
  summary: string
  paths: PathEvidence[]
  score_steps: ScoringStep[]
  deduplicated_safeguards: DeduplicatedSafeguard[]
  boundary_note: string
  reference_time: string
  failure_window_days?: number
}

export interface CoverageSnapshot {
  scenario_id?: number
  scenario_version?: number
  causes?: string[]
  consequences?: string[]
  safeguards?: unknown[]
  input_hash?: string
}

export interface CoverageEvaluation {
  id: number
  scenario_id: number
  algorithm_version: string
  input_snapshot: CoverageSnapshot | string
  coverage_score: number
  uncovered_paths: PathEvidence[] | string
  risk_rank_before: string
  risk_rank_after: string
  evaluation_state: CoverageState
  explanation: EvaluationExplanation | string
  failure_window_days: number
  failure_projection: FailureProjection | string
  evaluated_by: number
  evaluated_by_name?: string
  evaluated_at: string
  input_hash?: string
  deduplicated_safeguards?: DeduplicatedSafeguard[]
  duration_milliseconds?: number
  determinism_replay_passed?: boolean
}

export const DEFAULT_FAILURE_WINDOW_DAYS = 30
export const MIN_FAILURE_WINDOW_DAYS = 1
export const MAX_FAILURE_WINDOW_DAYS = 365

export interface CoverageRunInput {
  scenario_id: number
  failure_window_days: number
}
