import { useEffect, useState, type FormEvent } from 'react'
import { toDateInputValue } from '../../lib/datetime'
import { SALON, type LandingService } from '../content'

const TIME_SLOTS = ['10:00', '12:00', '14:00', '16:00', '18:00'] as const

type BookingCtaProps = {
  /** Catalogue in use: the public API when it answers, the bundled one otherwise. */
  services: readonly LandingService[]
  /** Service preselected when a service card was clicked. */
  preselected: LandingService | null
}

/** Final CTA: booking request that hands the message to WhatsApp or email. */
export function BookingCta({ services, preselected }: BookingCtaProps) {
  // Local calendar date of the salon: toISOString() would shift the minimum one
  // day in the evening in Mexico City.
  const today = toDateInputValue(new Date())
  const [name, setName] = useState('')
  const [phone, setPhone] = useState('')
  const [service, setService] = useState<string>(services[0]?.name ?? '')
  const [date, setDate] = useState('')
  const [time, setTime] = useState<string>(TIME_SLOTS[0])
  const [error, setError] = useState<string | null>(null)
  const [sentMessage, setSentMessage] = useState<string | null>(null)

  useEffect(() => {
    if (preselected) {
      setService(preselected.name)
    }
  }, [preselected])

  useEffect(() => {
    setService((current) =>
      services.some((option) => option.name === current)
        ? current
        : (services[0]?.name ?? ''),
    )
  }, [services])

  const buildMessage = (): string =>
    [
      `Hola ${SALON.name}, quiero reservar una cita.`,
      `Nombre: ${name.trim()}`,
      `Teléfono: ${phone.trim()}`,
      `Servicio: ${service}`,
      date ? `Fecha preferida: ${date}` : '',
      `Horario preferido: ${time}`,
    ]
      .filter(Boolean)
      .join('\n')

  const handleSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (name.trim().length < 2) {
      setError('Escribe tu nombre para que podamos ubicarte en la agenda.')
      setSentMessage(null)
      return
    }
    if (phone.replace(/\D/g, '').length < 8) {
      setError('Escribe un teléfono de al menos 8 dígitos.')
      setSentMessage(null)
      return
    }
    setError(null)
    const message = buildMessage()
    window.open(`https://wa.me/${SALON.whatsapp}?text=${encodeURIComponent(message)}`, '_blank')
    setSentMessage(
      `Listo, ${name.trim().split(' ')[0] ?? ''}. Abrimos WhatsApp con tu solicitud para ${service}${
        date ? ` el ${date}` : ''
      } a las ${time}. Si no se abrió, escríbenos a ${SALON.email}.`,
    )
  }

  return (
    <section className="section booking on-dark" id="reservar" aria-labelledby="booking-title">
      <div className="container booking__grid">
        <div className="booking__copy">
          <p className="eyebrow">Reserva</p>
          <h2 id="booking-title">Tu cita empieza con un mensaje</h2>
          <p className="lede">
            Cuéntanos qué necesitas y te confirmamos horario con la especialista disponible. Atendemos
            de lunes a sábado en {SALON.district}.
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
                {SALON.hours[1]?.time}
              </span>
            </li>
          </ul>
        </div>

        <form className="booking__form" onSubmit={handleSubmit} noValidate>
          <h3>Agenda tu cita</h3>
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
              <label htmlFor="booking-phone">Teléfono o WhatsApp</label>
              <input
                id="booking-phone"
                className="input"
                name="phone"
                type="tel"
                inputMode="tel"
                autoComplete="tel"
                placeholder="55 1234 5678"
                value={phone}
                onChange={(event) => setPhone(event.target.value)}
                required
              />
            </div>
            <div className="field booking__field--wide">
              <label htmlFor="booking-service">Servicio</label>
              <select
                id="booking-service"
                className="select"
                name="service"
                value={service}
                onChange={(event) => setService(event.target.value)}
              >
                {services.map((option) => (
                  <option key={option.id} value={option.name}>
                    {option.name}
                  </option>
                ))}
              </select>
            </div>
            <div className="field">
              <label htmlFor="booking-date">Fecha preferida</label>
              <input
                id="booking-date"
                className="input"
                name="date"
                type="date"
                min={today}
                value={date}
                onChange={(event) => setDate(event.target.value)}
              />
            </div>
            <div className="field">
              <label htmlFor="booking-time">Horario preferido</label>
              <select
                id="booking-time"
                className="select"
                name="time"
                value={time}
                onChange={(event) => setTime(event.target.value)}
              >
                {TIME_SLOTS.map((slot) => (
                  <option key={slot} value={slot}>
                    {slot} h
                  </option>
                ))}
              </select>
            </div>
          </div>

          {error ? (
            <p className="notice notice--error" role="alert">
              {error}
            </p>
          ) : null}
          {sentMessage ? (
            <p className="notice notice--success" role="status">
              {sentMessage}
            </p>
          ) : null}

          <button type="submit" className="btn btn--gold booking__submit">
            Enviar solicitud por WhatsApp
          </button>
          <p className="booking__fineprint">
            También puedes llamarnos al{' '}
            <a href={SALON.phoneHref}>{SALON.phone}</a>. No cobramos anticipo para apartar el horario.
          </p>
        </form>
      </div>
    </section>
  )
}
