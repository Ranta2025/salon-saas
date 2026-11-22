import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { PhoneInput } from '../../components/PhoneInput'
import { toDateInputValue } from '../../lib/datetime'
import {
  EMAIL_INVALID_MESSAGE,
  isEmailErrorMessage,
  isValidEmail,
  normalizeEmail,
} from '../../lib/email'
import { splitPhone, validatePhone } from '../../lib/phone'
import {
  BookingAttempt,
  createPublicBooking,
  describeBookingError,
  fetchAvailability,
  formatSlotRange,
  groupSlotsByTime,
  timeZoneLabel,
  visitorTimeZone,
  type AvailabilitySlot,
  type BookingFailure,
  type PublicBookingCreated,
} from '../../lib/publicBooking'
import { PUBLIC_HOURS_NOTE, SALON, type LandingService } from '../content'

/** `2025-03-14` → `viernes, 14 de marzo de 2025` without shifting the day. */
function longDate(dateValue: string): string {
  const parsed = /^(\d{4})-(\d{2})-(\d{2})$/.exec(dateValue)
  if (!parsed) {
    return dateValue
  }
  const date = new Date(Date.UTC(Number(parsed[1]), Number(parsed[2]) - 1, Number(parsed[3]), 12))
  return new Intl.DateTimeFormat('es-MX', {
    weekday: 'long',
    day: 'numeric',
    month: 'long',
    timeZone: 'UTC',
  }).format(date)
}

type Confirmation = {
  booking: PublicBookingCreated
  serviceName: string
  dateLabel: string
  timeLabel: string
  /** Empty when the visitor booked anonymously. */
  email: string
}

type BookingCtaProps = {
  /** Public slug of the salon, used by the anonymous availability/booking endpoints. */
  slug: string
  /** Catalogue in use: the public API when it answers, the bundled one otherwise. */
  services: readonly LandingService[]
  /** Service preselected when a service card was clicked. */
  preselected: LandingService | null
}

/**
 * Final CTA: real booking against the public API. The visitor picks a service
 * and a day, the free slots come from the salon in UTC and are shown in the
 * visitor's local time, and the request carries a `client_op_id` so a retry is
 * never a double booking. WhatsApp stays as the secondary channel.
 */
export function BookingCta({ slug, services, preselected }: BookingCtaProps) {
  // Minimum date and slot times use the visitor's local calendar (not UTC).
  const timeZone = useMemo(() => visitorTimeZone(), [])
  const today = toDateInputValue(new Date(), timeZone)

  const [serviceId, setServiceId] = useState<string>(services[0]?.id ?? '')
  const [date, setDate] = useState('')
  const [slots, setSlots] = useState<AvailabilitySlot[]>([])
  const [slotsLoading, setSlotsLoading] = useState(false)
  const [slotsError, setSlotsError] = useState<string | null>(null)
  const [slotsKey, setSlotsKey] = useState(0)
  const [selectedStart, setSelectedStart] = useState<string | null>(null)
  const [name, setName] = useState('')
  // `+<code><digits>` (e.g. `+5355528595`); it stays a plain string everywhere.
  const [phone, setPhone] = useState('')
  const [phoneError, setPhoneError] = useState<string | null>(null)
  const [email, setEmail] = useState('')
  const [emailError, setEmailError] = useState<string | null>(null)
  const [submitting, setSubmitting] = useState(false)
  const [failure, setFailure] = useState<BookingFailure | null>(null)
  const [confirmation, setConfirmation] = useState<Confirmation | null>(null)

  const attempt = useRef<BookingAttempt | null>(null)
  const slotsRef = useRef<HTMLDivElement>(null)
  const confirmationRef = useRef<HTMLHeadingElement>(null)
  const shouldFocusSlots = useRef(false)

  useEffect(() => {
    if (preselected && services.some((service) => service.id === preselected.id)) {
      setServiceId(preselected.id)
    }
  }, [preselected, services])

  useEffect(() => {
    setServiceId((current) =>
      services.some((service) => service.id === current) ? current : (services[0]?.id ?? ''),
    )
  }, [services])

  const service = services.find((item) => item.id === serviceId) ?? null

  // Slots of the chosen service and day; a new search resets the selection.
  useEffect(() => {
    if (serviceId === '' || date === '') {
      setSlots([])
      setSlotsError(null)
      return
    }
    const controller = new AbortController()
    let active = true
    setSlotsLoading(true)
    setSlotsError(null)
    setSelectedStart(null)
    fetchAvailability(slug, { date, serviceId }, { signal: controller.signal })
      .then((list) => {
        if (active) {
          setSlots(list)
          shouldFocusSlots.current = true
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setSlots([])
          setSlotsError(
            err instanceof Error && err.message !== ''
              ? err.message
              : 'No pudimos consultar los horarios. Revisa tu conexión e intenta de nuevo.',
          )
        }
      })
      .finally(() => {
        if (active) {
          setSlotsLoading(false)
        }
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [slug, serviceId, date, slotsKey])

  const groups = useMemo(
    () => (date === '' ? [] : groupSlotsByTime(slots, timeZone)),
    [slots, timeZone, date],
  )
  const selectedGroup = groups.find((group) => group.startsAt === selectedStart) ?? null

  // The slot list changes (new search, reload after a 409): move focus to the
  // first free slot so a keyboard user lands on the next step.
  useEffect(() => {
    if (slotsLoading || !shouldFocusSlots.current) {
      return
    }
    shouldFocusSlots.current = false
    const firstSlot = slotsRef.current?.querySelector<HTMLButtonElement>('.slot-btn')
    if (firstSlot) {
      firstSlot.focus()
      return
    }
    document.getElementById('booking-date')?.focus()
  }, [slotsLoading, groups])

  const refreshSlots = useCallback(() => setSlotsKey((key) => key + 1), [])

  const reload = (): void => {
    setConfirmation(null)
    setFailure(null)
    setSelectedStart(null)
    attempt.current = null
    setSlotsKey((key) => key + 1)
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setFailure(null)
    setPhoneError(null)
    setEmailError(null)
    if (service === null) {
      setFailure({ kind: 'validation', message: 'Elige el servicio que quieres reservar.' })
      return
    }
    if (selectedGroup === null) {
      setFailure({ kind: 'validation', message: 'Elige uno de los horarios disponibles.' })
      return
    }
    if (name.trim().length < 2) {
      setFailure({
        kind: 'validation',
        message: 'Escribe tu nombre para que podamos ubicarte en la agenda.',
      })
      return
    }
    const phoneProblem = validatePhone(splitPhone(phone))
    if (phoneProblem !== null) {
      // The message is shown by the phone field itself (role="alert").
      setPhoneError(phoneProblem)
      return
    }
    // The email is optional; a filled one must pass the same rule as the API.
    if (!isValidEmail(email)) {
      setEmailError(EMAIL_INVALID_MESSAGE)
      return
    }
    const input = {
      serviceId: service.id,
      // Without professional_id the salon assigns whoever is free at that time.
      clientName: name,
      clientPhone: phone,
      clientEmail: email,
      startsAt: selectedGroup.startsAt,
    }
    if (attempt.current === null) {
      attempt.current = new BookingAttempt(input)
    } else {
      attempt.current.update(input)
    }
    setSubmitting(true)
    try {
      const booking = await createPublicBooking(slug, attempt.current.payload())
      setConfirmation({
        booking,
        serviceName: service.name,
        dateLabel: longDate(date),
        timeLabel: formatSlotRange(booking.startsAt, booking.endsAt, timeZone),
        email: normalizeEmail(email),
      })
      attempt.current.renew()
      attempt.current = null
    } catch (err: unknown) {
      const next = describeBookingError(err)
      if (next.kind === 'validation' && isEmailErrorMessage(next.message)) {
        // A 400 `{"error":"Email inválido"}` belongs next to the email field.
        setEmailError(next.message)
      } else {
        setFailure(next)
      }
      if (next.kind === 'slot_taken') {
        // Somebody else took it: reload the day and keep what the visitor typed.
        setSelectedStart(null)
        refreshSlots()
      }
    } finally {
      setSubmitting(false)
    }
  }

  useEffect(() => {
    if (confirmation) {
      confirmationRef.current?.focus()
    }
  }, [confirmation])

  const whatsappMessage = confirmation
    ? [
        `Hola ${SALON.name}, reservé una cita en el sitio.`,
        `Nombre: ${name.trim()}`,
        `Teléfono: ${phone.trim()}`,
        `Servicio: ${confirmation.serviceName}`,
        `Fecha: ${confirmation.dateLabel}`,
        `Horario: ${confirmation.timeLabel}`,
      ].join('\n')
    : [
        `Hola ${SALON.name}, quiero reservar una cita.`,
        `Nombre: ${name.trim()}`,
        `Teléfono: ${phone.trim()}`,
        service ? `Servicio: ${service.name}` : '',
        date ? `Fecha preferida: ${date}` : '',
        selectedGroup ? `Horario preferido: ${selectedGroup.time}` : '',
      ]
        .filter(Boolean)
        .join('\n')
  const whatsappHref = `https://wa.me/${SALON.whatsapp}?text=${encodeURIComponent(whatsappMessage)}`

  return (
    <section className="section booking on-dark" id="reservar" aria-labelledby="booking-title">
      <div className="container booking__grid">
        <div className="booking__copy">
          <p className="eyebrow">Reserva</p>
          <h2 id="booking-title">Aparta tu cita en línea</h2>
          <p className="lede">
            Elige servicio, día y horario. La confirmación es inmediata y la especialista queda
            asignada según la disponibilidad del salón en {SALON.district}.
          </p>
          <ul className="booking__contact">
            <li>
              <span className="booking__label">WhatsApp</span>
              <a href={`https://wa.me/${SALON.whatsapp}`} target="_blank" rel="noreferrer">
                {SALON.phone}
              </a>
            </li>
            <li>
              <span className="booking__label">Correo</span>
              <a href={`mailto:${SALON.email}`}>{SALON.email}</a>
            </li>
            <li>
              <span className="booking__label">Salón</span>
              <span>{SALON.address}</span>
            </li>
            <li>
              <span className="booking__label">Horario</span>
              <span>
                {SALON.hours[0]?.days}: {SALON.hours[0]?.time} · {SALON.hours[1]?.days}:{' '}
                {SALON.hours[1]?.time} ({PUBLIC_HOURS_NOTE})
              </span>
            </li>
          </ul>
        </div>

        <div className="booking__form">
          {confirmation ? (
            <div className="booking__confirmation" aria-labelledby="booking-confirmed-title">
              <p className="eyebrow">Cita confirmada</p>
              <h3 id="booking-confirmed-title" tabIndex={-1} ref={confirmationRef}>
                Nos vemos {confirmation.dateLabel}, {confirmation.timeLabel}
              </h3>
              <p className="visually-hidden" role="status">
                Cita confirmada: {confirmation.serviceName}, {confirmation.dateLabel} a las{' '}
                {confirmation.timeLabel}, en tu hora local {timeZoneLabel(timeZone)}.
              </p>
              <dl className="booking__summary">
                <div>
                  <dt>Servicio</dt>
                  <dd>{confirmation.serviceName}</dd>
                </div>
                <div>
                  <dt>Fecha</dt>
                  <dd>{confirmation.dateLabel}</dd>
                </div>
                <div>
                  <dt>Horario</dt>
                  <dd>{confirmation.timeLabel}</dd>
                </div>
                <div>
                  <dt>Zona horaria</dt>
                  <dd>
                    {timeZoneLabel(timeZone)} ({timeZone})
                  </dd>
                </div>
                <div>
                  <dt>Reserva</dt>
                  <dd className="booking__summary-id">{confirmation.booking.id}</dd>
                </div>
              </dl>
              {confirmation.email !== '' ? (
                <p className="booking__note">
                  Te enviamos la confirmación a <strong>{confirmation.email}</strong>. También te
                  escribimos un recordatorio el día antes; si no llega, revisa el correo no
                  deseado.
                </p>
              ) : (
                <p className="booking__note">
                  Reservaste sin correo, así que no podemos enviarte recordatorios. La próxima vez
                  agrega tu correo y te avisamos antes de la cita.
                </p>
              )}
              <p className="booking__note">
                Te esperamos en {SALON.address}. Si necesitas mover la cita, escríbenos por
                WhatsApp con el folio de tu reserva.
              </p>
              <div className="booking__actions">
                <a className="btn btn--wine" href={whatsappHref} target="_blank" rel="noreferrer">
                  Avisar también por WhatsApp
                </a>
                <button type="button" className="btn btn--quiet" onClick={reload}>
                  Reservar otra cita
                </button>
              </div>
            </div>
          ) : (
            <form className="booking__steps" onSubmit={handleSubmit} noValidate>
              <fieldset className="booking__step">
                <legend>
                  <span className="booking__step-number" aria-hidden="true">
                    1
                  </span>{' '}
                  Servicio y fecha
                </legend>
                <div className="booking__fields">
                  <div className="field booking__field--wide">
                    <label htmlFor="booking-service">Servicio</label>
                    <select
                      id="booking-service"
                      className="select"
                      name="service"
                      value={serviceId}
                      onChange={(event) => setServiceId(event.target.value)}
                    >
                      {services.map((option) => (
                        <option key={option.id} value={option.id}>
                          {option.name} · {option.durationMin} min
                        </option>
                      ))}
                    </select>
                  </div>
                  <div className="field booking__field--wide booking__field--date">
                    <label htmlFor="booking-date">Fecha</label>
                    <input
                      id="booking-date"
                      className="input"
                      name="date"
                      type="date"
                      min={today}
                      value={date}
                      onChange={(event) => setDate(event.target.value)}
                      aria-describedby="booking-date-hint"
                    />
                    <p className="field__hint" id="booking-date-hint">
                      Desde hoy ({longDate(today)}) en tu calendario local.
                    </p>
                  </div>
                </div>
              </fieldset>

              <fieldset className="booking__step">
                <legend>
                  <span className="booking__step-number" aria-hidden="true">
                    2
                  </span>{' '}
                  Horario
                </legend>
                <div
                  className="booking__slots"
                  ref={slotsRef}
                  tabIndex={-1}
                  aria-busy={slotsLoading}
                >
                  {date === '' ? (
                    <p className="booking__slots-empty">Elige una fecha para ver los horarios libres.</p>
                  ) : null}
                  {date !== '' ? (
                    <p className="booking__slots-zone">
                      Horarios del {longDate(date)} en tu hora local:{' '}
                      <strong>{timeZoneLabel(timeZone)}</strong> ({timeZone}).
                    </p>
                  ) : null}
                  {slotsLoading ? (
                    <p className="loading" role="status">
                      <span className="loading__dot" aria-hidden="true" />
                      Buscando horarios libres…
                    </p>
                  ) : null}
                  {slotsError ? (
                    <div className="notice notice--error" role="alert">
                      <p>{slotsError}</p>
                      <button type="button" className="btn btn--quiet btn--sm" onClick={refreshSlots}>
                        Reintentar
                      </button>
                    </div>
                  ) : null}
                  {!slotsLoading && !slotsError && date !== '' && groups.length === 0 ? (
                    <p className="booking__slots-empty">
                      No quedan horarios libres ese día. Prueba con otra fecha o escríbenos por
                      WhatsApp.
                    </p>
                  ) : null}
                  {groups.length > 0 ? (
                    <ul className="slot-grid" aria-label="Horarios disponibles">
                      {groups.map((group) => {
                        const selected = group.startsAt === selectedStart
                        return (
                          <li key={group.startsAt}>
                            <button
                              type="button"
                              className="slot-btn"
                              aria-pressed={selected}
                              aria-label={`${group.time}, ${
                                group.slots.length === 1
                                  ? 'una especialista disponible'
                                  : `${group.slots.length} especialistas disponibles`
                              }`}
                              onClick={() => setSelectedStart(group.startsAt)}
                            >
                              {group.time}
                            </button>
                          </li>
                        )
                      })}
                    </ul>
                  ) : null}
                  <p className="visually-hidden" role="status">
                    {slotsLoading
                      ? 'Buscando horarios'
                      : groups.length > 0
                        ? `${groups.length} horarios disponibles, en tu hora local ${timeZoneLabel(timeZone)}`
                        : ''}
                  </p>
                </div>
              </fieldset>

              <fieldset className="booking__step">
                <legend>
                  <span className="booking__step-number" aria-hidden="true">
                    3
                  </span>{' '}
                  Tus datos
                </legend>
                <div className="booking__fields">
                  <div className="field">
                    <label htmlFor="booking-name">Nombre completo</label>
                    <input
                      id="booking-name"
                      className="input"
                      name="name"
                      autoComplete="name"
                      value={name}
                      onChange={(event) => setName(event.target.value)}
                      required
                    />
                  </div>
                  <div className="field">
                    <label htmlFor="booking-email">
                      Correo electrónico (te enviamos la confirmación y un recordatorio)
                    </label>
                    <input
                      id="booking-email"
                      className="input"
                      name="email"
                      type="email"
                      inputMode="email"
                      autoComplete="email"
                      placeholder="tucorreo@ejemplo.com"
                      value={email}
                      onChange={(event) => {
                        setEmail(event.target.value)
                        setEmailError(null)
                      }}
                      aria-describedby="booking-email-hint"
                      aria-invalid={emailError !== null ? true : undefined}
                    />
                    <p className="field__hint" id="booking-email-hint">
                      Opcional. Sin correo la cita se agenda igual, pero no te enviamos
                      recordatorio.
                    </p>
                    {emailError !== null ? (
                      <p className="field__error" role="alert">
                        {emailError}
                      </p>
                    ) : null}
                  </div>
                  <PhoneInput
                    id="booking-phone"
                    className="booking__field--wide"
                    label="Teléfono o WhatsApp"
                    name="phone"
                    value={phone}
                    error={phoneError}
                    onChange={(next) => {
                      setPhone(next)
                      setPhoneError(null)
                    }}
                    required
                  />
                </div>
              </fieldset>

              {failure ? (
                <p className="notice notice--error" role="alert">
                  {failure.message}
                </p>
              ) : null}

              <button
                type="submit"
                className="btn btn--gold booking__submit"
                disabled={submitting || selectedGroup === null}
              >
                {submitting ? 'Confirmando tu cita…' : 'Confirmar cita'}
              </button>
              <p className="booking__fineprint">
                <a href={whatsappHref} target="_blank" rel="noreferrer">
                  ¿Prefieres WhatsApp? También puedes confirmar por mensaje
                </a>
                . No cobramos anticipo para apartar el horario.
              </p>
            </form>
          )}
        </div>
      </div>
    </section>
  )
}
