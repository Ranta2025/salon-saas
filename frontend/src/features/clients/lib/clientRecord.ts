/**
 * Client file of the salon (contract F5, «Contrato F5 (historial de la clienta…)»,
 * decisions D4 and D5): the frozen `client` shape, the replace with ALL FIVE keys,
 * the allergies field and the optional client picker of the POS (D3c).
 *
 * The screen at `/panel/clientes/:id` renders what these functions return; the
 * server stays the authority on permissions and on validation. Allergies are
 * health data: they are read for every active member (a professional needs them
 * while attending) and written only by owner/admin/reception.
 */

import { ApiError, NetworkError, request } from '../../../lib/api.ts'
import { EMAIL_INVALID_MESSAGE, isValidEmail } from '../../../lib/email.ts'

/** Longest name of a file (same rule as the create, D5). */
export const MAX_NAME_LENGTH = 120
/** Longest phone of a file (same rule as the create, D5). */
export const MAX_PHONE_LENGTH = 40
/** Longest notes of a file (same rule as the create, D5). */
export const MAX_NOTES_LENGTH = 2000
/** Longest allergies text (`CHECK (char_length(allergies) <= 500)`, D4). */
export const MAX_ALLERGIES_LENGTH = 500

/** Heading of the allergies block; the notice is shown for every role (D4). */
export const ALLERGIES_TITLE = 'Alergias'
/** Sentence next to the allergies, so nobody reads them as a preference. */
export const ALLERGIES_NOTICE =
  'Dato de salud: revisalo antes de atender y avisale a la clienta si tenés dudas.'
/** What the file says when the field is empty: no notice is shown in that case. */
export const NO_ALLERGIES_TEXT = 'Sin alergias registradas.'
/** Heading of the edit form (only owner/admin/reception see it). */
export const EDIT_CLIENT_TITLE = 'Editar ficha'
/** Explanation under the form: the screen reloads the file from the server. */
export const EDIT_CLIENT_HINT =
  'Al guardar volvemos a pedir la ficha al servidor: la pantalla nunca muestra un dato sin guardar.'

/** Client-side sentences: the same texts the API uses for the same rules. */
export const CLIENT_NAME_REQUIRED_MESSAGE = 'Escribí el nombre de la clienta.'
export const CLIENT_NAME_LONG_MESSAGE = `El nombre no puede superar los ${MAX_NAME_LENGTH} caracteres`
export const CLIENT_PHONE_LONG_MESSAGE = `El teléfono no puede superar los ${MAX_PHONE_LENGTH} caracteres`
export const CLIENT_NOTES_LONG_MESSAGE = `Las notas no pueden superar los ${MAX_NOTES_LENGTH} caracteres`
/** `400` of the API when the replace lacks one of the five keys (D5). */
export const MISSING_KEYS_MESSAGE =
  'Faltan datos de la ficha: nombre, teléfono, email, notas y alergias'
/** `400` of the API when the allergies pass the cap of the base (D4). */
export const ALLERGIES_LONG_MESSAGE = 'Las alergias no pueden superar los 500 caracteres'
/** `400` of the API when any of the five fields carries a NUL character (D5). */
export const INVALID_CHARS_MESSAGE = 'La ficha tiene caracteres no válidos'
/** `404` of the API, also for a file of another salon (D5). */
export const CLIENT_NOT_FOUND_MESSAGE = 'Ficha no encontrada'
/** `403` of the API: the role may not write the file. */
export const NO_PERMISSION_MESSAGE = 'sin permiso'
/** Answer of the second press of a save that is already running. */
export const CLIENT_SAVE_BUSY_MESSAGE = 'Estamos guardando los cambios: esperá un momento.'
/** Nothing to read: the server answered 200 with an unusable body. */
export const CLIENT_UNREADABLE_MESSAGE = 'La API no devolvió los datos de la ficha.'
/** Nothing to read after a write: the change may or may not be stored. */
export const CLIENT_SAVE_UNREADABLE_MESSAGE =
  'El servidor guardó la ficha pero no devolvió sus datos. Actualizá para ver la versión guardada.'

/**
 * One file as `GET /salons/:id/clients/:cid` returns it. `phone`, `email`, `notes`
 * and `allergies` are always text (`""` when empty), so the edit form can hold
 * them without null checks.
 */
export type ClientRecord = {
  id: string
  name: string
  phone: string
  email: string
  notes: string
  allergies: string
  created_at: string | null
}

/** Body of `PUT /salons/:id/clients/:cid`: the endpoint REPLACES, so all five travel. */
export type ClientRecordInput = {
  name: string
  phone: string
  email: string
  notes: string
  allergies: string
}

export type ClientRecordField = 'name' | 'phone' | 'email' | 'notes' | 'allergies'

export type ClientRecordCheck =
  | { ok: true; payload: ClientRecordInput }
  | { ok: false; field: ClientRecordField; message: string }

/** Anything with an id and a name: a file, or a row of the client list. */
export type PickableClient = { id: string; name: string; phone?: string | null }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

/** Characters, not bytes: `ñ` or an emoji count as one (same rule as the server). */
function charCount(value: string): number {
  return [...value].length
}

/** Reads one `client`; a row without id is dropped instead of rendered nameless. */
export function normalizeClientRecord(raw: unknown): ClientRecord | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = text(raw['id'])
  if (id === '') {
    return null
  }
  const createdAt = text(raw['created_at'])
  return {
    id,
    name: text(raw['name']) || text(raw['full_name']) || 'Clienta sin nombre',
    phone: text(raw['phone']),
    email: text(raw['email']),
    notes: text(raw['notes']),
    allergies: text(raw['allergies']),
    created_at: createdAt === '' ? null : createdAt,
  }
}

/**
 * Body of the replace: the FIVE keys, always, trimmed, with the email the way the
 * server stores it (lowercase). An empty `notes` or `allergies` is sent as `""`,
 * because the endpoint replaces the row instead of patching it.
 */
export function clientRecordBody(input: ClientRecordInput): ClientRecordInput {
  return {
    name: input.name.trim(),
    phone: input.phone.trim(),
    email: input.email.trim().toLowerCase(),
    notes: input.notes.trim(),
    allergies: input.allergies.trim(),
  }
}

/**
 * Reads the form before spending a request, with the sentences of the API, so the
 * person sees the problem on the field that has it. `validateClientRecordDraft`
 * never rewrites what was typed: it returns the payload already trimmed.
 */
export function validateClientRecordDraft(draft: ClientRecordInput): ClientRecordCheck {
  const fields: [ClientRecordField, string][] = [
    ['name', draft.name.trim()],
    ['phone', draft.phone.trim()],
    ['email', draft.email.trim().toLowerCase()],
    ['notes', draft.notes.trim()],
    ['allergies', draft.allergies.trim()],
  ]
  for (const [field, value] of fields) {
    if (value.includes('\u0000')) {
      return { ok: false, field, message: INVALID_CHARS_MESSAGE }
    }
  }
  const payload = Object.fromEntries(fields) as ClientRecordInput
  if (payload.name === '') {
    return { ok: false, field: 'name', message: CLIENT_NAME_REQUIRED_MESSAGE }
  }
  if (charCount(payload.name) > MAX_NAME_LENGTH) {
    return { ok: false, field: 'name', message: CLIENT_NAME_LONG_MESSAGE }
  }
  if (charCount(payload.phone) > MAX_PHONE_LENGTH) {
    return { ok: false, field: 'phone', message: CLIENT_PHONE_LONG_MESSAGE }
  }
  if (!isValidEmail(payload.email)) {
    return { ok: false, field: 'email', message: EMAIL_INVALID_MESSAGE }
  }
  if (charCount(payload.notes) > MAX_NOTES_LENGTH) {
    return { ok: false, field: 'notes', message: CLIENT_NOTES_LONG_MESSAGE }
  }
  if (charCount(payload.allergies) > MAX_ALLERGIES_LENGTH) {
    return { ok: false, field: 'allergies', message: ALLERGIES_LONG_MESSAGE }
  }
  return { ok: true, payload }
}

/**
 * Case-insensitive search over a list that is ALREADY in memory: the POS picker
 * never asks the API again while the person types. The name and the phone digits
 * are searched, so `5552` finds `+53 5552 8595`.
 */
export function searchClients<T extends PickableClient>(
  clients: readonly T[],
  term: string,
): T[] {
  const needle = term.trim().toLowerCase()
  if (needle === '') {
    return [...clients]
  }
  const digits = needle.replace(/\D/g, '')
  return clients.filter((client) => {
    if (client.name.toLowerCase().includes(needle)) {
      return true
    }
    const phone = (client.phone ?? '').toLowerCase()
    if (phone.includes(needle)) {
      return true
    }
    return digits !== '' && phone.replace(/\D/g, '').includes(digits)
  })
}

/**
 * Message of a failed call: the sentence of the API verbatim, including the 403
 * `sin permiso` and the 404 `Ficha no encontrada` of this contract. Unlike
 * `describeApiError`.message, the 403 is not replaced here: F5 freezes the texts
 * per status and the screen must show them as they arrive.
 */
export function clientApiMessage(error: unknown, fallback: string): string {
  if (error instanceof ApiError) {
    return error.message.trim() !== '' ? error.message : fallback
  }
  if (error instanceof NetworkError) {
    return error.message.trim() !== '' ? error.message : fallback
  }
  if (error instanceof Error && error.message.trim() !== '') {
    return error.message
  }
  return fallback
}

// --- API client (frozen shapes of the F5 contract) ---

function salonPath(salonId: string, suffix = ''): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}${suffix}`
}

function clientPath(salonId: string, clientId: string): string {
  return salonPath(salonId, `/clients/${encodeURIComponent(clientId)}`)
}

function requireRecord(raw: unknown, message: string): ClientRecord {
  const record = normalizeClientRecord(raw)
  if (!record) {
    throw new ApiError(200, message)
  }
  return record
}

/** `GET /salons/:id/clients/:cid`: any active member; 404 `Ficha no encontrada`. */
export async function fetchClientRecord(
  salonId: string,
  clientId: string,
  signal?: AbortSignal,
): Promise<ClientRecord> {
  return requireRecord(
    await request<unknown>(clientPath(salonId, clientId), { signal }),
    CLIENT_UNREADABLE_MESSAGE,
  )
}

/** `PUT /salons/:id/clients/:cid`: replaces the file with the five keys (owner/admin/reception). */
export async function updateClientRecord(
  salonId: string,
  clientId: string,
  input: ClientRecordInput,
): Promise<ClientRecord> {
  return requireRecord(
    await request<unknown>(clientPath(salonId, clientId), {
      method: 'PUT',
      body: clientRecordBody(input),
    }),
    CLIENT_SAVE_UNREADABLE_MESSAGE,
  )
}

// --- one save at a time (a double click must not send two PUTs) ---

export type ClientSaveGate = {
  /** True while a save is running. */
  busy: () => boolean
  /** Claims the gate; `false` when another save is already running. */
  start: () => boolean
  /** Releases the gate; `true` when it was busy. */
  finish: () => boolean
}

/**
 * Synchronous gate of the page. React state alone is not enough for a double
 * click: both presses can read the same render, so the guard lives here and the
 * state only paints the disabled button (same pattern as the export buttons).
 */
export function createClientSaveGate(): ClientSaveGate {
  let running = false
  return {
    busy: () => running,
    start: () => {
      if (running) {
        return false
      }
      running = true
      return true
    },
    finish: () => {
      const wasBusy = running
      running = false
      return wasBusy
    },
  }
}

export type ClientSaveOutcome =
  | { status: 'saved'; record: ClientRecord }
  | { status: 'busy'; message: string }
  | { status: 'failed'; message: string }

/**
 * One press of "Guardar cambios": claims the gate, replaces the file and frees the
 * gate whatever happens. The record it returns is NOT painted: the page asks the
 * server for the file again, so the screen never shows an amount or an allergy
 * that the server did not store (contract F5: no optimistic state).
 */
export async function saveClientRecord(
  gate: ClientSaveGate,
  salonId: string,
  clientId: string,
  input: ClientRecordInput,
): Promise<ClientSaveOutcome> {
  if (!gate.start()) {
    return { status: 'busy', message: CLIENT_SAVE_BUSY_MESSAGE }
  }
  try {
    const record = await updateClientRecord(salonId, clientId, input)
    return { status: 'saved', record }
  } catch (error: unknown) {
    return { status: 'failed', message: clientApiMessage(error, 'No pudimos guardar la ficha.') }
  } finally {
    gate.finish()
  }
}
