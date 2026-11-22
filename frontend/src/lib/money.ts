/** Money helpers. All amounts travel and are stored as integer cents. */

/** ISO code of the currency used by the salon. */
export const CURRENCY = 'USD'
/** Human readable currency label used in copy and price tags. */
export const CURRENCY_LABEL = 'USD'
/** `es-MX` keeps the `$1,234.55` shape the salon prints (comma groups, dot cents). */
export const LOCALE = 'es-MX'

function assertCents(value: number): void {
  if (!Number.isInteger(value) || !Number.isFinite(value)) {
    throw new TypeError(`Los montos se expresan en centavos enteros, recibido: ${value}`)
  }
}

/** Formats integer cents as `$1,234.55` (es-MX grouping, never a float). */
export function formatCents(cents: number): string {
  assertCents(cents)
  const negative = cents < 0
  const abs = Math.abs(cents)
  const units = Math.trunc(abs / 100)
  const fraction = abs % 100
  const grouped = String(units).replace(/\B(?=(\d{3})+(?!\d))/g, ',')
  const body = `${grouped}.${String(fraction).padStart(2, '0')}`
  return `${negative ? '-' : ''}$${body}`
}

/** Sums integer cents without ever passing through a float. */
export function sumCents(values: readonly number[]): number {
  let total = 0
  for (const value of values) {
    assertCents(value)
    total += value
  }
  return total
}

/**
 * Parses what a receptionist types (`350`, `350.5`, `$1,234.55`) into cents.
 * Returns null instead of guessing when the input is not a valid amount.
 */
export function parseAmountToCents(input: string): number | null {
  const cleaned = input.trim().replace(/\$/g, '').replace(/\s/g, '').replace(/,/g, '')
  if (!/^\d+(\.\d{1,2})?$/.test(cleaned)) {
    return null
  }
  const [units = '0', fraction = ''] = cleaned.split('.')
  return Number(units) * 100 + Number(fraction.padEnd(2, '0'))
}

/** `percentOf(35000, 12.5)` → 4375 cents, rounded to a whole cent. */
export function percentOf(cents: number, percent: number): number {
  assertCents(cents)
  return Math.round((cents * percent) / 100)
}

/** Renders a service duration (`90` → `1 h 30 min`). */
export function formatMinutes(minutes: number): string {
  if (!Number.isFinite(minutes) || minutes <= 0) {
    return '—'
  }
  const hours = Math.floor(minutes / 60)
  const rest = minutes % 60
  if (hours === 0) {
    return `${rest} min`
  }
  return rest === 0 ? `${hours} h` : `${hours} h ${rest} min`
}
