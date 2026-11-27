/**
 * Two-step verification (TOTP) of the account (contract F10, D7, D8, D11, D12).
 * Pure rules plus the thin API client: the second step of the login, the
 * «Seguridad» page and the login flow render what these return. The server is the
 * authority on codes, lockouts and expiry; nothing here checks a code.
 *
 * Online only (D12): nothing in this module touches the offline queue. Offline the
 * action is not sent, and a network error says so; a queued 2FA change would be
 * replayed later with a code that no longer works.
 *
 * Secrets never leave memory: the challenge, the setup secret and the recovery codes
 * live in React state only (no storage, no logs).
 */

import { ApiError, NetworkError, request, narrowLoginResponse } from './api.ts'
import { isBrowserOnline } from './connectivity.ts'
import { createPackagesGate, type PackagesGate } from '../features/packages/lib/packages.ts'

export { narrowLoginResponse }
export type { LoginResult } from './api.ts'

/** Longest code the field accepts: a recovery code is `XXXXX-XXXXX` (11), spaces allowed. */
export const MAX_VERIFICATION_CODE_LENGTH = 24

/** Sentences of this module. The ones of the server are never rewritten (printed verbatim). */
export const TWO_FACTOR_MESSAGES = {
  codeRequired: 'Escribí el código de verificación',
  codeTooLong: 'El código es demasiado largo',
  setupCodeDigits: 'El código tiene 6 dígitos',
  passwordRequired: 'Escribí tu contraseña',
  offline:
    'Sin conexión. La seguridad de la cuenta solo se cambia en línea: esta operación no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.',
  busy: 'Ya se está procesando una operación: esperá a que termine.',
  network:
    'No pudimos conectar con el servidor, así que no sabemos si la operación llegó. No quedó en cola ni se guardó para más tarde.',
  loginFallback: 'No pudimos verificar el código. Intentá de nuevo.',
  noToken: 'La respuesta de inicio de sesión no incluyó un token.',
  invalidStatus: 'La API no devolvió el estado de la verificación en dos pasos.',
  invalidSetup: 'La API no devolvió el código de configuración.',
  invalidCodes: 'La API no devolvió los códigos de recuperación.',
  /** The invitation page logs in a person who may have 2FA: it cannot ask for the code. */
  invitationNeedsSignIn:
    'Esta cuenta tiene la verificación en dos pasos activada. Iniciá sesión desde la pantalla de acceso y después volvé a abrir el enlace de la invitación.',
} as const

// --- the code of the second step ---

/**
 * What the person typed, ready to send: every space (also inside a pasted
 * «123 456») is dropped and letters go to upper case, which is how recovery codes
 * are issued. The server does the verification.
 */
export function normalizeVerificationCode(input: string): string {
  return input.replace(/\s+/g, '').toUpperCase()
}

export type CodeCheck = { ok: true; code: string } | { ok: false; message: string }

/** A 6-digit code or a recovery code: only «something» and «not absurdly long» are checked here. */
export function validateVerificationCode(input: string): CodeCheck {
  const code = normalizeVerificationCode(input)
  if (code === '') {
    return { ok: false, message: TWO_FACTOR_MESSAGES.codeRequired }
  }
  if (code.length > MAX_VERIFICATION_CODE_LENGTH) {
    return { ok: false, message: TWO_FACTOR_MESSAGES.codeTooLong }
  }
  return { ok: true, code }
}

/** Activating asks for a TOTP code: exactly six digits (a recovery code does not exist yet). */
export function validateSetupCode(input: string): CodeCheck {
  const code = normalizeVerificationCode(input)
  if (code === '') {
    return { ok: false, message: TWO_FACTOR_MESSAGES.codeRequired }
  }
  if (!/^\d{6}$/.test(code)) {
    return { ok: false, message: TWO_FACTOR_MESSAGES.setupCodeDigits }
  }
  return { ok: true, code }
}

export type DisableDraft = { password: string; code: string }

export type DisableCheck =
  | { ok: true; value: DisableDraft }
  | { ok: false; message: string }

/** Disabling needs the password (sent exactly as typed) and a TOTP or recovery code. */
export function validateDisableDraft(draft: DisableDraft): DisableCheck {
  if (draft.password === '') {
    return { ok: false, message: TWO_FACTOR_MESSAGES.passwordRequired }
  }
  const code = validateVerificationCode(draft.code)
  if (!code.ok) {
    return code
  }
  return { ok: true, value: { password: draft.password, code: code.code } }
}

// --- failure of the second step of the login ---

export type ChallengeFailure = {
  /** `credentials` = back to the first step (the challenge is gone); `code` = stay. */
  next: 'credentials' | 'code'
  message: string
}

/**
 * A 401 of `POST /auth/login/2fa` is uniform on the server: invalid, expired or
 * wrong code are the same sentence. The challenge cannot be trusted any more, so
 * the form goes back to the first step and prints the server message. Anything else
 * (a lockout 429, a 400, no network) keeps the step so nothing typed is lost.
 */
export function challengeFailure(error: unknown): ChallengeFailure {
  if (error instanceof ApiError) {
    return { next: error.status === 401 ? 'credentials' : 'code', message: error.message }
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return { next: 'code', message: error.message }
  }
  return { next: 'code', message: TWO_FACTOR_MESSAGES.loginFallback }
}

// --- normalizers ---

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

export type TwoFactorStatus = { enabled: boolean }

/** `GET /me/2fa` -> `{enabled}`. Anything else is not a status. */
export function normalizeTwoFactorStatus(raw: unknown): TwoFactorStatus | null {
  if (!isRecord(raw) || typeof raw['enabled'] !== 'boolean') {
    return null
  }
  return { enabled: raw['enabled'] }
}

export type TwoFactorSetup = { secret: string; otpauth_url: string }

/** `POST /me/2fa/setup` -> `{secret, otpauth_url}`, both non-empty. */
export function normalizeTwoFactorSetup(raw: unknown): TwoFactorSetup | null {
  if (!isRecord(raw)) {
    return null
  }
  const secret = typeof raw['secret'] === 'string' ? raw['secret'].trim() : ''
  const url = typeof raw['otpauth_url'] === 'string' ? raw['otpauth_url'].trim() : ''
  if (secret === '' || url === '') {
    return null
  }
  return { secret, otpauth_url: url }
}

/**
 * `POST /me/2fa/enable` -> `{recovery_codes:[...]}`. They are shown once and cannot
 * be recovered, so a list with a bad item is rejected instead of shown partially.
 */
export function normalizeRecoveryCodes(raw: unknown): string[] | null {
  if (!isRecord(raw) || !Array.isArray(raw['recovery_codes'])) {
    return null
  }
  const codes: string[] = []
  for (const item of raw['recovery_codes'] as unknown[]) {
    if (typeof item !== 'string' || item.trim() === '') {
      return null
    }
    codes.push(item.trim())
  }
  return codes.length > 0 ? codes : null
}

// --- API client (frozen shapes of the F10 contract) ---

/** `GET /me/2fa`. */
export async function fetchTwoFactorStatus(signal?: AbortSignal): Promise<TwoFactorStatus> {
  const status = normalizeTwoFactorStatus(
    await request<unknown>('/api/v1/me/2fa', signal ? { signal } : {}),
  )
  if (!status) {
    throw new ApiError(200, TWO_FACTOR_MESSAGES.invalidStatus)
  }
  return status
}

/** `POST /me/2fa/setup`: a new pending secret (replaces a previous pending one). */
export async function startTwoFactorSetup(): Promise<TwoFactorSetup> {
  const setup = normalizeTwoFactorSetup(
    await request<unknown>('/api/v1/me/2fa/setup', { method: 'POST' }),
  )
  if (!setup) {
    throw new ApiError(200, TWO_FACTOR_MESSAGES.invalidSetup)
  }
  return setup
}

/** `POST /me/2fa/enable {code}` -> the 8 recovery codes, shown once. */
export async function enableTwoFactor(code: string): Promise<string[]> {
  const codes = normalizeRecoveryCodes(
    await request<unknown>('/api/v1/me/2fa/enable', { method: 'POST', body: { code } }),
  )
  if (!codes) {
    throw new ApiError(200, TWO_FACTOR_MESSAGES.invalidCodes)
  }
  return codes
}

/** `POST /me/2fa/disable {password, code}` -> 204. */
export async function disableTwoFactor(draft: DisableDraft): Promise<void> {
  await request<unknown>('/api/v1/me/2fa/disable', {
    method: 'POST',
    body: { password: draft.password, code: draft.code },
    // A wrong password answers 401 `Credenciales invalidas`: the session is fine.
    keepSessionOn401: true,
  })
}

/**
 * `POST /auth/login/2fa {challenge, code}` -> the session token. It does NOT store
 * it: the screen does, only after this call succeeds.
 */
export async function completeLogin2fa(challenge: string, code: string): Promise<string> {
  const result = narrowLoginResponse(
    await request<unknown>('/api/v1/auth/login/2fa', {
      method: 'POST',
      body: { challenge, code },
      auth: 'none',
      // A wrong or expired code answers 401: it is a step of the login, not an
      // expired session, so the app must not purge caches or navigate.
      keepSessionOn401: true,
    }),
  )
  if (!result || result.kind !== 'token') {
    throw new ApiError(200, TWO_FACTOR_MESSAGES.noToken)
  }
  return result.token
}

// --- one write at a time, online only (D12) ---

/** Synchronous gate of the screens (same one as the packages): the state only paints the button. */
export type SecurityGate = PackagesGate
export const createSecurityGate: () => SecurityGate = createPackagesGate

export type SecurityOutcome<T> =
  | { status: 'ok'; value: T }
  | { status: 'busy'; message: string }
  /** Nothing was sent: the browser is offline. */
  | { status: 'offline'; message: string }
  /** Sent, but the answer never arrived: the result is unknown. */
  | { status: 'network'; message: string }
  /** The server answered an error; `message` is its sentence, verbatim. */
  | { status: 'failed'; message: string; httpStatus: number | null }

/**
 * One press of a security button: refuses a second one while the first runs and
 * sends NOTHING when the browser is offline. Nothing is ever queued.
 */
export async function runSecurityAction<T>(
  gate: SecurityGate,
  action: () => Promise<T>,
  isOnline: () => boolean = isBrowserOnline,
): Promise<SecurityOutcome<T>> {
  if (!gate.start()) {
    return { status: 'busy', message: TWO_FACTOR_MESSAGES.busy }
  }
  try {
    if (!isOnline()) {
      return { status: 'offline', message: TWO_FACTOR_MESSAGES.offline }
    }
    return { status: 'ok', value: await action() }
  } catch (error: unknown) {
    if (error instanceof NetworkError) {
      return { status: 'network', message: TWO_FACTOR_MESSAGES.network }
    }
    if (error instanceof ApiError) {
      return { status: 'failed', message: error.message, httpStatus: error.status }
    }
    const message =
      error instanceof Error && error.message.trim() !== ''
        ? error.message
        : 'No pudimos completar la operación.'
    return { status: 'failed', message, httpStatus: null }
  } finally {
    gate.finish()
  }
}

// --- the flow of the «Seguridad» page ---

export type SecurityView =
  | { kind: 'loading' }
  | { kind: 'inactive' }
  | { kind: 'setup'; secret: string; otpauthUrl: string }
  /** The one-time recovery codes: they stay until the person confirms having saved them. */
  | { kind: 'codes'; codes: string[] }
  | { kind: 'active' }
  /** Active, with the «Desactivar» form open. */
  | { kind: 'disabling' }

export type SecurityEvent =
  | { type: 'status'; enabled: boolean }
  | { type: 'setup_started'; secret: string; otpauthUrl: string }
  | { type: 'setup_cancelled' }
  | { type: 'enabled'; codes: string[] }
  | { type: 'codes_acknowledged' }
  | { type: 'disable_opened' }
  | { type: 'disable_cancelled' }
  | { type: 'disabled' }

/**
 * Transitions of the page. The invariant: the recovery codes are shown ONCE, so a
 * status refresh, a cancelled setup or any other event never hides them; only the
 * explicit confirmation does.
 */
export function securityReducer(view: SecurityView, event: SecurityEvent): SecurityView {
  if (view.kind === 'codes') {
    return event.type === 'codes_acknowledged' ? { kind: 'active' } : view
  }
  switch (event.type) {
    case 'status':
      if (event.enabled) {
        return view.kind === 'disabling' ? view : { kind: 'active' }
      }
      // A pending setup survives a refresh that still says «not enabled».
      return view.kind === 'setup' ? view : { kind: 'inactive' }
    case 'setup_started':
      return view.kind === 'inactive' || view.kind === 'setup'
        ? { kind: 'setup', secret: event.secret, otpauthUrl: event.otpauthUrl }
        : view
    case 'setup_cancelled':
      return view.kind === 'setup' ? { kind: 'inactive' } : view
    case 'enabled':
      return event.codes.length > 0 ? { kind: 'codes', codes: [...event.codes] } : { kind: 'active' }
    case 'codes_acknowledged':
      return view
    case 'disable_opened':
      return view.kind === 'active' ? { kind: 'disabling' } : view
    case 'disable_cancelled':
      return view.kind === 'disabling' ? { kind: 'active' } : view
    case 'disabled':
      return { kind: 'inactive' }
  }
}

/** The browser warns before leaving only while the one-time codes are on screen. */
export function shouldWarnBeforeLeaving(view: SecurityView): boolean {
  return view.kind === 'codes'
}
