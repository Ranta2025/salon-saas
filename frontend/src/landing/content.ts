/**
 * Generic product copy of the public landing. UI copy is Spanish, identifiers
 * English.
 *
 * Since F16 this file holds NO data of any salon: identity, contact, services,
 * team, photos and hours come from `GET /api/v1/public/salons/:slug` (see
 * `lib/publicSalon.ts`). Do not add a name, phone, address, price, statistic,
 * testimonial or claim about a salon here.
 */

/**
 * Text shown while the public API does not send the `hours` of the salon
 * (F2d1, S-F2d1-6). The landing never invents opening hours: it invites to
 * confirm them on WhatsApp instead (only when the salon has a phone).
 */
export const PUBLIC_HOURS_FALLBACK = 'Consultá el horario por WhatsApp'
