/**
 * Contact email of the public booking (`client_email`, optional).
 *
 * The rule mirrors `domain.NormalizeAndValidateEmail` of the API so the message
 * the visitor sees is the same whether the check runs in the browser or in the
 * server: lowercase, trimmed, must contain `@` and be 5..254 characters.
 */

export const EMAIL_INVALID_MESSAGE = 'Email inválido'

const MIN_EMAIL_LENGTH = 5
const MAX_EMAIL_LENGTH = 254

export function normalizeEmail(value: string): string {
  return value.trim().toLowerCase()
}

/** An empty email is valid: the field is optional, a filled one must look right. */
export function isValidEmail(value: string): boolean {
  const email = normalizeEmail(value)
  if (email === '') {
    return true
  }
  if (email.length < MIN_EMAIL_LENGTH || email.length > MAX_EMAIL_LENGTH) {
    return false
  }
  return !email.includes(' ') && email.includes('@') && !email.startsWith('@') && !email.endsWith('@')
}

/** The server's `400 {"error":"Email inválido"}` belongs next to the field. */
export function isEmailErrorMessage(message: string): boolean {
  return normalizeEmail(message) === normalizeEmail(EMAIL_INVALID_MESSAGE)
}
