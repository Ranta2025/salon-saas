/**
 * Operational settings of the salon: time zone, opening window and slot step
 * (`GET/PUT /api/v1/salons/:id/settings`). The form validates before sending so
 * the person sees the field that is wrong, not a generic 400.
 */

import { request } from '../../../lib/api.ts'
import { normalizeSalonHours, type SalonDay, type SalonHours } from '../../team/lib/weeklyHours.ts'

export type SalonSettings = {
  timezone: string
  openTime: string
  closeTime: string
  slotStepMin: number
}

export type SettingsField = 'timezone' | 'openTime' | 'closeTime' | 'slotStepMin'

export type SettingsValidation =
  | { ok: true; settings: SalonSettings }
  | { ok: false; field: SettingsField; message: string }

export const SLOT_STEPS = [15, 30, 60] as const

export type SlotStep = (typeof SLOT_STEPS)[number]

/** Common IANA zones of Latin America, Spain and UTC (the select of the page). */
export const TIME_ZONES: readonly { value: string; label: string }[] = [
  { value: 'America/Mexico_City', label: 'Ciudad de México (centro)' },
  { value: 'America/Monterrey', label: 'Monterrey' },
  { value: 'America/Cancun', label: 'Cancún' },
  { value: 'America/Tijuana', label: 'Tijuana' },
  { value: 'America/Guatemala', label: 'Ciudad de Guatemala' },
  { value: 'America/Costa_Rica', label: 'San José de Costa Rica' },
  { value: 'America/El_Salvador', label: 'San Salvador' },
  { value: 'America/Panama', label: 'Panamá' },
  { value: 'America/Bogota', label: 'Bogotá' },
  { value: 'America/Lima', label: 'Lima' },
  { value: 'America/Caracas', label: 'Caracas' },
  { value: 'America/La_Paz', label: 'La Paz' },
  { value: 'America/Santiago', label: 'Santiago de Chile' },
  { value: 'America/Asuncion', label: 'Asunción' },
  { value: 'America/Montevideo', label: 'Montevideo' },
  { value: 'America/Argentina/Buenos_Aires', label: 'Buenos Aires' },
  { value: 'America/Sao_Paulo', label: 'São Paulo' },
  { value: 'America/Santo_Domingo', label: 'Santo Domingo' },
  { value: 'America/Havana', label: 'La Habana' },
  { value: 'America/Puerto_Rico', label: 'San Juan de Puerto Rico' },
  { value: 'Europe/Madrid', label: 'Madrid' },
  { value: 'Atlantic/Canary', label: 'Islas Canarias' },
  { value: 'UTC', label: 'UTC (sin horario local)' },
]

export const DEFAULT_SETTINGS: SalonSettings = {
  timezone: 'America/Mexico_City',
  openTime: '09:00',
  closeTime: '19:00',
  slotStepMin: 30,
}

const TIME_PATTERN = /^([01]\d|2[0-3]):([0-5]\d)$/

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value)
}

function text(value: unknown): string {
  return typeof value === 'string' ? value.trim() : ''
}

function timeOrDefault(value: unknown, fallback: string): string {
  const candidate = text(value)
  return TIME_PATTERN.test(candidate) ? candidate : fallback
}

function stepOrDefault(value: unknown): number {
  return SLOT_STEPS.includes(value as SlotStep) ? (value as SlotStep) : DEFAULT_SETTINGS.slotStepMin
}

/** Reads the API body; an unusable payload falls back to the defaults. */
export function normalizeSettings(raw: unknown): SalonSettings {
  if (!isRecord(raw)) {
    return { ...DEFAULT_SETTINGS }
  }
  const timezone = text(raw['timezone'])
  if (timezone === '') {
    return { ...DEFAULT_SETTINGS }
  }
  return {
    timezone,
    openTime: timeOrDefault(raw['open_time'], DEFAULT_SETTINGS.openTime),
    closeTime: timeOrDefault(raw['close_time'], DEFAULT_SETTINGS.closeTime),
    slotStepMin: stepOrDefault(raw['slot_step_min']),
  }
}

export function toSettingsPayload(settings: SalonSettings): {
  timezone: string
  open_time: string
  close_time: string
  slot_step_min: number
} {
  return {
    timezone: settings.timezone.trim(),
    open_time: settings.openTime,
    close_time: settings.closeTime,
    slot_step_min: settings.slotStepMin,
  }
}

function isValidTimeZone(timeZone: string): boolean {
  try {
    new Intl.DateTimeFormat('es-MX', { timeZone })
    return true
  } catch {
    return false
  }
}

/** Whole minutes since midnight of an `HH:MM` value (already validated). */
function minutesOfDay(value: string): number {
  const match = TIME_PATTERN.exec(value)
  if (!match) {
    return Number.NaN
  }
  return Number(match[1]) * 60 + Number(match[2])
}

export function validateSettings(draft: SalonSettings): SettingsValidation {
  const timezone = draft.timezone.trim()
  if (timezone === '') {
    return { ok: false, field: 'timezone', message: 'Elegí la zona horaria del salón.' }
  }
  if (!isValidTimeZone(timezone)) {
    return {
      ok: false,
      field: 'timezone',
      message: 'Esa zona horaria no existe. Elegí una de la lista.',
    }
  }
  const openTime = draft.openTime.trim()
  if (!TIME_PATTERN.test(openTime)) {
    return {
      ok: false,
      field: 'openTime',
      message: 'Escribí la hora de apertura como HH:MM (por ejemplo 09:30).',
    }
  }
  const closeTime = draft.closeTime.trim()
  if (!TIME_PATTERN.test(closeTime)) {
    return {
      ok: false,
      field: 'closeTime',
      message: 'Escribí la hora de cierre como HH:MM (por ejemplo 19:00).',
    }
  }
  if (minutesOfDay(closeTime) <= minutesOfDay(openTime)) {
    return {
      ok: false,
      field: 'closeTime',
      message: 'La hora de cierre debe ser después de la de apertura.',
    }
  }
  if (!SLOT_STEPS.includes(draft.slotStepMin as SlotStep)) {
    return {
      ok: false,
      field: 'slotStepMin',
      message: 'El paso de los turnos debe ser de 15, 30 o 60 minutos.',
    }
  }
  return {
    ok: true,
    settings: {
      timezone,
      openTime,
      closeTime,
      slotStepMin: draft.slotStepMin,
    },
  }
}

function settingsPath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/settings`
}

export async function fetchSettings(salonId: string): Promise<SalonSettings> {
  return normalizeSettings(await request<unknown>(settingsPath(salonId)))
}

export async function saveSettings(
  salonId: string,
  draft: SalonSettings,
): Promise<SalonSettings> {
  const body = await request<unknown>(settingsPath(salonId), {
    method: 'PUT',
    body: toSettingsPayload(draft),
  })
  return normalizeSettings(body)
}

// --- weekly hours of the salon (F2d1) ---

function hoursPath(salonId: string): string {
  return `/api/v1/salons/${encodeURIComponent(salonId)}/hours`
}

/**
 * `GET /salons/:id/hours` (contract S-F2d1-4): the open days and whether they
 * are custom. `custom = false` means the single opening window of `/settings`
 * expanded to the seven days.
 */
export async function fetchSalonHours(salonId: string): Promise<SalonHours> {
  return normalizeSalonHours(await request<unknown>(hoursPath(salonId)))
}

/**
 * `PUT /salons/:id/hours`: atomic replacement. An empty list goes back to the
 * single opening window of the settings page. The server sentences of a `400`
 * («Máximo 4 tramos por día», «Día inválido»…) reach the screen verbatim.
 */
export async function saveSalonHours(
  salonId: string,
  days: readonly SalonDay[],
): Promise<SalonHours> {
  const body = await request<unknown>(hoursPath(salonId), {
    method: 'PUT',
    body: {
      days: days.map((day) => ({ weekday: day.weekday, open: day.open, close: day.close })),
    },
  })
  return normalizeSalonHours(body)
}
