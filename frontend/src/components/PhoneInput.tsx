import { useEffect, useRef, useState, type ChangeEvent, type ClipboardEvent } from 'react'
import {
  COUNTRIES,
  DEFAULT_COUNTRY_ID,
  digitsOnly,
  formatNational,
  phoneCountry,
  phoneHint,
  splitPhone,
  toE164,
  validatePhone,
  type CountryId,
  type PhoneParts,
} from '../lib/phone'

type PhoneInputProps = {
  /** Id of the national-number input; the selector and hints derive their ids. */
  id: string
  label: string
  /**
   * Value in the shape the API receives (`+5355528595`). An old free-form phone
   * is read with the fallback country, never thrown away.
   */
  value: string
  /** Emits `+<code><digits>` (or `''` while empty) on every edit. */
  onChange: (value: string) => void
  /** Country of a value without prefix: Cuba, the salon's country. */
  defaultCountry?: CountryId
  /** Message coming from the form/server; it wins over the local validation. */
  error?: string | null
  /** Extra text before the example of the country. */
  hint?: string
  name?: string
  required?: boolean
  /** `tel-national`: the selector already holds the country code. */
  autoComplete?: string
  className?: string
}

function emittedValue(parts: PhoneParts): string {
  return toE164(parts)
}

/**
 * Phone field with a country-code selector plus a national number.
 *
 * - The selector is a native `<select>`: labelled, keyboard operable and with a
 *   visible "Cuba +53" style option text.
 * - The national number accepts digits only and is shown grouped with spaces;
 *   `inputMode="tel"` opens the numeric keyboard on phones.
 * - Pasting a full international number (`+53 5552 8595`, `0053…`, `53…`)
 *   switches the selector to that country and splits the digits.
 */
export function PhoneInput({
  id,
  label,
  value,
  onChange,
  defaultCountry = DEFAULT_COUNTRY_ID,
  error = null,
  hint,
  name,
  required = false,
  autoComplete = 'tel-national',
  className,
}: PhoneInputProps) {
  const [parts, setParts] = useState<PhoneParts>(() => splitPhone(value, defaultCountry))
  const [touched, setTouched] = useState(false)
  const lastEmitted = useRef<string>(emittedValue(splitPhone(value, defaultCountry)))

  // A value changed from the outside (form reset, loaded record) is re-read.
  useEffect(() => {
    if (value === lastEmitted.current) {
      return
    }
    const next = splitPhone(value, defaultCountry)
    setParts(next)
    lastEmitted.current = emittedValue(next)
  }, [value, defaultCountry])

  const country = phoneCountry(parts.countryId)
  const hintId = `${id}-hint`
  const errorId = `${id}-error`

  const commit = (next: PhoneParts): void => {
    setParts(next)
    const emitted = emittedValue(next)
    lastEmitted.current = emitted
    onChange(emitted)
  }

  // Digits are never silently dropped: a number longer than the country allows
  // stays visible with the message of `validatePhone` instead of being truncated
  // into a different (valid looking) phone.
  const handleNumber = (event: ChangeEvent<HTMLInputElement>): void => {
    commit({ countryId: parts.countryId, digits: digitsOnly(event.target.value) })
  }

  const handlePaste = (event: ClipboardEvent<HTMLInputElement>): void => {
    const text = event.clipboardData?.getData('text') ?? ''
    const international = /^\s*(\+|00)/.test(text) || digitsOnly(text).length > country.maxDigits
    if (text.trim() === '' || !international) {
      // A short national fragment inserts where the caret is; the change handler
      // still normalizes it to digits.
      return
    }
    event.preventDefault()
    commit(splitPhone(text, parts.countryId))
  }

  const handleCountry = (event: ChangeEvent<HTMLSelectElement>): void => {
    commit({ countryId: event.target.value as CountryId, digits: parts.digits })
  }

  const localError = touched ? validatePhone(parts) : null
  const message = error ?? localError
  const describedBy = [hintId, message !== null ? errorId : ''].filter(Boolean).join(' ')

  return (
    <div className={className ? `field phone-field ${className}` : 'field phone-field'}>
      <label htmlFor={id}>{label}</label>
      <div className="phone-field__row">
        <select
          id={`${id}-country`}
          className="select phone-field__country"
          aria-label="Código de país"
          value={parts.countryId}
          onChange={handleCountry}
        >
          {COUNTRIES.map((option) => (
            <option key={option.id} value={option.id}>
              {`${option.optionLabel} +${option.dialCode}`}
            </option>
          ))}
        </select>
        <input
          id={id}
          className="input phone-field__number"
          name={name}
          type="tel"
          inputMode="tel"
          autoComplete={autoComplete}
          placeholder={country.placeholder}
          value={formatNational(parts.digits, parts.countryId)}
          onChange={handleNumber}
          onPaste={handlePaste}
          onBlur={() => setTouched(true)}
          required={required}
          aria-describedby={describedBy}
          aria-invalid={message !== null ? true : undefined}
        />
      </div>
      <p className="field__hint" id={hintId}>
        {hint ? `${hint} · ${phoneHint(parts.countryId)}` : phoneHint(parts.countryId)}
      </p>
      {/* The local validation is not an alert while typing: it appears once the
          field loses focus and disappears as soon as the number is complete. A
          message from the form (server rejection) is announced. */}
      {message !== null ? (
        <p className="field__error" id={errorId} role={error !== null ? 'alert' : undefined}>
          {message}
        </p>
      ) : null}
    </div>
  )
}
