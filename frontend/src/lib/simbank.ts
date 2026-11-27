/**
 * Simulated bank of contract F11 (D6, D13-c): the public `/pagar` page and its pure
 * rules. The bank is a separate service; this module is only its client plus the
 * formatting of the card form, so the card rules live in one place and are tested
 * without a DOM.
 *
 * Rules of D6 pinned here:
 * - the card number is sent as DIGITS ONLY, inside the body of
 *   `POST /api/v1/simbank/charges/pay`; it never goes in a URL, a header, storage,
 *   the console or a log, and the page keeps it only in React state;
 * - Luhn is a UX HINT only: a number that fails it can still be sent, because the
 *   server is the one that decides (`400 Datos de tarjeta invalidos`);
 * - every server sentence is shown verbatim («El pago vencio», «Este pago ya se
 *   aprobo», «Demasiados intentos», «Datos de tarjeta invalidos»).
 *
 * Online only: nothing here is queued offline.
 */

import { ApiError, NetworkError, request } from './api.ts'
import { createPackagesGate, type PackagesGate } from '../features/packages/lib/packages.ts'

// --- the two public routes of the payment (D13-c) ---

export const PAYMENT_ROUTE = '/pagar'
export const RESULT_ROUTE = '/pago-resultado'

/** Public paths (no session): a 401 anywhere must NOT send them to /login. */
export const BANK_PATHS: readonly string[] = [PAYMENT_ROUTE, RESULT_ROUTE]

/** The permanent banner of the simulated bank (D13-c, verbatim). */
export const SIMULATED_BANNER = 'PAGO SIMULADO - no se cobra dinero real'

export function isPaymentPath(pathname: string): boolean {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return BANK_PATHS.includes(clean)
}

/** `/pagar#<charge_id>` as the app builds it (the id travels in the fragment, D6). */
export function payPath(chargeId: string): string {
  return `${PAYMENT_ROUTE}#${chargeId}`
}

/** `/pago-resultado#<charge_id>`: the link «Volver al salón» of the bank. */
export function resultPath(chargeId: string): string {
  return `${RESULT_ROUTE}#${chargeId}`
}

// --- messages ---

/**
 * Sentences of the bank. The four first ones are the contract's (D6) and are shown
 * exactly as the server sends them; the rest are the page's own copy.
 */
export const SIMBANK_MESSAGES = {
  expired: 'El pago vencio',
  alreadyApproved: 'Este pago ya se aprobo',
  tooMany: 'Demasiados intentos',
  invalidCard: 'Datos de tarjeta invalidos',
  numberHint: 'Escribí los 16 dígitos de la tarjeta de prueba',
  expiryHint: 'El vencimiento va como MM/AA',
  cvcHint: 'El código de seguridad tiene 3 o 4 dígitos',
  nameHint: 'Escribí el nombre como figura en la tarjeta',
  luhnHint:
    'El número no pasa la verificación de Luhn: el banco probablemente lo rechace. Podés enviarlo igual.',
  network: 'No pudimos conectar con el banco simulado. Revisá tu conexión e intentá de nuevo.',
  server: 'No pudimos completar el pago.',
  unknownCharge: 'El banco no devolvió el detalle del cobro.',
  unknownResult: 'El banco no devolvió el resultado del pago.',
} as const

/** Test cards of D6: the three buttons that fill the form. */
export type TestCard = {
  /** Digits only, what the form receives. */
  digits: string
  /** How it reads in the button and in the field. */
  number: string
  label: string
  outcome: 'approved' | 'declined' | 'insufficient'
}

export const TEST_CARDS: readonly TestCard[] = [
  { digits: '4242424242424242', number: '4242 4242 4242 4242', label: 'Aprobada', outcome: 'approved' },
  { digits: '4000000000000002', number: '4000 0000 0000 0002', label: 'Rechazada', outcome: 'declined' },
  { digits: '4000000000009995', number: '4000 0000 0000 9995', label: 'Sin fondos', outcome: 'insufficient' },
]

// --- one payment at a time ---

/** Synchronous gate of the pay button (the same one as packages, loyalty and 2FA). */
export type SimbankGate = PackagesGate
export const createSimbankGate: () => SimbankGate = createPackagesGate

// --- the card form ---

/** Longest PAN the form accepts (ISO/IEC 7812 allows up to 19 digits). */
const MAX_CARD_DIGITS = 19
const MIN_CARD_DIGITS = 12
const MIN_NAME_LENGTH = 2
const MAX_NAME_LENGTH = 40

/** Digits only, capped: what travels to the bank and what the form groups. */
export function cardDigits(input: string): string {
  const raw = typeof input === 'string' ? input : ''
  return raw.replace(/[^0-9]/g, '').slice(0, MAX_CARD_DIGITS)
}

/** `4242424242424242` → `4242 4242 4242 4242` as the person types. */
export function formatCardNumber(input: string): string {
  const digits = cardDigits(input)
  return digits.replace(/(.{4})/g, '$1 ').trim()
}

/** `1227` → `12/27`: the slash is inserted and the year stopped at two digits. */
export function formatExpiry(input: string): string {
  const digits = cardDigits(input).slice(0, 4)
  if (digits.length <= 2) {
    return digits
  }
  return `${digits.slice(0, 2)}/${digits.slice(2)}`
}

export type Expiry = { month: number; year: number }

/** Reads `MM/AA`; `null` when the month is impossible or the shape is not `MM/AA`. */
export function parseExpiry(input: string): Expiry | null {
  const match = /^(\d{2})\/(\d{2})$/.exec(formatExpiry(input))
  if (!match) {
    return null
  }
  const month = Number(match[1])
  const year = Number(match[2])
  if (month < 1 || month > 12 || !Number.isSafeInteger(year)) {
    return null
  }
  return { month, year }
}

/**
 * Luhn check digit of the number. It is a UX HINT: the server decides the outcome
 * of the payment and answers `Datos de tarjeta invalidos` for a malformed one.
 */
export function luhnValid(digits: string): boolean {
  const clean = typeof digits === 'string' ? digits : ''
  if (!/^\d+$/.test(clean)) {
    return false
  }
  let sum = 0
  let double = false
  for (let index = clean.length - 1; index >= 0; index -= 1) {
    const digit = clean.charCodeAt(index) - 48
    const value = double ? digit * 2 : digit
    sum += value > 9 ? value - 9 : value
    double = !double
  }
  return sum % 10 === 0
}

/** What the card form holds: text as typed, never grouped or normalized in place. */
export type CardDraft = { number: string; exp: string; cvc: string; name: string }

/** The card as the bank receives it: digits only, `MM/AA`, trimmed name. */
export type SimbankCard = { number: string; exp: string; cvc: string; name: string }

export type CardField = 'number' | 'exp' | 'cvc' | 'name'

export type CardCheck =
  | { ok: true; card: SimbankCard; luhnHint: string | null }
  | { ok: false; field: CardField; message: string }

/**
 * Reads the card form. Only the shape is checked here (16-ish digits, `MM/AA`, 3 or
 * 4 digits of CVC, a name): a number that fails Luhn still travels with its hint,
 * because the bank is the one that decides if the card works (D6).
 */
export function validateCardDraft(draft: CardDraft): CardCheck {
  const number = cardDigits(draft.number)
  if (number.length < MIN_CARD_DIGITS || number.length > MAX_CARD_DIGITS) {
    return { ok: false, field: 'number', message: SIMBANK_MESSAGES.numberHint }
  }
  const exp = formatExpiry(draft.exp)
  if (parseExpiry(exp) === null) {
    return { ok: false, field: 'exp', message: SIMBANK_MESSAGES.expiryHint }
  }
  const cvc = cardDigits(draft.cvc)
  if (cvc.length < 3 || cvc.length > 4) {
    return { ok: false, field: 'cvc', message: SIMBANK_MESSAGES.cvcHint }
  }
  const name = (typeof draft.name === 'string' ? draft.name : '').trim()
  if (name.length < MIN_NAME_LENGTH || name.length > MAX_NAME_LENGTH) {
    return { ok: false, field: 'name', message: SIMBANK_MESSAGES.nameHint }
  }
  return {
    ok: true,
    card: { number, exp, cvc, name },
    luhnHint: luhnValid(number) ? null : SIMBANK_MESSAGES.luhnHint,
  }
}

// --- charges: lookup, pay, result ---

export type ChargeStatus = 'pending' | 'approved' | 'declined' | 'insufficient' | 'refunded'

const CHARGE_STATUSES: readonly ChargeStatus[] = [
  'pending',
  'approved',
  'declined',
  'insufficient',
  'refunded',
]

function isStatus(value: string): value is ChargeStatus {
  return (CHARGE_STATUSES as readonly string[]).includes(value)
}

/** `POST /simbank/charges/lookup` (D6): what the client is about to pay. */
export type ChargeLookup = {
  amountCents: number
  description: string
  status: string
  expiresAt: string | null
  simulated: boolean
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function instant(value: unknown): string | null {
  const raw = text(value)
  return raw !== '' && !Number.isNaN(Date.parse(raw)) ? raw : null
}

function isInt(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value)
}

export function normalizeChargeLookup(raw: unknown): ChargeLookup | null {
  if (!isRecord(raw)) {
    return null
  }
  const amount = raw['amount_cents']
  const status = text(raw['status'])
  if (!isInt(amount) || amount <= 0 || status === '') {
    return null
  }
  return {
    amountCents: amount,
    description: text(raw['description']),
    status,
    expiresAt: instant(raw['expires_at']),
    simulated: raw['simulated'] === true,
  }
}

/** `POST /simbank/charges/pay` (D6): the outcome the page paints. */
export type PayResult = {
  status: ChargeStatus | 'unknown'
  message: string | null
  amountCents: number | null
  /** Only the last four digits ever come back (the number is not stored). */
  last4: string | null
  cardBrand: string | null
}

/** Reads the outcome of a payment without inventing it; `null` without a body. */
export function normalizePayResult(raw: unknown): PayResult | null {
  if (!isRecord(raw)) {
    return null
  }
  const source = isRecord(raw['charge'])
    ? raw['charge']
    : isRecord(raw['result'])
      ? raw['result']
      : raw
  const status = text(source['status']) || text(raw['status'])
  const message = text(source['message']) || text(source['detail']) || text(source['error']) || text(raw['message'])
  const amount = isInt(source['amount_cents']) ? source['amount_cents'] : isInt(raw['amount_cents']) ? raw['amount_cents'] : null
  const last4 = text(source['last4']) || text(raw['last4'])
  const brand = text(source['brand']) || text(source['card_brand']) || text(raw['brand'])
  return {
    status: isStatus(status) ? status : 'unknown',
    message: message === '' ? null : message,
    amountCents: amount,
    last4: last4 === '' ? null : last4,
    cardBrand: brand === '' ? null : brand,
  }
}

/** `POST /simbank/charges/lookup` — anonymous: the bank has no session. */
export async function lookupCharge(
  chargeId: string,
  options: { fetchImpl?: typeof fetch; signal?: AbortSignal } = {},
): Promise<ChargeLookup> {
  const charge = normalizeChargeLookup(
    await request<unknown>('/api/v1/simbank/charges/lookup', {
      method: 'POST',
      body: { charge_id: chargeId },
      auth: 'none',
      ...(options.fetchImpl ? { fetchImpl: options.fetchImpl } : {}),
      ...(options.signal ? { signal: options.signal } : {}),
    }),
  )
  if (!charge) {
    throw new ApiError(200, SIMBANK_MESSAGES.unknownCharge)
  }
  return charge
}

/**
 * `POST /simbank/charges/pay`: the card travels in the BODY with digits only. A
 * second call while one is in flight is refused by the screen's gate, never here.
 */
export async function payCharge(
  chargeId: string,
  card: SimbankCard,
  options: { fetchImpl?: typeof fetch; signal?: AbortSignal } = {},
): Promise<PayResult> {
  const result = normalizePayResult(
    await request<unknown>('/api/v1/simbank/charges/pay', {
      method: 'POST',
      body: {
        charge_id: chargeId,
        card: {
          number: cardDigits(card.number),
          exp: formatExpiry(card.exp),
          cvc: cardDigits(card.cvc),
          name: card.name.trim(),
        },
      },
      auth: 'none',
      ...(options.fetchImpl ? { fetchImpl: options.fetchImpl } : {}),
      ...(options.signal ? { signal: options.signal } : {}),
    }),
  )
  if (!result) {
    throw new ApiError(200, SIMBANK_MESSAGES.unknownResult)
  }
  return result
}

export type SimbankFailureKind =
  | 'expired'
  | 'already_approved'
  | 'conflict'
  | 'too_many'
  | 'invalid_card'
  | 'not_found'
  | 'network'
  | 'server'

export type SimbankFailure = {
  kind: SimbankFailureKind
  /** What the page prints. A server sentence is never rewritten. */
  message: string
  /** True when the sentence is the server's own. */
  verbatim: boolean
  /** True when retrying the same card can change the outcome. */
  retryable: boolean
}

/**
 * Reads a failed request of the bank. The 409 of D6 carries two different
 * sentences, so the kind is decided by the sentence the server sent; the message is
 * always printed as it arrived.
 */
export function describeSimbankError(error: unknown): SimbankFailure {
  if (error instanceof ApiError) {
    const message = error.message
    if (error.status === 409) {
      const kind: SimbankFailureKind = /venci/i.test(message)
        ? 'expired'
        : /aprob/i.test(message)
          ? 'already_approved'
          : 'conflict'
      return { kind, message, verbatim: true, retryable: false }
    }
    if (error.status === 429) {
      return { kind: 'too_many', message, verbatim: true, retryable: false }
    }
    if (error.status === 400) {
      return { kind: 'invalid_card', message, verbatim: true, retryable: true }
    }
    if (error.status === 404) {
      return { kind: 'not_found', message, verbatim: true, retryable: false }
    }
    return { kind: 'server', message, verbatim: true, retryable: true }
  }
  if (error instanceof NetworkError || error instanceof TypeError) {
    return { kind: 'network', message: SIMBANK_MESSAGES.network, verbatim: false, retryable: true }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { kind: 'server', message: error.message, verbatim: false, retryable: true }
  }
  return { kind: 'server', message: SIMBANK_MESSAGES.server, verbatim: false, retryable: true }
}

const RESULT_TEXTS: Record<ChargeStatus, string> = {
  pending: 'Pago pendiente',
  approved: 'Pago aprobado',
  declined: 'Tarjeta rechazada',
  insufficient: 'Fondos insuficientes',
  refunded: 'Pago devuelto',
}

/** Spanish sentence of an outcome; an unknown one says there is none. */
export function chargeResultText(status: string): string {
  return isStatus(status) ? RESULT_TEXTS[status] : 'Sin resultado'
}

/** The page shows the approved state with its own copy and a way back to the salon. */
export function isApproved(status: string): boolean {
  return status === 'approved'
}
