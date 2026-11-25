/**
 * Public payload of `GET /api/v1/public/salons/:slug` (no auth) and the PURE
 * builders that decide what the landing renders (F16, D4/D5).
 *
 * The landing has no salon data of its own: identity, contact, services, team,
 * gallery, packages and hours all come from this payload. A section without data
 * is not rendered, and a failed request is an error state, never a catalogue
 * invented by the page.
 */

import { normalizeDays, type SalonDay } from './weeklyHours.ts'

export type PublicService = {
  id: string
  name: string
  durationMin: number
  priceCents: number
  /** Present only when the salon wrote one; the page hides the line otherwise. */
  description?: string
}

/** Salon block. Every field after `name` is present only when the salon filled it. */
export type PublicSalonInfo = {
  id: string
  slug: string
  name: string
  description?: string
  address?: string
  phone?: string
  email?: string
  instagram?: string
  timezone?: string
}

export type PublicTeamMember = { name: string; trade: string }

export type PublicPhoto = {
  id: string
  /** Absolute (or API-origin) URL of the image, already resolved. */
  url: string
  caption: string
}

export type PublicPackage = {
  id: string
  name: string
  serviceName: string
  sessions: number
  priceCents: number
  /** `null` = no expiry. */
  validityDays: number | null
}

export type PublicSalon = {
  salon: PublicSalonInfo
  services: PublicService[]
  team: PublicTeamMember[]
  gallery: PublicPhoto[]
  packages: PublicPackage[]
  /**
   * `hours` of the public salon (F2d1): the open days only, already expanded by
   * the server. `null` means the API did not send a usable field, so the landing
   * asks to confirm by WhatsApp instead of inventing hours.
   */
  hours: SalonDay[] | null
}

/** Verbatim copy of the states of the landing (D5/D6). */
export const LANDING_ERROR_MESSAGE =
  'No pudimos cargar los datos del salón. Probá de nuevo en un momento.'
export const LANDING_NOT_FOUND_MESSAGE = 'No encontramos este salón.'
export const LANDING_OFFLINE_NOTICE = 'Sin conexión: los datos pueden no estar al día.'

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function integer(value: unknown): number | null {
  return typeof value === 'number' && Number.isInteger(value) ? value : null
}

/** Gallery urls are a path under the API origin: absolute ones are kept as they are. */
export function resolveAssetUrl(url: string, apiBase: string): string {
  if (/^(?:https?:|data:|blob:)/i.test(url)) {
    return url
  }
  return `${apiBase}${url.startsWith('/') ? '' : '/'}${url}`
}

function normalizeService(value: unknown): PublicService | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  const name = text(value['name'])
  const duration = integer(value['duration_min'])
  const price = integer(value['price_cents'])
  if (!id || !name || duration === null || duration <= 0 || price === null || price < 0) {
    return null
  }
  const service: PublicService = { id, name, durationMin: duration, priceCents: price }
  const description = text(value['description'])
  if (description !== '') {
    service.description = description
  }
  return service
}

function normalizeTeamMember(value: unknown): PublicTeamMember | null {
  if (!isRecord(value)) {
    return null
  }
  const name = text(value['name'])
  return name === '' ? null : { name, trade: text(value['trade']) }
}

function normalizePhoto(value: unknown, apiBase: string): PublicPhoto | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  const url = text(value['url'])
  if (id === '' || url === '') {
    return null
  }
  return { id, url: resolveAssetUrl(url, apiBase), caption: text(value['caption']) }
}

function normalizePackage(value: unknown): PublicPackage | null {
  if (!isRecord(value)) {
    return null
  }
  const id = text(value['id'])
  const name = text(value['name'])
  const sessions = integer(value['sessions'])
  const price = integer(value['price_cents'])
  if (!id || !name || sessions === null || sessions < 1 || price === null || price < 0) {
    return null
  }
  const validity = integer(value['validity_days'])
  return {
    id,
    name,
    serviceName: text(value['service_name']),
    sessions,
    priceCents: price,
    validityDays: validity !== null && validity >= 1 ? validity : null,
  }
}

function rowsOf<T>(raw: unknown, read: (row: unknown) => T | null): T[] {
  const out: T[] = []
  for (const row of Array.isArray(raw) ? raw : []) {
    const item = read(row)
    if (item) {
      out.push(item)
    }
  }
  return out
}

const OPTIONAL_SALON_FIELDS = [
  'description',
  'address',
  'phone',
  'email',
  'instagram',
  'timezone',
] as const

/**
 * Returns null when the payload is not a salon at all (no salon block and no
 * usable service). A salon with zero services is valid: the page says so instead
 * of inventing a catalogue. `apiBase` resolves the gallery paths.
 */
export function normalizePublicSalon(raw: unknown, apiBase = ''): PublicSalon | null {
  if (!isRecord(raw)) {
    return null
  }
  const services = rowsOf(raw['services'], normalizeService)
  if (!isRecord(raw['salon']) && services.length === 0) {
    return null
  }
  const salonRaw = isRecord(raw['salon']) ? raw['salon'] : {}
  const salon: PublicSalonInfo = {
    id: text(salonRaw['id']),
    slug: text(salonRaw['slug']),
    name: text(salonRaw['name']),
  }
  for (const key of OPTIONAL_SALON_FIELDS) {
    const value = text(salonRaw[key])
    if (value !== '') {
      salon[key] = value
    }
  }
  const hoursRaw = raw['hours']
  const hours = Array.isArray(hoursRaw) ? normalizeDays(hoursRaw) : []
  return {
    salon,
    services,
    team: rowsOf(raw['team'], normalizeTeamMember),
    gallery: rowsOf(raw['gallery'], (row) => normalizePhoto(row, apiBase)),
    packages: rowsOf(raw['packages'], normalizePackage),
    hours: hours.length > 0 ? hours : null,
  }
}

// ---------- contact links, built only from data the salon has ----------

/** `wa.me` takes 8..15 digits (E.164 allows 15): anything else is not a number to link. */
const MIN_WHATSAPP_DIGITS = 8
const MAX_WHATSAPP_DIGITS = 15

function digitsOf(phone: string | null | undefined): string {
  return (phone ?? '').replace(/\D/g, '')
}

/** WhatsApp link of the salon phone; null when the salon has no usable phone. */
export function whatsappUrl(phone: string | null | undefined, message?: string): string | null {
  const digits = digitsOf(phone)
  if (digits.length < MIN_WHATSAPP_DIGITS || digits.length > MAX_WHATSAPP_DIGITS) {
    return null
  }
  const base = `https://wa.me/${digits}`
  return message ? `${base}?text=${encodeURIComponent(message)}` : base
}

/** `tel:` link of the salon phone; null when it has no digits. */
export function telHref(phone: string | null | undefined): string | null {
  const digits = digitsOf(phone)
  if (digits === '') {
    return null
  }
  return `tel:${(phone ?? '').trim().startsWith('+') ? '+' : ''}${digits}`
}

export function instagramUrl(handle: string | null | undefined): string | null {
  const user = (handle ?? '').trim()
  return user === '' ? null : `https://instagram.com/${encodeURIComponent(user)}`
}

// ---------- what the landing renders ----------

/** A service of the landing: only what the API sent. */
export type LandingService = {
  id: string
  name: string
  durationMin: number
  priceCents: number
  /** `null` when the salon wrote none: the card hides the line. */
  description: string | null
}

export function toLandingServices(services: readonly PublicService[]): LandingService[] {
  return services.map((service) => ({
    id: service.id,
    name: service.name,
    durationMin: service.durationMin,
    priceCents: service.priceCents,
    description: service.description ?? null,
  }))
}

export type LandingPackage = {
  id: string
  name: string
  priceCents: number
  /** `5 sesiones de Corte · vigencia 90 días` / `sin vencimiento`. */
  detail: string
}

export type NavLink = { href: string; label: string }

export type LandingModel = {
  name: string
  description: string | null
  address: string | null
  phone: { display: string; tel: string | null; whatsapp: string | null } | null
  email: string | null
  instagram: { handle: string; url: string } | null
  /** Zone of the salon, used to highlight today's hours; null when not sent. */
  timezone: string | null
  hours: SalonDay[] | null
  services: LandingService[]
  team: { name: string; trade: string | null }[]
  gallery: PublicPhoto[]
  packages: LandingPackage[]
  /** First gallery photo, used behind the hero; null = a hero without photo. */
  heroPhoto: PublicPhoto | null
  sections: { services: boolean; team: boolean; packages: boolean; gallery: boolean }
  /** The booking form needs at least one service. */
  canBook: boolean
  nav: NavLink[]
}

function packageDetail(pkg: PublicPackage): string {
  const sessions = `${pkg.sessions} ${pkg.sessions === 1 ? 'sesión' : 'sesiones'}`
  const sessionsOf = pkg.serviceName === '' ? sessions : `${sessions} de ${pkg.serviceName}`
  const validity =
    pkg.validityDays === null
      ? 'sin vencimiento'
      : `vigencia ${pkg.validityDays} ${pkg.validityDays === 1 ? 'día' : 'días'}`
  return `${sessionsOf} · ${validity}`
}

/** Decides every section of the landing from the payload; no constant of any salon. */
export function buildLanding(data: PublicSalon): LandingModel {
  const { salon } = data
  const services = toLandingServices(data.services)
  const gallery = data.gallery
  const sections = {
    services: services.length > 0,
    team: data.team.length > 0,
    packages: data.packages.length > 0,
    gallery: gallery.length > 0,
  }
  const nav: NavLink[] = []
  if (sections.services) {
    nav.push({ href: '#servicios', label: 'Servicios' })
  }
  if (sections.team) {
    nav.push({ href: '#equipo', label: 'Equipo' })
  }
  if (sections.packages) {
    nav.push({ href: '#paquetes', label: 'Paquetes' })
  }
  if (sections.gallery) {
    nav.push({ href: '#galeria', label: 'Galería' })
  }
  const instagram = instagramUrl(salon.instagram)
  return {
    name: salon.name,
    description: salon.description ?? null,
    address: salon.address ?? null,
    phone: salon.phone
      ? { display: salon.phone, tel: telHref(salon.phone), whatsapp: whatsappUrl(salon.phone) }
      : null,
    email: salon.email ?? null,
    instagram: salon.instagram && instagram ? { handle: salon.instagram, url: instagram } : null,
    timezone: salon.timezone ?? null,
    hours: data.hours,
    services,
    team: data.team.map((member) => ({
      name: member.name,
      trade: member.trade === '' ? null : member.trade,
    })),
    gallery,
    packages: data.packages.map((pkg) => ({
      id: pkg.id,
      name: pkg.name,
      priceCents: pkg.priceCents,
      detail: packageDetail(pkg),
    })),
    heroPhoto: gallery[0] ?? null,
    sections,
    canBook: services.length > 0,
    nav,
  }
}

/**
 * State after a failed (re)load. A salon already on screen is KEPT on a transient
 * failure (for example the automatic reload when the connection comes back): only a
 * 404 replaces it, because a salon that stopped existing must not keep showing.
 */
export function landingStateAfterFailure(
  previous: 'loading' | 'ready' | 'not_found' | 'error',
  failure: 'not_found' | 'error',
): 'loading' | 'ready' | 'not_found' | 'error' {
  return previous === 'ready' && failure === 'error' ? 'ready' : failure
}

/** A 404 is «No encontramos este salón.»; every other failure is the retryable error. */
export function landingFailure(error: unknown): 'not_found' | 'error' {
  return isRecord(error) && error['status'] === 404 ? 'not_found' : 'error'
}
