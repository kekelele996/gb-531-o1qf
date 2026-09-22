import { describe, expect, it } from 'vitest'
import type { CoverageEvaluation } from '../types/coverage-evaluation'
import { DEFAULT_FAILURE_WINDOW_DAYS, MAX_FAILURE_WINDOW_DAYS, firstEscalation, formatForecastDate, normalizeWindowDays } from './forecast'

describe('normalizeWindowDays', () => {
  it('defaults empty, zero and negative values to 30 days', () => {
    expect(normalizeWindowDays(undefined)).toBe(DEFAULT_FAILURE_WINDOW_DAYS)
    expect(normalizeWindowDays(0)).toBe(30)
    expect(normalizeWindowDays(-5)).toBe(30)
  })

  it('keeps the 1..365 range and clamps larger values', () => {
    expect(normalizeWindowDays(1)).toBe(1)
    expect(normalizeWindowDays(365)).toBe(365)
    expect(normalizeWindowDays(MAX_FAILURE_WINDOW_DAYS + 1)).toBe(MAX_FAILURE_WINDOW_DAYS)
  })
})

describe('firstEscalation', () => {
  const base = { id: 1 } as unknown as CoverageEvaluation

  it('returns the earliest escalation event when one is projected', () => {
    const event = { horizon_days: 12, risk_rank_after: 'high' }
    const evaluation = {
      ...base,
      failure_forecast: { window_days: 30, window_end: '2026-10-01T00:00:00Z', forecast_note: '', escalation_within_window: true, first_escalation: event, events: [event] },
    } as unknown as CoverageEvaluation
    expect(firstEscalation(evaluation)?.horizon_days).toBe(12)
  })

  it('returns undefined when the window shows no rank increase', () => {
    const evaluation = {
      ...base,
      failure_forecast: { window_days: 30, window_end: '2026-10-01T00:00:00Z', forecast_note: '', escalation_within_window: false, events: [] },
    } as unknown as CoverageEvaluation
    expect(firstEscalation(evaluation)).toBeUndefined()
  })
})

describe('formatForecastDate', () => {
  it('formats valid ISO dates and falls back for missing values', () => {
    expect(formatForecastDate('2026-09-13T00:00:00Z')).toMatch(/2026/)
    expect(formatForecastDate(undefined)).toBe('—')
    expect(formatForecastDate('not-a-date')).toBe('—')
  })
})
