/**
 * Phone numbers of the product: one country code per market, digits only stored
 * and an E.164-ish `+<code><digits>` value sent to the API (the backend
 * normalizes it to digits, so the contract does not change).
 *
 * Everything here is pure: the same helpers feed the public booking form, the
 * staff client form and the read-only displays of existing files.
 */

export type CountryId =
  | 'CU'
  | 'MX'
  | 'US'
  | 'ES'
  | 'AR'
  | 'CO'
  | 'VE'
  | 'CL'
  | 'PE'
  | 'UY'
  | 'PA'
  | 'DO'

export type PhoneCountry = {
  id: CountryId
  /** Full name used in messages ("El teléfono de Cuba…"). */
  name: string
  /** Short label of the selector option: the widest one must fit 390 px. */
  optionLabel: string
  /** Dial code without the plus (`53`). */
  dialCode: string
  minDigits: number
  maxDigits: number
  /** Example national number, placeholders included ("5xxx xxxx"). */
  placeholder: string
  /** How to group the digits when showing them ("5552 8595"). */
  groups: readonly number[]
}

/**
 * Catalogue in display order: Cuba first because it is the default, then the
 * markets the salon quotes to. Two countries can share a dial code (`+1`), so
 * `id` — never `dialCode` — is the key of the selector.
 */
export const COUNTRIES: readonly PhoneCountry[] = [
  {
    id: 'CU',
    name: 'Cuba',
    optionLabel: 'Cuba',
    dialCode: '53',
    minDigits: 8,
    maxDigits: 8,
    placeholder: '5xxx xxxx',
    groups: [4, 4],
  },
  {
    id: 'MX',
    name: 'México',
    optionLabel: 'México',
    dialCode: '52',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '55 xxxx xxxx',
    groups: [2, 4, 4],
  },
  {
    id: 'US',
    name: 'Estados Unidos/Canadá',
    optionLabel: 'EE. UU./Canadá',
    dialCode: '1',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '555 123 4567',
    groups: [3, 3, 4],
  },
  {
    id: 'ES',
    name: 'España',
    optionLabel: 'España',
    dialCode: '34',
    minDigits: 9,
    maxDigits: 9,
    placeholder: '612 345 678',
    groups: [3, 3, 3],
  },
  {
    id: 'AR',
    name: 'Argentina',
    optionLabel: 'Argentina',
    dialCode: '54',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '11 xxxx xxxx',
    groups: [2, 4, 4],
  },
  {
    id: 'CO',
    name: 'Colombia',
    optionLabel: 'Colombia',
    dialCode: '57',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '300 123 4567',
    groups: [3, 3, 4],
  },
  {
    id: 'VE',
    name: 'Venezuela',
    optionLabel: 'Venezuela',
    dialCode: '58',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '412 xxx xxxx',
    groups: [3, 3, 4],
  },
  {
    id: 'CL',
    name: 'Chile',
    optionLabel: 'Chile',
    dialCode: '56',
    minDigits: 9,
    maxDigits: 9,
    placeholder: '9 xxxx xxxx',
    groups: [1, 4, 4],
  },
  {
    id: 'PE',
    name: 'Perú',
    optionLabel: 'Perú',
    dialCode: '51',
    minDigits: 9,
    maxDigits: 9,
    placeholder: '912 345 678',
    groups: [3, 3, 3],
  },
  {
    id: 'UY',
    name: 'Uruguay',
    optionLabel: 'Uruguay',
    dialCode: '598',
    minDigits: 8,
    maxDigits: 8,
    placeholder: '94 123 456',
    groups: [2, 3, 3],
  },
  {
    id: 'PA',
    name: 'Panamá',
    optionLabel: 'Panamá',
    dialCode: '507',
    minDigits: 7,
    maxDigits: 8,
    placeholder: '6xxx xxxx',
    groups: [4, 4],
  },
  {
    id: 'DO',
    name: 'República Dominicana',
    optionLabel: 'R. Dominicana',
    dialCode: '1',
    minDigits: 10,
    maxDigits: 10,
    placeholder: '809 123 4567',
    groups: [3, 3, 4],
  },
]

/** Cuba is the salon's country: every form opens with `+53`. */
export const DEFAULT_COUNTRY_ID: CountryId = 'CU'

/** A phone split in the two parts the form edits. */
export type PhoneParts = { countryId: CountryId; digits: string }

const byId = new Map<CountryId, PhoneCountry>(COUNTRIES.map((country) => [country.id, country]))

/** Dial codes longest first: `+598` must win over a shorter `+5`-style prefix. */
const byDialCode = [...COUNTRIES].sort((a, b) => b.dialCode.length - a.dialCode.length)

export function phoneCountry(id: CountryId): PhoneCountry {
  return byId.get(id) ?? (byId.get(DEFAULT_COUNTRY_ID) as PhoneCountry)
}

/** Digits of any typed value, with every space, dash or plus removed. */
export function digitsOnly(value: string): string {
  return value.replace(/\D/g, '')
}

/** `55528595` + Cuba → `5552 8595` (grouping of the country, no invented digits). */
export function formatNational(digits: string, countryId: CountryId): string {
  const country = phoneCountry(countryId)
  const clean = digitsOnly(digits)
  const parts: string[] = []
  let index = 0
  for (const size of country.groups) {
    if (index >= clean.length) {
      break
    }
    parts.push(clean.slice(index, index + size))
    index += size
  }
  if (index < clean.length) {
    parts.push(clean.slice(index))
  }
  return parts.join(' ')
}

/** The value the API receives: `+5355528595`; empty while there are no digits. */
export function toE164(parts: PhoneParts): string {
  const digits = digitsOnly(parts.digits)
  if (digits === '') {
    return ''
  }
  return `+${phoneCountry(parts.countryId).dialCode}${digits}`
}

type DialMatch = { country: PhoneCountry; digits: string }

function matchDialCode(digits: string, requireValidLength: boolean): DialMatch | null {
  let loose: DialMatch | null = null
  for (const country of byDialCode) {
    if (!digits.startsWith(country.dialCode) || digits.length === country.dialCode.length) {
      continue
    }
    const rest = digits.slice(country.dialCode.length)
    if (rest.length >= country.minDigits && rest.length <= country.maxDigits) {
      return { country, digits: rest }
    }
    loose ??= { country, digits: rest }
  }
  return requireValidLength ? null : loose
}

/**
 * Splits what the person typed or pasted into country code + national number.
 *
 * `+53 5552 8595`, `0053…` and `53…` (when it is longer than the fallback
 * country) are read as international numbers; anything else keeps the fallback
 * country, so an old free-form phone never gets lost.
 */
export function splitPhone(value: string, fallbackCountryId: CountryId = DEFAULT_COUNTRY_ID): PhoneParts {
  const fallback = phoneCountry(fallbackCountryId)
  const text = value.trim()
  if (text === '') {
    return { countryId: fallback.id, digits: '' }
  }
  const explicit = /^\+/.test(text) ? true : /^0{2}/.test(text)
  const body = text.replace(/^\+/, '').replace(/^00/, '')
  const digits = digitsOnly(body)
  if (digits === '') {
    return { countryId: fallback.id, digits: '' }
  }
  if (explicit) {
    const match = matchDialCode(digits, false)
    return match
      ? { countryId: match.country.id, digits: match.digits }
      : { countryId: fallback.id, digits }
  }
  // Without a prefix a number can only be read as international when it would
  // not fit the fallback country: `5512345678` typed in Cuba stays untouched.
  if (digits.length > fallback.maxDigits) {
    const match = matchDialCode(digits, true)
    if (match) {
      return { countryId: match.country.id, digits: match.digits }
    }
  }
  return { countryId: fallback.id, digits }
}

function lengthLabel(country: PhoneCountry): string {
  return country.minDigits === country.maxDigits
    ? `${country.maxDigits} dígitos`
    : `entre ${country.minDigits} y ${country.maxDigits} dígitos`
}

/** Clean Spanish message for an incomplete or too long number; null when valid. */
export function validatePhone(parts: PhoneParts): string | null {
  const country = phoneCountry(parts.countryId)
  const digits = digitsOnly(parts.digits)
  if (digits === '') {
    return 'Escribe el número de teléfono.'
  }
  if (digits.length < country.minDigits || digits.length > country.maxDigits) {
    return `El teléfono de ${country.name} ${
      country.minDigits === country.maxDigits ? 'tiene' : 'debe tener'
    } ${lengthLabel(country)} (escribiste ${digits.length}).`
  }
  return null
}

/**
 * `+5355528595` → `+53 5552 8595` for the read-only places of the panel.
 *
 * A number with its prefix is always reformatted; a free-form old record is only
 * prefixed when its digits fit the default country. Otherwise it is shown as it
 * was stored: guessing a prefix would lie about the number.
 */
export function displayPhone(value: string, fallbackCountryId: CountryId = DEFAULT_COUNTRY_ID): string {
  const text = value.trim()
  if (text === '') {
    return ''
  }
  const explicit = /^(\+|00)/.test(text)
  const parts = splitPhone(text, fallbackCountryId)
  const country = phoneCountry(parts.countryId)
  const digits = digitsOnly(parts.digits)
  if (digits === '') {
    return ''
  }
  const fitsDefault = digits.length >= country.minDigits && digits.length <= country.maxDigits
  if (!explicit && !fitsDefault) {
    return text
  }
  return `+${country.dialCode} ${formatNational(digits, parts.countryId)}`
}

/** Hint under the field: the example of the chosen country and its length. */
export function phoneHint(countryId: CountryId): string {
  const country = phoneCountry(countryId)
  return `Ejemplo: ${country.placeholder} · ${lengthLabel(country)}`
}
