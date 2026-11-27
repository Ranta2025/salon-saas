/**
 * Password recovery and email verification (contract F15, D2, D3, D7, D11, D12).
 * Pure rules plus the thin API clients: the three public pages (`/recuperar`,
 * `/restablecer`, `/verificar-email`), the login notice and the «Verifica tu
 * email» banner of the panel render what these return. The server stays the
 * authority; every server sentence is shown verbatim.
 *
 * Security rules of the pages (same as the invitation page):
 * - the token travels in the FRAGMENT of the emailed link, so it never reaches a
 *   server or proxy log; the page reads it once and wipes it from the address bar
 *   with `history.replaceState`, and keeps it only in memory (never storage, never
 *   the console, never an error message, never the query or the path);
 * - every request sends it in the BODY;
 * - nothing here stores a session: `/auth/reset` answers 204 and the person signs
 *   in again (D3); `/auth/forgot` always answers 202 and the page never
 *   distinguishes a registered email from an unknown one (D2).
 *
 * Online only (D12): nothing in this module touches the offline queue.
 */

import { ApiError, NetworkError, readEmailVerified, request } from './api.ts'
import { isBrowserOnline } from './connectivity.ts'
import { EMAIL_INVALID_MESSAGE, isValidEmail, normalizeEmail } from './email.ts'
import { INVITATION_PASSWORD_MAX_BYTES, INVITATION_PASSWORD_MIN_BYTES } from '../features/team/lib/invitations.ts'

export { readEmailVerified }

/**
 * Sentences of this module. The ones marked «contract» are quoted by the roadmap
 * (D11, D12) and are shown exactly; the sentences of the server are never rewritten.
 */
export const RECOVERY_MESSAGES = {
  /** contract D11: the ONLY answer to a 202 of `/auth/forgot`, registered email or not. */
  forgotAccepted: 'Si el correo esta registrado, te enviamos un enlace. Revisa tu bandeja.',
  /** contract D11: the notice of the login after a reset. */
  resetDone: 'Contrasena actualizada. Inicia sesion.',
  /** contract D12: answer to a 202 of the resend. */
  resendSent: 'Te enviamos un nuevo correo.',
  emailRequired: 'Escribí tu correo',
  passwordMismatch: 'Las contraseñas no coinciden',
  confirmRequired: 'Repetí la contraseña',
  noToken:
    'Este enlace está incompleto o ya se usó. Pedí uno nuevo para continuar.',
  offline:
    'Sin conexión. Esto solo se pide en línea: no se envió ni quedó en cola. Volvé a intentarlo cuando tengas conexión.',
  resendFallback: 'No pudimos reenviar el correo. Intentá de nuevo.',
} as const

/** Public paths (no session): a 401 anywhere must NOT send them to /login (D11). */
export const RECOVERY_PATHS: readonly string[] = ['/recuperar', '/restablecer', '/verificar-email']

export function isRecoveryPath(pathname: string): boolean {
  const clean = pathname.length > 1 ? pathname.replace(/\/+$/, '') : pathname
  return RECOVERY_PATHS.includes(clean)
}

// --- the token of the fragment ---

/** `crypto/rand` gives 32 bytes → 43 characters in base64url. */
const TOKEN_PATTERN = /^[A-Za-z0-9_-]{20,128}$/

export type FragmentRead = { token: string | null; url: string }

/**
 * Reads the token of `location.hash` and returns the URL WITHOUT any fragment,
 * ready for `history.replaceState`. The fragment of these links is exactly the
 * token: anything else (empty, spaces, percent-encoding, a second `#`) is junk,
 * is dropped and is never used as a token. A token in the query string is not
 * ours: it is neither read nor touched.
 */
export function readFragmentToken(href: string): FragmentRead {
  const hashIndex = href.indexOf('#')
  if (hashIndex === -1) {
    return { token: null, url: href }
  }
  const fragment = href.slice(hashIndex + 1)
  return {
    token: TOKEN_PATTERN.test(fragment) ? fragment : null,
    url: href.slice(0, hashIndex),
  }
}

// --- /recuperar ---

export type EmailCheck = { ok: true; email: string } | { ok: false; message: string }

/** Only «something that looks like an email»; the server validates for real (400 verbatim). */
export function validateForgotEmail(input: string): EmailCheck {
  const email = normalizeEmail(input)
  if (email === '') {
    return { ok: false, message: RECOVERY_MESSAGES.emailRequired }
  }
  if (!isValidEmail(email)) {
    return { ok: false, message: EMAIL_INVALID_MESSAGE }
  }
  return { ok: true, email }
}

/**
 * `POST /auth/forgot {email}` → 202. Resolves with nothing: the page shows the
 * same sentence for every 202, so no answer detail can tell registered emails
 * apart. A 400 (malformed email) rejects with the server sentence.
 */
export async function requestPasswordReset(email: string): Promise<void> {
  await request<unknown>('/api/v1/auth/forgot', {
    method: 'POST',
    body: { email: normalizeEmail(email) },
    auth: 'none',
    keepSessionOn401: true,
  })
}

// --- /restablecer ---

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length
}

export type PasswordCheck =
  | { ok: true; password: string }
  | { ok: false; field: 'password' | 'confirm'; message: string }

/**
 * New password of the reset form: 8..72 BYTES like the API (`ValidatePassword`;
 * bcrypt truncates at 72 bytes, so an accented letter counts twice), never
 * trimmed, and the confirmation must match.
 */
export function validateNewPassword(input: { password: string; confirm: string }): PasswordCheck {
  const bytes = byteLength(input.password)
  if (bytes < INVITATION_PASSWORD_MIN_BYTES) {
    return {
      ok: false,
      field: 'password',
      message: `La contraseña debe tener al menos ${INVITATION_PASSWORD_MIN_BYTES} caracteres`,
    }
  }
  if (bytes > INVITATION_PASSWORD_MAX_BYTES) {
    return {
      ok: false,
      field: 'password',
      message: `La contraseña no puede superar los ${INVITATION_PASSWORD_MAX_BYTES} caracteres`,
    }
  }
  if (input.confirm === '') {
    return { ok: false, field: 'confirm', message: RECOVERY_MESSAGES.confirmRequired }
  }
  if (input.confirm !== input.password) {
    return { ok: false, field: 'confirm', message: RECOVERY_MESSAGES.passwordMismatch }
  }
  return { ok: true, password: input.password }
}

/**
 * `POST /auth/reset {token,password}` → 204. NO session comes back (D3): nothing
 * of the answer is read or stored, the person signs in again. A 400 keeps the
 * server sentence («Enlace invalido o vencido» or the password rule).
 */
export async function resetPassword(token: string, password: string): Promise<void> {
  await request<unknown>('/api/v1/auth/reset', {
    method: 'POST',
    body: { token, password },
    auth: 'none',
    keepSessionOn401: true,
  })
}

// --- /verificar-email ---

/** `POST /auth/verify-email {token}` → 204. A 400 keeps the server sentence. */
export async function verifyEmail(token: string): Promise<void> {
  await request<unknown>('/api/v1/auth/verify-email', {
    method: 'POST',
    body: { token },
    auth: 'none',
    keepSessionOn401: true,
  })
}

// --- the panel banner (D12) ---

/** Only an explicit `email_verified === false` shows the banner. */
export function shouldShowVerifyBanner(emailVerified: boolean | null | undefined): boolean {
  return emailVerified === false
}

/**
 * What the banner shows after «Reenviar correo»:
 * - `sent`: 202, «Te enviamos un nuevo correo.»;
 * - `verified`: 409, the email is already verified (server sentence verbatim);
 * - `error`: 429 and every other failure, with the server sentence verbatim.
 */
export type ResendOutcome =
  | { kind: 'sent'; message: string }
  | { kind: 'verified'; message: string }
  | { kind: 'error'; message: string }

/**
 * `POST /me/verify-email/resend` (authenticated, online only). Offline it does not
 * send and does not queue anything. Never throws: the banner prints `message`.
 */
export async function resendVerification(
  options: { online?: () => boolean } = {},
): Promise<ResendOutcome> {
  const online = options.online ?? isBrowserOnline
  if (!online()) {
    return { kind: 'error', message: RECOVERY_MESSAGES.offline }
  }
  try {
    await request<unknown>('/api/v1/me/verify-email/resend', { method: 'POST' })
    return { kind: 'sent', message: RECOVERY_MESSAGES.resendSent }
  } catch (err: unknown) {
    if (err instanceof ApiError && err.status === 409) {
      return { kind: 'verified', message: err.message }
    }
    if (err instanceof ApiError || err instanceof NetworkError) {
      return { kind: 'error', message: err.message }
    }
    return {
      kind: 'error',
      message: err instanceof Error && err.message.trim() !== '' ? err.message : RECOVERY_MESSAGES.resendFallback,
    }
  }
}

// --- the notice of the login after a reset: one shot ---

/** The one-shot key. It holds only the notice sentence, never a token or a password. */
export const RESET_NOTICE_KEY = 'salon.notice.reset'

export type NoticeStorage = Pick<Storage, 'getItem' | 'setItem' | 'removeItem'>

const memoryNotice = new Map<string, string>()

function defaultStorage(): NoticeStorage {
  try {
    if (typeof sessionStorage !== 'undefined') {
      return sessionStorage
    }
  } catch {
    // access can throw (private mode): fall back to memory
  }
  return {
    getItem: (key) => memoryNotice.get(key) ?? null,
    setItem: (key, value) => void memoryNotice.set(key, value),
    removeItem: (key) => void memoryNotice.delete(key),
  }
}

/** Leaves the notice for the login that comes next (sessionStorage: this tab only). */
export function rememberResetNotice(storage: NoticeStorage = defaultStorage()): void {
  try {
    storage.setItem(RESET_NOTICE_KEY, RECOVERY_MESSAGES.resetDone)
  } catch {
    // no storage: the login simply does not show the notice
  }
}

/**
 * Returns the notice ONCE and clears it. Anything that is not the exact sentence
 * is discarded (it is rendered as text, but nothing else should ever live there).
 */
export function takeResetNotice(storage: NoticeStorage = defaultStorage()): string | null {
  try {
    const value = storage.getItem(RESET_NOTICE_KEY)
    if (value === null) {
      return null
    }
    storage.removeItem(RESET_NOTICE_KEY)
    return value === RECOVERY_MESSAGES.resetDone ? value : null
  } catch {
    return null
  }
}
