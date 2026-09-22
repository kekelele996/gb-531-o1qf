import type { CoverageEvaluation, FailureWindowEvent, FailureWindowForecast } from '../types/coverage-evaluation'

export const DEFAULT_FAILURE_WINDOW_DAYS = 30
export const MIN_FAILURE_WINDOW_DAYS = 1
export const MAX_FAILURE_WINDOW_DAYS = 365

export function normalizeWindowDays(value: number | undefined): number {
  if (!value || value <= 0) return DEFAULT_FAILURE_WINDOW_DAYS
  if (value > MAX_FAILURE_WINDOW_DAYS) return MAX_FAILURE_WINDOW_DAYS
  return value
}

/** A frozen evaluation always carries a forecast; older records fall back to an empty one. */
export function forecastOf(evaluation: CoverageEvaluation | undefined): FailureWindowForecast | undefined {
  return evaluation?.failure_forecast
}

export function firstEscalation(evaluation: CoverageEvaluation | undefined): FailureWindowEvent | undefined {
  const forecast = forecastOf(evaluation)
  return forecast?.escalation_within_window ? forecast.first_escalation : undefined
}

export function formatForecastDate(value: string | undefined): string {
  if (!value) return '—'
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return '—'
  return date.toLocaleDateString('zh-CN', { year: 'numeric', month: '2-digit', day: '2-digit' })
}
