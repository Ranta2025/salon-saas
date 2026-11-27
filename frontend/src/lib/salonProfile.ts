/**
 * «Mi salón» (contract F16, D1/D3/D7): the public profile of the salon, its
 * photo gallery and the preview of what the client will see.
 *
 * Everything that decides something is PURE and tested: the validators (same
 * limits as the server), the payload builder (the PUT requires the six keys, so
 * none is ever omitted), the gallery rules (12 photos, type and size before the
 * request) and the preview, which runs through the SAME builder as the landing
 * so the panel cannot show something the public page would not.
 */

import { API_BASE, ApiError, request, requestMultipart } from './api.ts'
import { EMAIL_INVALID_MESSAGE, isValidEmail } from './email.ts'
import { validatePhotoFile, type PhotoFileLike } from '../features/clients/lib/photos.ts'
import {
  buildLanding,
  resolveAssetUrl,
  type LandingModel,
  type PublicPhoto,
  type PublicSalonInfo,
} from './publicSalon.ts'

/** Photos a salon can publish; the server answers 409 beyond it. */
export const GALLERY_LIMIT = 12

/** Verbatim `409` of the server, also shown before sending when the gallery is full. */
export const GALLERY_FULL_MESSAGE = 'La galería ya tiene 12 fotos'

export const MAX_CAPTION_LENGTH = 120
export const MAX_SERVICE_DESCRIPTION_LENGTH = 300

export const PROFILE_LIMITS = {
  name: 120,
  description: 500,
  address: 200,
  phone: 40,
  email: 254,
  instagram: 60,
} as const

export type ProfileField = keyof typeof PROFILE_LIMITS

const FIELD_LABELS: Record<ProfileField, string> = {
  name: 'El nombre',
  description: 'La descripción',
  address: 'La dirección',
  phone: 'El teléfono',
  email: 'El email',
  instagram: 'El usuario de Instagram',
}

export const PROFILE_MESSAGES = {
  nameRequired: 'Escribí el nombre del salón',
  noLineBreaks: 'No uses saltos de línea en este campo',
  instagramUser: 'Escribí solo el usuario de Instagram, sin @ ni enlace',
  tooLong: (label: string, max: number): string =>
    `${label} no puede superar los ${max} caracteres`,
} as const

/** Help texts of the page. */
export const PROFILE_HELP = {
  /** Heading of the preview block, verbatim. */
  preview: 'Esto es lo que ve el cliente en tu página pública',
  description: 'Se muestra en la portada de tu página pública.',
  contact:
    'Lo que dejes vacío no se muestra en la página pública: el teléfono arma el botón de WhatsApp.',
  gallery:
    'Estas fotos se muestran tal cual en la galería de tu página pública, de la más antigua a la más nueva.',
} as const

// ---------- profile ----------

export type SalonProfile = {
  name: string
  slug: string
  description: string | null
  address: string | null
  phone: string | null
  email: string | null
  instagram: string | null
}

/** What the form edits: every field is text, empty means «not filled». */
export type ProfileDraft = {
  name: string
  description: string
  address: string
  phone: string
  email: string
  instagram: string
}

/** Body of `PUT /profile`: the six keys always, `null` for an empty one; no slug. */
export type ProfilePayload = {
  name: string
  description: string | null
  address: string | null
  phone: string | null
  email: string | null
  instagram: string | null
}

export type ProfileValidation =
  | { ok: true; payload: ProfilePayload }
  | { ok: false; field: ProfileField; message: string }

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function nullableText(value: unknown): string | null {
  if (typeof value !== 'string') {
    return null
  }
  const trimmed = value.trim()
  return trimmed === '' ? null : trimmed
}

/** Length in characters (code points), the way the server counts them. */
function length(value: string): number {
  return [...value].length
}

function hasControl(value: string): boolean {
  return /[\u0000\r\n]/.test(value)
}

function orNull(value: string): string | null {
  const trimmed = value.trim()
  return trimmed === '' ? null : trimmed
}

/** `@luna` is stored as `luna`: the server keeps only the user. */
function instagramUser(value: string): string {
  const trimmed = value.trim()
  return trimmed.startsWith('@') ? trimmed.slice(1).trim() : trimmed
}

export function normalizeProfile(raw: unknown): SalonProfile | null {
  if (!isRecord(raw)) {
    return null
  }
  const name = nullableText(raw['name'])
  if (name === null) {
    return null
  }
  return {
    name,
    slug: nullableText(raw['slug']) ?? '',
    description: nullableText(raw['description']),
    address: nullableText(raw['address']),
    phone: nullableText(raw['phone']),
    email: nullableText(raw['email']),
    instagram: nullableText(raw['instagram']),
  }
}

export function draftFromProfile(profile: SalonProfile): ProfileDraft {
  return {
    name: profile.name,
    description: profile.description ?? '',
    address: profile.address ?? '',
    phone: profile.phone ?? '',
    email: profile.email ?? '',
    instagram: profile.instagram ?? '',
  }
}

/** Builds the PUT body: trimmed, empty → `null`, the six keys always present. */
export function buildProfilePayload(draft: ProfileDraft): ProfilePayload {
  return {
    name: draft.name.trim(),
    description: orNull(draft.description),
    address: orNull(draft.address),
    phone: orNull(draft.phone),
    email: orNull(draft.email),
    instagram: orNull(instagramUser(draft.instagram)),
  }
}

const VALIDATION_ORDER: readonly ProfileField[] = [
  'name',
  'description',
  'address',
  'phone',
  'email',
  'instagram',
]

/** Same rules as the server (D1) so the person sees the field that is wrong, not a generic 400. */
export function validateProfileDraft(draft: ProfileDraft): ProfileValidation {
  const payload = buildProfilePayload(draft)
  for (const field of VALIDATION_ORDER) {
    const raw = field === 'instagram' ? instagramUser(draft[field]) : draft[field].trim()
    if (hasControl(draft[field])) {
      return { ok: false, field, message: PROFILE_MESSAGES.noLineBreaks }
    }
    if (field === 'name' && raw === '') {
      return { ok: false, field, message: PROFILE_MESSAGES.nameRequired }
    }
    if (length(raw) > PROFILE_LIMITS[field]) {
      return {
        ok: false,
        field,
        message: PROFILE_MESSAGES.tooLong(FIELD_LABELS[field], PROFILE_LIMITS[field]),
      }
    }
    if (field === 'email' && !isValidEmail(raw)) {
      return { ok: false, field, message: EMAIL_INVALID_MESSAGE }
    }
    if (field === 'instagram' && /[\s/@]/.test(raw)) {
      return { ok: false, field, message: PROFILE_MESSAGES.instagramUser }
    }
  }
  return { ok: true, payload }
}

// ---------- preview ----------

/**
 * What the public page will show for this draft and gallery. It goes through
 * `buildLanding`, the builder of the landing itself: an empty field hides its
 * block here exactly as it does there.
 */
export function previewLanding(draft: ProfileDraft, gallery: readonly PublicPhoto[]): LandingModel {
  const payload = buildProfilePayload(draft)
  const salon: PublicSalonInfo = { id: '', slug: '', name: payload.name }
  for (const key of ['description', 'address', 'phone', 'email', 'instagram'] as const) {
    const value = payload[key]
    if (value !== null) {
      salon[key] = value
    }
  }
  return buildLanding({
    salon,
    services: [],
    team: [],
    gallery: [...gallery],
    packages: [],
    hours: null,
  })
}

// ---------- gallery ----------

export type GalleryPhoto = {
  id: string
  url: string
  /** `''` = no caption. */
  caption: string
  createdAt: string | null
}

function normalizeGalleryPhoto(raw: unknown, apiBase: string): GalleryPhoto | null {
  if (!isRecord(raw)) {
    return null
  }
  const id = nullableText(raw['id'])
  const url = nullableText(raw['url'])
  if (id === null || url === null) {
    return null
  }
  return {
    id,
    url: resolveAssetUrl(url, apiBase),
    caption: nullableText(raw['caption']) ?? '',
    createdAt: nullableText(raw['created_at']),
  }
}

/** `GET /gallery` → `{photos:[...]}`; the order of the server (upload order) is kept. */
export function normalizeGallery(raw: unknown, apiBase: string): GalleryPhoto[] {
  const rows = isRecord(raw) && Array.isArray(raw['photos']) ? raw['photos'] : []
  const photos: GalleryPhoto[] = []
  for (const row of rows) {
    const photo = normalizeGalleryPhoto(row, apiBase)
    if (photo) {
      photos.push(photo)
    }
  }
  return photos
}

export function galleryCounter(count: number): string {
  return `${count}/${GALLERY_LIMIT}`
}

export type UploadCheck = { ok: true } | { ok: false; message: string }

/** Type, size and the 12-photo cap, all before any request leaves the browser. */
export function validateGalleryUpload(file: PhotoFileLike, currentCount: number): UploadCheck {
  if (currentCount >= GALLERY_LIMIT) {
    return { ok: false, message: GALLERY_FULL_MESSAGE }
  }
  return validatePhotoFile(file)
}

export type CaptionCheck = { ok: true; caption: string | null } | { ok: false; message: string }

export function validateCaption(value: string): CaptionCheck {
  if (hasControl(value)) {
    return { ok: false, message: PROFILE_MESSAGES.noLineBreaks }
  }
  const trimmed = value.trim()
  if (length(trimmed) > MAX_CAPTION_LENGTH) {
    return { ok: false, message: PROFILE_MESSAGES.tooLong('La leyenda', MAX_CAPTION_LENGTH) }
  }
  return { ok: true, caption: trimmed === '' ? null : trimmed }
}

/** Body of `PUT /gallery/:pid`: the key is required, `null` = no caption. */
export function captionBody(caption: string | null): { caption: string | null } {
  const text = caption?.trim() ?? ''
  return { caption: text === '' ? null : text }
}

// ---------- service description (D2) ----------

export type DescriptionCheck = { ok: true; description: string } | { ok: false; message: string }

export function validateServiceDescription(value: string): DescriptionCheck {
  if (hasControl(value)) {
    return { ok: false, message: PROFILE_MESSAGES.noLineBreaks }
  }
  const trimmed = value.trim()
  if (length(trimmed) > MAX_SERVICE_DESCRIPTION_LENGTH) {
    return {
      ok: false,
      message: PROFILE_MESSAGES.tooLong('La descripción', MAX_SERVICE_DESCRIPTION_LENGTH),
    }
  }
  return { ok: true, description: trimmed }
}

/**
 * Part of the service body that carries the description. On create an empty one is
 * omitted (the key is optional); on edit it travels as `""` so an emptied field
 * really clears it instead of being read as «absent, keep».
 */
export function serviceDescriptionBody(
  description: string,
  mode: 'create' | 'edit',
): { description?: string } {
  const text = description.trim()
  if (text === '' && mode === 'create') {
    return {}
  }
  return { description: text }
}

// ---------- HTTP ----------

function profilePath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/profile`
}

function galleryPath(salonId: string, photoId?: string): string {
  const base = `/api/v1/salons/${encodeURIComponent(salonId)}/gallery`
  return photoId === undefined ? base : `${base}/${encodeURIComponent(photoId)}`
}

function readProfile(raw: unknown, status: number): SalonProfile {
  const profile = normalizeProfile(raw)
  if (!profile) {
    throw new ApiError(status, 'El servidor no devolvió el perfil del salón.')
  }
  return profile
}

export async function fetchProfile(salonId: string): Promise<SalonProfile> {
  return readProfile(await request<unknown>(profilePath(salonId)), 200)
}

export async function saveProfile(salonId: string, payload: ProfilePayload): Promise<SalonProfile> {
  const body = await request<unknown>(profilePath(salonId), { method: 'PUT', body: payload })
  return readProfile(body, 200)
}

export async function fetchGallery(salonId: string): Promise<GalleryPhoto[]> {
  return normalizeGallery(await request<unknown>(galleryPath(salonId)), API_BASE)
}

/** Multipart upload (field `file`); the page reloads the list afterwards. */
export async function uploadGalleryPhoto(
  salonId: string,
  file: File,
): Promise<GalleryPhoto | null> {
  const form = new FormData()
  form.append('file', file, file.name)
  const body = await requestMultipart<unknown>(galleryPath(salonId), form)
  const row = isRecord(body) && isRecord(body['photo']) ? body['photo'] : body
  return normalizeGalleryPhoto(row, API_BASE)
}

export async function saveCaption(
  salonId: string,
  photoId: string,
  caption: string | null,
): Promise<void> {
  await request<unknown>(galleryPath(salonId, photoId), {
    method: 'PUT',
    body: captionBody(caption),
  })
}

export async function deleteGalleryPhoto(salonId: string, photoId: string): Promise<void> {
  await request<unknown>(galleryPath(salonId, photoId), { method: 'DELETE' })
}
