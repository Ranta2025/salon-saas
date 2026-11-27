/**
 * Public payload of `GET /api/v1/public/salons/:slug` (no auth) and the PURE
 * builders that decide what the landing renders (F16, D4/D5).
 *
 * The landing has no salon data of its own: identity, contact, services, team,
 * gallery, packages and hours all come from this payload. A section without data
 * is not rendered, and a failed request is an error state, never a catalogue
 * invented by the page.
 */

import { normalizeDays, type SalonDay } from '../features/team/lib/weeklyHours.ts'

export type PublicService = {
  id: string
  name: string
  durationMin: number
  priceCents: number
  /** Present only when the salon wrote one; the page hides the line otherwise. */
  description?: string
  /**
   * `deposit_cents` (F11, D11): the deposit the server computed for this service with
   * the formula of D4. Present only when it is a usable amount: the page prints it and
   * NEVER recomputes it.
   */
  depositCents?: number
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

/**
 * Loyalty program of the salon (F9, D8): present only when the owner switched it on
 * and its reward service is still active. The public has no identity, so there is
 * no progress here: only the rule.
 */
export type PublicLoyalty = {
  visitsRequired: number
  rewardServiceName: string
}

/**
 * Deposit policy as the public payload sends it (F11, D11): the rule, never an
 * amount of a service (that travels per service, in `deposit_cents`).
 */export type PublicDeposit = {
  percent: number
  holdMinutes: number
  refundHours: number
  /** The gateway is the simulated bank: the page says so (D11, D13). */
  simulated: boolean
}

/**
 * One review of the landing (F13, D7): plain text, the author exactly as the server
 * decided it (first name and the initial of the last name, D5) and the date it was
 * written. Nothing else: no email, no phone, no turno.
 */
export type PublicReviewItem = {
  /** 1..5. */
  rating: number
  comment: string
  author: string
  /** `YYYY-MM-DD` or an ISO date; may be empty (the section hides the line). */
  date: string
}

/**
 * Reviews of the public page (F13, D7): ADDITIVE key and OMITTED when the salon has
 * no visible review. `average` and `count` are the ones the SERVER computed over
 * every visible review; `items` are at most the 6 most recent visible ones.
 */
export type PublicReviews = {
  /** One decimal, for example 4.6. */
  average: number
  count: number
  items: PublicReviewItem[]
}

export type PublicSalon = {
  salon: PublicSalonInfo
  services: PublicService[]
  team: PublicTeamMember[]
  gallery: PublicPhoto[]
  packages: PublicPackage[]
  /**
   * `deposit` (F11, D11): the deposit policy of the salon, present ONLY when it is on.
   * `null` when the API did not send it: the landing renders nothing about deposits.
   */
  deposit?: PublicDeposit | null
  /**
   * `loyalty` (F9, D8): `null` when the API sends none, `null` or something unusable.
   * Optional so code that builds a payload by hand (the profile preview) needs no
   * change; a missing field and `null` mean the same.
   */
  loyalty?: PublicLoyalty | null
  /**
   * `reviews` (F13, D7): the reviews of the salon, present ONLY when there is at
   * least one visible one. `null` when the API did not send it, so the landing
   * renders no section instead of an empty one. Optional so a payload built by hand
   * (the profile preview) needs no change.
   */
  reviews?: PublicReviews | null
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
  // The deposit amount comes from the server (D11): a zero or a missing field means
  // «this service has no deposit» and the card hides the line.
  const depositCents = integer(value['deposit_cents'])
  if (depositCents !== null && depositCents >= 1) {
    service.depositCents = depositCents
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

/** Visits of a loyalty program (D4: 2..50). */
const MIN_LOYALTY_VISITS = 2
const MAX_LOYALTY_VISITS = 50

/** Verbatim public sentence of the program (D8): «Cada N visitas, <servicio> de regalo». */
export function loyaltySentence(visitsRequired: number, serviceName: string): string {
  return `Cada ${visitsRequired} visitas, ${serviceName} de regalo`
}

/** Bounds of the deposit policy (F11, D2): percent 10..100, hold 5..120, refund 0..168. */
const MIN_DEPOSIT_PERCENT = 10
const MAX_DEPOSIT_PERCENT = 100
const MIN_HOLD_MINUTES = 5
const MAX_HOLD_MINUTES = 120
const MIN_REFUND_HOURS = 0
const MAX_REFUND_HOURS = 168

/** The sentence of the preview and of the landing (F11, D11/D13), verbatim. */
export function depositSentence(percent: number): string {
  return `Seña del ${percent}% - pago simulado`
}

/** «El turno queda guardado 30 minutos.» */
export function holdLabel(minutes: number): string {
  return `El turno queda guardado ${minutes} ${minutes === 1 ? 'minuto' : 'minutos'}.`
}

/** «Devolución hasta 24 horas antes del turno.» / «Sin devolución.» */
export function refundLabel(hours: number): string {
  if (hours <= 0) {
    return 'Sin devolución.'
  }
  return `Devolución hasta ${hours} ${hours === 1 ? 'hora' : 'horas'} antes del turno.`
}

/** `deposit` of the public payload; anything that is not a usable policy is `null`. */
function normalizeDeposit(value: unknown): PublicDeposit | null {
  if (!isRecord(value)) {
    return null
  }
  const percent = integer(value['percent'])
  const hold = integer(value['hold_minutes'])
  const refund = integer(value['refund_hours'])
  if (
    percent === null ||
    percent < MIN_DEPOSIT_PERCENT ||
    percent > MAX_DEPOSIT_PERCENT ||
    hold === null ||
    hold < MIN_HOLD_MINUTES ||
    hold > MAX_HOLD_MINUTES ||
    refund === null ||
    refund < MIN_REFUND_HOURS ||
    refund > MAX_REFUND_HOURS
  ) {
    return null
  }
  return { percent, holdMinutes: hold, refundHours: refund, simulated: value['simulated'] === true }
}

/** `loyalty` of the public payload; anything that is not a usable rule is `null`. */
function normalizeLoyalty(value: unknown): PublicLoyalty | null {
  if (!isRecord(value)) {
    return null
  }
  const visits = integer(value['visits_required'])
  const name = text(value['reward_service_name'])
  if (visits === null || visits < MIN_LOYALTY_VISITS || visits > MAX_LOYALTY_VISITS || name === '') {
    return null
  }
  return { visitsRequired: visits, rewardServiceName: name }
}

/** Reviews of the landing are capped by the contract (D7: the 6 most recent). */
export const MAX_LANDING_REVIEWS = 6

/** One review; a row without a rating 1..5 or without an author is dropped. */
function normalizePublicReview(value: unknown): PublicReviewItem | null {
  if (!isRecord(value)) {
    return null
  }
  const rating = integer(value['rating'])
  const author = text(value['author'])
  if (rating === null || rating < 1 || rating > 5 || author === '') {
    return null
  }
  return { rating, comment: text(value['comment']), author, date: text(value['date']) }
}

/**
 * `reviews` of the public payload; anything that is not a usable summary is `null`.
 * The average is NOT recomputed here: the server owns it (D7). A count of zero means
 * «no visible reviews», so the section is not rendered and the key is as if omitted.
 */
function normalizeReviews(value: unknown): PublicReviews | null {
  if (!isRecord(value)) {
    return null
  }
  const count = integer(value['count'])
  const average = value['average']
  if (count === null || count < 1) {
    return null
  }
  if (typeof average !== 'number' || !Number.isFinite(average) || average < 1 || average > 5) {
    return null
  }
  if (!Array.isArray(value['items'])) {
    // The average and the count are the server's, but a summary with no rows at all
    // is not the documented payload: the section is not rendered for it.
    return null
  }
  return {
    // The server sends one decimal already: this only removes float noise.
    average: Math.round(average * 10) / 10,
    count,
    items: rowsOf(value['items'], normalizePublicReview).slice(0, MAX_LANDING_REVIEWS),
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
    loyalty: normalizeLoyalty(raw['loyalty']),
    deposit: normalizeDeposit(raw['deposit']),
    reviews: normalizeReviews(raw['reviews']),
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
  /** `deposit_cents` of the server (F11, D11); `null` = this service has no deposit. */
  depositCents: number | null
}

export function toLandingServices(services: readonly PublicService[]): LandingService[] {
  return services.map((service) => ({
    id: service.id,
    name: service.name,
    durationMin: service.durationMin,
    priceCents: service.priceCents,
    description: service.description ?? null,
    depositCents: service.depositCents ?? null,
  }))
}

export type LandingPackage = {
  id: string
  name: string
  priceCents: number
  /** `5 sesiones de Corte · vigencia 90 días` / `sin vencimiento`. */
  detail: string
}

/** Section «Programa de fidelidad» of the landing (D8). */
export type LandingLoyalty = {
  visitsRequired: number
  rewardServiceName: string
  /** `Cada 10 visitas, Corte de regalo`. */
  sentence: string
}

/**
 * Section «Seña» of the landing (F11, D11). Rendered ONLY when the payload carries
 * `deposit`; every sentence of the section comes from those four numbers.
 */
export type LandingDeposit = {
  percent: number
  holdMinutes: number
  refundHours: number
  simulated: boolean
  /** `Seña del 30% - pago simulado`. */
  sentence: string
  /** «El turno queda guardado 30 minutos.» */
  holdLabel: string
  /** «Devolución hasta 24 horas antes del turno.» */
  refundLabel: string
}

export type NavLink = { href: string; label: string }

/** «4.6»: the average the server computed, always with one decimal. */
export function reviewsAverageLabel(average: number): string {
  return average.toFixed(1)
}

/** «1 reseña» / «12 reseñas». */
export function reviewsCountLabel(count: number): string {
  return count === 1 ? '1 reseña' : `${count} reseñas`
}

/**
 * Section «Reseñas» of the landing (F13, D7). Rendered ONLY when the payload carries
 * `reviews`; the average, the count and the rows are the ones the SERVER sent, never
 * recounted here (an occluded review disappears from the web and from the average).
 */
export type LandingReviews = {
  average: number
  count: number
  /** `4.6`. */
  averageLabel: string
  /** «12 reseñas». */
  countLabel: string
  items: PublicReviewItem[]
}

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
  /** `null` = the salon has no active program: the section is not rendered. */
  loyalty: LandingLoyalty | null
  /** `null` = the salon charges no deposit: the section is not rendered (F11, D11). */
  deposit: LandingDeposit | null
  /** `null` = there are no visible reviews: the section is not rendered (F13, D7). */
  reviews: LandingReviews | null
  /** First gallery photo, used behind the hero; null = a hero without photo. */
  heroPhoto: PublicPhoto | null
  sections: {
    services: boolean
    team: boolean
    packages: boolean
    loyalty: boolean
    deposit: boolean
    gallery: boolean
  }
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
  const program = data.loyalty ?? null
  const policy = data.deposit ?? null
  const reviews = data.reviews ?? null
  const sections = {
    services: services.length > 0,
    team: data.team.length > 0,
    packages: data.packages.length > 0,
    loyalty: program !== null,
    deposit: policy !== null,
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
  if (sections.loyalty) {
    nav.push({ href: '#fidelidad', label: 'Fidelidad' })
  }
  if (sections.deposit) {
    nav.push({ href: '#sena', label: 'Seña' })
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
    loyalty:
      program === null
        ? null
        : {
            visitsRequired: program.visitsRequired,
            rewardServiceName: program.rewardServiceName,
            sentence: loyaltySentence(program.visitsRequired, program.rewardServiceName),
          },
    deposit:
      policy === null
        ? null
        : {
            percent: policy.percent,
            holdMinutes: policy.holdMinutes,
            refundHours: policy.refundHours,
            simulated: policy.simulated,
            sentence: depositSentence(policy.percent),
            holdLabel: holdLabel(policy.holdMinutes),
            refundLabel: refundLabel(policy.refundHours),
          },
    // F13 (D7): driven only by the payload `reviews`; null renders no section. It is
    // deliberately NOT part of `sections`/`nav`: the public page gained no new menu
    // entry, so the landing keeps the navigation it already had.
    reviews:
      reviews === null
        ? null
        : {
            average: reviews.average,
            count: reviews.count,
            averageLabel: reviewsAverageLabel(reviews.average),
            countLabel: reviewsCountLabel(reviews.count),
            items: reviews.items,
          },
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
