import { useState, type KeyboardEvent } from 'react'
import { PhoneInput } from '@/components/PhoneInput'
import { isEmailErrorMessage, normalizeEmail } from '@/lib/email'
import {
  WAITLIST_DATE_MISSING,
  WAITLIST_FORM_TITLE,
  WAITLIST_JOIN_FALLBACK,
  WAITLIST_SUCCESS,
  describeWaitlistError,
  isWaitlistDate,
  joinWaitlist,
  validateWaitlistDraft,
  waitlistDayLabel,
  type WaitlistField,
} from '@/features/waitlist/lib/waitlist'

type WaitlistFormProps = {
  /** Public slug of the salon, as the availability request already uses it. */
  slug: string
  /** Service and day already chosen in the booking form (D9a). */
  serviceId: string
  serviceName: string
  date: string
}

/**
 * «Avisame si se libera un turno» (contract F7, D9a).
 *
 * It appears UNDER the "No quedan horarios libres ese día" message only, so it
 * never competes with a day that still has slots. It reuses the service and the
 * day picked in the booking form and asks for the only channel of the aviso
 * (email), plus an optional phone: the reservation is NOT made here, the slot
 * stays free until someone books it.
 *
 * Every answer of the server is shown verbatim — the three 409 of D4 («Todavía
 * hay horarios libres ese día», «Ya tenés el máximo de avisos activos», «La
 * lista de espera de este salón está completa») — except a 429, which keeps the
 * sentence the public booking flow already uses everywhere else.
 */
export function WaitlistForm({ slug, serviceId, serviceName, date }: WaitlistFormProps) {
  const [name, setName] = useState('')
  const [email, setEmail] = useState('')
  const [phone, setPhone] = useState('')
  const [fieldError, setFieldError] = useState<{ field: WaitlistField; message: string } | null>(
    null,
  )
  const [failure, setFailure] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const [done, setDone] = useState(false)

  const handleSubmit = async (): Promise<void> => {
    if (sending) {
      return
    }
    setFailure(null)
    setFieldError(null)
    if (!isWaitlistDate(date)) {
      setFailure(WAITLIST_DATE_MISSING)
      return
    }
    const check = validateWaitlistDraft({ name, email, phone })
    if (!check.ok) {
      setFieldError({ field: check.field, message: check.message })
      return
    }
    setSending(true)
    try {
      await joinWaitlist(slug, {
        serviceId,
        date,
        name: check.value.name,
        email: check.value.email,
        phone: check.value.phone,
      })
      setDone(true)
    } catch (err: unknown) {
      const next = describeWaitlistError(err, WAITLIST_JOIN_FALLBACK)
      if (next.kind === 'validation' && isEmailErrorMessage(next.message)) {
        // A 400 «Email inválido» belongs next to the email field.
        setFieldError({ field: 'email', message: next.message })
      } else {
        setFailure(next.message)
      }
    } finally {
      setSending(false)
    }
  }

  /**
   * The booking form is a `<form>`: this block lives inside it, so it cannot be
   * another `<form>` (HTML forbids nesting them and the browser would submit the
   * booking instead). Enter in one of these fields is what a form would do, so
   * it is handled here and never reaches the booking submit.
   */
  const handleKeyDown = (event: KeyboardEvent<HTMLDivElement>): void => {
    if (event.key !== 'Enter' || event.defaultPrevented) {
      return
    }
    const target = event.target
    // The country selector of the phone field keeps Enter for itself.
    if (target instanceof HTMLElement && target.tagName === 'SELECT') {
      return
    }
    event.preventDefault()
    void handleSubmit()
  }

  if (done) {
    return (
      <section className="booking__step" id="lista-de-espera" aria-labelledby="waitlist-title">
        <h3 id="waitlist-title">{WAITLIST_FORM_TITLE}</h3>
        <p className="notice notice--success" role="status">
          {WAITLIST_SUCCESS}
        </p>
        <p className="booking__note">
          Guardamos el aviso para {serviceName === '' ? 'este servicio' : serviceName} el{' '}
          {waitlistDayLabel(date)}. Te escribimos a <strong>{normalizeEmail(email)}</strong> si se
          libera un horario: el turno sigue libre hasta que alguien lo reserve.
        </p>
      </section>
    )
  }

  return (
    <section className="booking__step" id="lista-de-espera" aria-labelledby="waitlist-title">
      <h3 id="waitlist-title">{WAITLIST_FORM_TITLE}</h3>
      <p className="booking__note">
        {serviceName === '' ? 'Para este servicio' : `Para ${serviceName}`} el{' '}
        {waitlistDayLabel(date)}. Te avisamos por correo si se libera un horario ese día: no
        reservamos nada por vos.
      </p>
      <div
        className="booking__fields"
        role="group"
        aria-labelledby="waitlist-title"
        onKeyDown={handleKeyDown}
      >
        <div className="field booking__field--wide">
          <label htmlFor="waitlist-name">Nombre</label>
          <input
            id="waitlist-name"
            className="input"
            name="name"
            autoComplete="name"
            value={name}
            onChange={(event) => {
              setName(event.target.value)
              setFieldError(null)
            }}
            aria-invalid={fieldError?.field === 'name' ? true : undefined}
            required
          />
          {fieldError?.field === 'name' ? (
            <p className="field__error" role="alert">
              {fieldError.message}
            </p>
          ) : null}
        </div>

        <div className="field booking__field--wide">
          <label htmlFor="waitlist-email">Correo electrónico</label>
          <input
            id="waitlist-email"
            className="input"
            name="email"
            type="email"
            inputMode="email"
            autoComplete="email"
            placeholder="tucorreo@ejemplo.com"
            value={email}
            onChange={(event) => {
              setEmail(event.target.value)
              setFieldError(null)
            }}
            aria-describedby="waitlist-email-hint"
            aria-invalid={fieldError?.field === 'email' ? true : undefined}
            required
          />
          <p className="field__hint" id="waitlist-email-hint">
            Es el único canal del aviso: si el horario se libera, te escribimos acá.
          </p>
          {fieldError?.field === 'email' ? (
            <p className="field__error" role="alert">
              {fieldError.message}
            </p>
          ) : null}
        </div>

        <PhoneInput
          id="waitlist-phone"
          className="booking__field--wide"
          label="Teléfono (opcional)"
          name="phone"
          value={phone}
          hint="Solo si preferís que te llamemos cuando se libere el horario. "
          error={fieldError?.field === 'phone' ? fieldError.message : null}
          onChange={(next) => {
            setPhone(next)
            setFieldError(null)
          }}
        />

        {failure !== null ? (
          <p className="notice notice--error booking__field--wide" role="alert">
            {failure}
          </p>
        ) : null}

        <button
          type="button"
          className="btn btn--gold booking__submit booking__field--wide"
          disabled={sending}
          onClick={() => void handleSubmit()}
        >
          {sending ? 'Enviando el aviso…' : 'Avisame si se libera'}
        </button>
      </div>
    </section>
  )
}
