/**
 * Public catalogue of `GET /api/v1/public/salons/:slug` (no auth). The landing
 * renders it when the request succeeds and keeps the bundled catalogue as a
 * fallback when the API is unavailable.
 */

import type { LandingService } from '../landing/content.ts'

export type PublicService = {
  id: string
  name: string
  durationMin: number
  priceCents: number
}

export type PublicSalon = {
  salon: { id: string; slug: string; name: string }
  services: PublicService[]
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function integer(value: unknown): number | null {
  return typeof value === 'number' && Number.isInteger(value) ? value : null
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
  return { id, name, durationMin: duration, priceCents: price }
}

/** Returns null when the payload has no usable service (caller keeps the fallback). */
export function normalizePublicSalon(raw: unknown): PublicSalon | null {
  if (!isRecord(raw)) {
    return null
  }
  const services: PublicService[] = []
  const rows = Array.isArray(raw['services']) ? raw['services'] : []
  for (const row of rows) {
    const service = normalizeService(row)
    if (service) {
      services.push(service)
    }
  }
  if (services.length === 0) {
    return null
  }
  const salon = isRecord(raw['salon']) ? raw['salon'] : {}
  return {
    salon: {
      id: text(salon['id']),
      slug: text(salon['slug']),
      name: text(salon['name']),
    },
    services,
  }
}

/**
 * API rows win on id, name, duration and price; the static catalogue keeps the
 * marketing copy for services that still exist under the same name.
 */
export function toLandingServices(
  services: readonly PublicService[],
  fallback: readonly LandingService[],
): readonly LandingService[] {
  if (services.length === 0) {
    return fallback
  }
  return services.map((service) => {
    const known = fallback.find(
      (item) => item.name.toLowerCase() === service.name.toLowerCase(),
    )
    return {
      id: service.id,
      name: service.name,
      durationMin: service.durationMin,
      priceCents: service.priceCents,
      description: known?.description ?? 'Servicio del salón, con precio final en pesos.',
      tag: known?.tag,
    }
  })
}
