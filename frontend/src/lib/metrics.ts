/** Normalisation of the dashboard aggregates, tolerant to number/ratio variants. */

export type TopService = {
  name: string
  count: number
  revenue_cents: number | null
}

export type DashboardMetrics = {
  revenue_today: number
  appointments_today: number
  occupancy_rate: number | null
  top_services: TopService[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function readNumber(row: Record<string, unknown>, keys: readonly string[]): number | null {
  for (const key of keys) {
    const value = row[key]
    if (typeof value === 'number' && Number.isFinite(value)) {
      return value
    }
    if (typeof value === 'string' && /^-?\d+(\.\d+)?$/.test(value.trim())) {
      return Number(value)
    }
  }
  return null
}

function readString(row: Record<string, unknown>, keys: readonly string[]): string | null {
  for (const key of keys) {
    const value = row[key]
    if (typeof value === 'string' && value.trim() !== '') {
      return value
    }
  }
  return null
}

/** The API documents `occupancy_rate`; accepts both a 0..1 ratio and a 0..100 percentage. */
export function occupancyFraction(value: number | null | undefined): number {
  if (typeof value !== 'number' || !Number.isFinite(value) || value <= 0) {
    return 0
  }
  const ratio = value > 1 ? value / 100 : value
  return Math.min(1, ratio)
}

export function formatOccupancy(value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) {
    return '—'
  }
  return `${Math.round(occupancyFraction(value) * 100)}%`
}

/** Maps `top_services` rows into something renderable, dropping unusable ones. */
export function normalizeTopServices(value: unknown): TopService[] {
  if (!Array.isArray(value)) {
    return []
  }
  const rows: TopService[] = []
  for (const entry of value) {
    if (!isRecord(entry)) {
      continue
    }
    const name = readString(entry, ['name', 'service_name', 'service'])
    if (name === null) {
      continue
    }
    rows.push({
      name,
      count: readNumber(entry, ['count', 'bookings', 'sales', 'qty']) ?? 0,
      revenue_cents: readNumber(entry, ['revenue_cents', 'total_cents', 'revenue']),
    })
  }
  return rows
}

/** Reads the dashboard payload defensively; unknown fields become stable defaults. */
export function normalizeDashboard(payload: unknown): DashboardMetrics {
  const row = isRecord(payload) ? payload : {}
  return {
    revenue_today: readNumber(row, ['revenue_today', 'revenue_today_cents']) ?? 0,
    appointments_today: readNumber(row, ['appointments_today', 'appointments']) ?? 0,
    occupancy_rate: readNumber(row, ['occupancy_rate']),
    top_services: normalizeTopServices(row['top_services']),
  }
}
