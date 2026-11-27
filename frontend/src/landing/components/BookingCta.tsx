import { useCallback, useEffect, useMemo, useRef, useState, type FormEvent } from 'react'
import { useNavigate } from 'react-router-dom'
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
} from '@/features/bookings/lib/publicBooking'
import {
  depositPaymentSummary,
  paymentTarget,
  type BookingPayment,
} from '@/features/bookings/lib/deposits'
import { whatsappUrl, type LandingModel, type LandingService } from '../../lib/publicSalon'
import { weekSummary } from '@/features/team/lib/weeklyHours'
import { PUBLIC_HOURS_FALLBACK } from '../content'
import { WaitlistForm } from './WaitlistForm'

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

/**
 * Deposit block of the confirmation screen (F11, D8, D13-b): amount, countdown of the
 * hold computed from `expires_at`, the notice of the simulated payment and the button
 * that opens the bank. It renders NOTHING when the 201 carried no `payment`, so the
 * screen of a salon without deposits is exactly the one of before.
 */
function PaymentStep({ payment }: { payment: BookingPayment }) {
  const navigate = useNavigate()
  // The countdown ticks while the hold lasts; once it is over the paragraph says so.
  const [now, setNow] = useState(() => new Date())
  const summary = depositPaymentSummary(payment, now)

  useEffect(() => {
    if (summary.expired) {
      return
    }
    const timer = window.setInterval(() => setNow(new Date()), 1_000)
    return () => window.clearInterval(timer)
  }, [summary.expired])

  // The server builds an absolute `pay_url`: same origin becomes a path plus fragment
  // (the SPA navigates and the charge id stays in the fragment), another origin is a
  // plain external link. Nothing usable means no button.
  const target = paymentTarget(payment.payUrl, window.location.origin)

  return (
    <div className="booking__deposit" aria-labelledby="booking-deposit-title">
      <h4 id="booking-deposit-title">Seña para confirmar el turno</h4>
      <dl className="booking__summary">
        <div>
          <dt>Seña</dt>
          <dd>{summary.amountLabel}</dd>
        </div>
        <div>
          <dt>Tenés tiempo hasta</dt>
          <dd>
            {summary.countdown === null ? (
              'el plazo que indica el salón'
            ) : summary.expired ? (
              'el plazo venció'
            ) : (
              <strong aria-live="polite">{summary.countdown}</strong>
            )}
          </dd>
        </div>
      </dl>
      {summary.notice !== null ? <p className="booking__deposit-notice">{summary.notice}</p> : null}
      <p className="booking__note">
        Si la seña no se paga dentro del plazo, el turno se cancela solo y el horario vuelve a
        quedar libre.
      </p>
      {target === null ? (
        <p className="booking__note">
          El salón no nos dio un enlace de pago válido. Escríbenos y lo resolvemos.
        </p>
      ) : target.kind === 'path' ? (
        <button type="button" className="btn btn--gold" onClick={() => navigate(target.path)}>
          Pagar seña
        </button>
      ) : (
        <a className="btn btn--gold" href={target.href} target="_blank" rel="noreferrer">
          Pagar seña
        </a>
      )}
    </div>
  )
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
  /** Services of the salon, as the public API sent them (never a bundled catalogue). */
  services: readonly LandingService[]
  /** Service preselected when a service card was clicked. */
  preselected: LandingService | null
  /**
   * The salon as the landing renders it: name, contact and `hours` (F2d1). The
   * contact rows only show what the salon filled in; without hours the block
   * asks to confirm on WhatsApp when the salon has a phone.
   */
  model: LandingModel
}

/**
 * Final CTA: real booking against the public API. The visitor picks a service
 * and a day, the free slots come from the salon in UTC and are shown in the
 * visitor's local time, and the request carries a `client_op_id` so a retry is
 * never a double booking. WhatsApp stays as the secondary channel.
 */
export function BookingCta({ slug, services, preselected, model }: BookingCtaProps) {
  const { hours } = model
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
        `Hola ${model.name}, reservé una cita en el sitio.`,
        `Nombre: ${name.trim()}`,
        `Teléfono: ${phone.trim()}`,
        `Servicio: ${confirmation.serviceName}`,
        `Fecha: ${confirmation.dateLabel}`,
        `Horario: ${confirmation.timeLabel}`,
      ].join('\n')
    : [
        `Hola ${model.name}, quiero reservar una cita.`,
        `Nombre: ${name.trim()}`,
        `Teléfono: ${phone.trim()}`,
        service ? `Servicio: ${service.name}` : '',
        date ? `Fecha preferida: ${date}` : '',
        selectedGroup ? `Horario preferido: ${selectedGroup.time}` : '',
      ]
        .filter(Boolean)
        .join('\n')
  // Only a salon with a usable phone has a WhatsApp channel.
  const whatsappHref = whatsappUrl(model.phone?.display, whatsappMessage)
  const whatsappBase = model.phone?.whatsapp ?? null

  return (
    <section className="section booking on-dark" id="reservar" aria-labelledby="booking-title">
      <div className="container booking__grid">
        <div className="booking__copy">
          <p className="eyebrow">Reserva</p>
          <h2 id="booking-title">Aparta tu cita en línea</h2>
          <p className="lede">
            Elige servicio, día y horario. La confirmación es inmediata y la especialista queda
            asignada según la disponibilidad del salón.
          </p>
          <ul className="booking__contact">
            {model.phone !== null ? (
              <li>
                <span className="booking__label">{whatsappBase !== null ? 'WhatsApp' : 'Teléfono'}</span>
                {whatsappBase !== null ? (
                  <a href={whatsappBase} target="_blank" rel="noreferrer">
                    {model.phone.display}
                  </a>
                ) : (
                  <span>{model.phone.display}</span>
                )}
              </li>
            ) : null}
            {model.email !== null ? (
              <li>
                <span className="booking__label">Correo</span>
                <a href={`mailto:${model.email}`}>{model.email}</a>
              </li>
            ) : null}
            {model.address !== null ? (
              <li>
                <span className="booking__label">Salón</span>
                <span>{model.address}</span>
              </li>
            ) : null}
            {model.instagram !== null ? (
              <li>
                <span className="booking__label">Instagram</span>
                <a href={model.instagram.url} target="_blank" rel="noreferrer">
                  @{model.instagram.handle}
                </a>
              </li>
            ) : null}
            {hours !== null ? (
              <li>
                <span className="booking__label">Horario</span>
                <span>{weekSummary(hours)}</span>
              </li>
            ) : whatsappBase !== null ? (
              <li>
                <span className="booking__label">Horario</span>
                <span>
                  {PUBLIC_HOURS_FALLBACK}:{' '}
                  <a href={whatsappBase} target="_blank" rel="noreferrer">
                    {model.phone?.display}
                  </a>
                </span>
              </li>
            ) : null}
          </ul>
        </div>

        <div className="booking__form">
          {!model.canBook ? (
            <p className="booking__note" role="status">
              Por ahora este salón no tiene servicios disponibles para reservar en línea.
            </p>
          ) : confirmation ? (
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
              {/* F11 (D13-b): only when the 201 of the booking carried `payment`. */}
              {confirmation.booking.payment ? (
                <PaymentStep payment={confirmation.booking.payment} />
              ) : null}
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
                {model.address !== null ? `Te esperamos en ${model.address}. ` : ''}
                {whatsappHref !== null
                  ? 'Si necesitas mover la cita, escríbenos por WhatsApp con el folio de tu reserva.'
                  : 'Guarda el folio de tu reserva por si necesitas mover la cita.'}
              </p>
              <div className="booking__actions">
                {whatsappHref !== null ? (
                  <a className="btn btn--wine" href={whatsappHref} target="_blank" rel="noreferrer">
                    Avisar también por WhatsApp
                  </a>
                ) : null}
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
                    <>
                      <p className="booking__slots-empty">
                        No quedan horarios libres ese día. Prueba con otra fecha
                        {whatsappBase !== null ? ' o escríbenos por WhatsApp' : ''}.
                      </p>
                      {/*
                       * F7 (D9a): only when the day has no slots at all. The form
                       * reuses the service and the day already chosen and never
                       * reserves anything: the aviso is by email when a slot frees up.
                       */}
                      {service !== null ? (
                        <WaitlistForm
                          key={`${service.id}-${date}`}
                          slug={slug}
                          serviceId={service.id}
                          serviceName={service.name}
                          date={date}
                        />
                      ) : null}
                    </>
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
              {whatsappHref !== null ? (
                <p className="booking__fineprint">
                  <a href={whatsappHref} target="_blank" rel="noreferrer">
                    ¿Prefieres WhatsApp? También puedes confirmar por mensaje
                  </a>
                </p>
              ) : null}
            </form>
          )}
        </div>
      </div>
    </section>
  )
}
