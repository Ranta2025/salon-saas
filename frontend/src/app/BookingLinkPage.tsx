import { useEffect, useMemo, useRef, useState } from 'react'
import { ApiError } from '../lib/api'
import {
  BOOKING_LINK_MESSAGES,
  CANCEL_ACTION,
  CANCEL_CONFIRM_NOTE,
  CANCEL_DONE,
  CHANGE_ACTION,
  INVALID_LINK_HELP,
  RESCHEDULE_DONE,
  bookingLinkErrorMessage,
  canCancel,
  canReschedule,
  cancelBookingLink,
  cancelConfirmQuestion,
  cutOffNotice,
  defaultPickDate,
  fetchLinkSlots,
  formatLinkDay,
  formatLinkWhen,
  formatLinkZone,
  initialLinkChange,
  isDateValue,
  lookupBookingLink,
  newLinkNotice,
  reduceLinkChange,
  rescheduleBookingLink,
  rescheduleConfirmQuestion,
  takeBookingLinkToken,
  type BookingLinkLookup,
} from '../lib/bookingLinks'
import { groupSlotsByTime, type AvailabilitySlot } from '../lib/publicBooking'
import { LoadingState } from './components/Feedback'

/** Copy of a failure nobody could explain. */
const LOOKUP_FALLBACK = 'No pudimos abrir el turno. Revisá tu conexión e intentá de nuevo.'
const SLOTS_FALLBACK = 'No pudimos consultar los horarios. Revisá tu conexión e intentá de nuevo.'
const CANCEL_FALLBACK = 'No pudimos cancelar el turno. Intentá de nuevo.'
const RESCHEDULE_FALLBACK = 'No pudimos cambiar el horario. Intentá de nuevo.'

/**
 * Public `/turno` page: cancel or move an appointment from the link of the
 * booking emails (contract F1, S-F1 «Frontend»). The token comes in the
 * FRAGMENT of the link (`/turno#<token>`) so it never reaches a server or proxy
 * log:
 *
 * 1. it is read from `location.hash` in the first render and wiped from the
 *    address bar with `history.replaceState` in the same tick, BEFORE any call;
 * 2. it lives only in a ref of this component: never localStorage, never
 *    sessionStorage, never a cookie, never the console, never an error message,
 *    never the query string and never the route path;
 * 3. it travels only in request bodies (`{token}`, `{token, date}`,
 *    `{token, starts_at}`);
 * 4. a dead link (used, expired, appointment cancelled or done, or less than two
 *    hours before the start) gets the ONE generic sentence: this page never says
 *    which cause it was, and it offers no action.
 *
 * Nothing changes optimistically: the appointment only moves on screen when the
 * server confirms it, and both successes are terminal because the token was
 * consumed (the new link only arrives by email).
 */
export function BookingLinkPage() {
  // Read + clean before anything else runs. The ref survives React's double
  // render in development, so the token is never lost on the second pass.
  const tokenRef = useRef<{ token: string | null } | null>(null)
  if (tokenRef.current === null) {
    tokenRef.current = { token: takeBookingLinkToken(window.location.href, window.history) }
  }
  const token = tokenRef.current.token

  const [now] = useState(() => new Date())
  const [lookup, setLookup] = useState<BookingLinkLookup | null>(null)
  const [loadError, setLoadError] = useState<string | null>(null)
  const [loading, setLoading] = useState(token !== null)
  const [attempt, setAttempt] = useState(0)
  const [change, setChange] = useState(initialLinkChange)
  const [slots, setSlots] = useState<AvailabilitySlot[]>([])
  const [slotsLoading, setSlotsLoading] = useState(false)
  const [slotsKey, setSlotsKey] = useState(0)
  const [selectedStart, setSelectedStart] = useState<string | null>(null)
  const [pending, setPending] = useState<'cancel' | 'reschedule' | null>(null)
  // Synchronous guard: two clicks in the same tick must not send two requests,
  // even before React repaints the disabled button.
  const pendingRef = useRef<'cancel' | 'reschedule' | null>(null)

  const slotsRef = useRef<HTMLDivElement>(null)
  const resultRef = useRef<HTMLHeadingElement>(null)
  const shouldFocusSlots = useRef(false)

  const zone = lookup ? lookup.booking.timezone : null
  const booking = lookup ? lookup.booking : null
  const busy = pending !== null

  useEffect(() => {
    if (token === null) {
      return
    }
    let active = true
    setLoading(true)
    setLoadError(null)
    lookupBookingLink(token)
      .then((result) => {
        if (!active) {
          return
        }
        setLookup(result)
        // The picker opens on the day of the appointment: that is the day the
        // person knows, and cancel/move never sends a past date.
        setChange((state) => ({
          ...state,
          date: defaultPickDate(new Date(result.booking.starts_at), result.booking.timezone),
        }))
      })
      .catch((err: unknown) => {
        if (active) {
          setLoadError(bookingLinkErrorMessage(err, LOOKUP_FALLBACK))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [token, attempt])

  // Slots of ONE local day of the salon, for the same service and professional.
  useEffect(() => {
    if (token === null || zone === null || change.step !== 'slots' || !isDateValue(change.date)) {
      setSlots([])
      return
    }
    const controller = new AbortController()
    let active = true
    setSlotsLoading(true)
    setSelectedStart(null)
    fetchLinkSlots(token, change.date, { signal: controller.signal })
      .then((list) => {
        if (active) {
          setSlots(list)
          shouldFocusSlots.current = true
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setSlots([])
          setChange((state) =>
            reduceLinkChange(state, {
              type: 'failed',
              message: bookingLinkErrorMessage(err, SLOTS_FALLBACK),
            }),
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
  }, [token, zone, change.step, change.date, slotsKey])

  const groups = useMemo(
    () => (zone === null ? [] : groupSlotsByTime(slots, zone)),
    [slots, zone],
  )
  const selectedGroup = groups.find((group) => group.startsAt === selectedStart) ?? null

  // A new list of slots (another day, a reload after a 409) lands the keyboard
  // on the first free time instead of leaving the focus behind.
  useEffect(() => {
    if (slotsLoading || !shouldFocusSlots.current) {
      return
    }
    shouldFocusSlots.current = false
    slotsRef.current?.querySelector<HTMLButtonElement>('.slot-btn')?.focus()
  }, [slotsLoading, groups])

  // The confirmation of a change (or the cancel) is announced and focused: the
  // person must read the result, not discover it by scrolling.
  useEffect(() => {
    if (change.step === 'done_cancel' || change.step === 'done_rescheduled') {
      resultRef.current?.focus()
    }
  }, [change.step])

  const openSlots = (): void => {
    setSelectedStart(null)
    setChange((state) => reduceLinkChange(state, { type: 'open_slots' }))
  }

  const openCancel = (): void => {
    setChange((state) => reduceLinkChange(state, { type: 'open_cancel' }))
  }

  const goBack = (): void => {
    setSelectedStart(null)
    setChange((state) => reduceLinkChange(state, { type: 'back' }))
  }

  const pickDate = (date: string): void => {
    setSelectedStart(null)
    setChange((state) => reduceLinkChange(state, { type: 'date', date }))
  }

  /** Cancel: the server confirms it, and only then the screen says so. */
  const handleCancel = async (): Promise<void> => {
    if (
      token === null ||
      pendingRef.current !== null ||
      !canCancel({ canChange: booking?.can_change === true, busy })
    ) {
      return
    }
    pendingRef.current = 'cancel'
    setPending('cancel')
    try {
      await cancelBookingLink(token)
      setChange((state) => reduceLinkChange(state, { type: 'cancelled' }))
    } catch (err: unknown) {
      setChange((state) =>
        reduceLinkChange(state, {
          type: 'failed',
          message: bookingLinkErrorMessage(err, CANCEL_FALLBACK),
        }),
      )
    } finally {
      pendingRef.current = null
      setPending(null)
    }
  }

  /** «Confirmar cambio»: a 409 leaves the token alive, so another slot can be chosen. */
  const handleReschedule = async (): Promise<void> => {
    if (
      token === null ||
      selectedGroup === null ||
      pendingRef.current !== null ||
      !canReschedule({
        canChange: booking?.can_change === true,
        busy,
        hasSlot: selectedGroup !== null,
      })
    ) {
      return
    }
    pendingRef.current = 'reschedule'
    setPending('reschedule')
    try {
      const result = await rescheduleBookingLink(token, selectedGroup.startsAt)
      setChange((state) =>
        reduceLinkChange(state, {
          type: 'rescheduled',
          booking: result.booking,
          newLinkSent: result.new_link_sent,
        }),
      )
    } catch (err: unknown) {
      setChange((state) =>
        reduceLinkChange(state, {
          type: 'failed',
          message: bookingLinkErrorMessage(err, RESCHEDULE_FALLBACK),
        }),
      )
      // A 409 (taken slot or the two-hour window) does not consume the token:
      // reload the day and let the person choose another time.
      if (err instanceof ApiError && err.status === 409) {
        setSelectedStart(null)
        setSlotsKey((key) => key + 1)
      }
    } finally {
      pendingRef.current = null
      setPending(null)
    }
  }

  const deadLink = token === null || loadError === BOOKING_LINK_MESSAGES.invalid
  const terminal = change.step === 'done_cancel' || change.step === 'done_rescheduled'
  /** Narrowed alias so the JSX can read the appointment without `!`. */
  const view = booking

  let title = 'Tu turno'
  if (deadLink) {
    title = BOOKING_LINK_MESSAGES.invalid
  } else if (loading) {
    title = 'Revisando tu turno'
  } else if (booking) {
    title = `Tu turno en ${booking.salon_name}`
  } else if (loadError) {
    title = 'No pudimos abrir el turno'
  }

  const cancelGuard = canCancel({ canChange: booking?.can_change === true, busy })
  const rescheduleGuard = canReschedule({
    canChange: booking?.can_change === true,
    busy,
    hasSlot: selectedGroup !== null,
  })

  return (
    <main className="invite">
      <section className="invite__card" aria-labelledby="link-title">
        <p className="eyebrow">Tu turno</p>
        <h1 id="link-title">{title}</h1>

        {deadLink ? (
          <>
            <p>
              El enlace de este turno ya no sirve: se usa una sola vez y deja de valer cuando el
              turno se cancela, se atiende o falta menos de dos horas para empezar.
            </p>
            <p>{INVALID_LINK_HELP}</p>
            <p className="invite__actions">
              <a className="btn btn--quiet" href="/">
                Ir al sitio del salón
              </a>
            </p>
          </>
        ) : null}

        {!deadLink && loading ? <LoadingState label="Revisando el turno…" /> : null}

        {!deadLink && !loading && loadError && !terminal ? (
          <>
            <p className="notice notice--error" role="alert">
              {loadError}
            </p>
            <p className="invite__actions">
              <button
                type="button"
                className="btn btn--wine"
                onClick={() => setAttempt((value) => value + 1)}
              >
                Reintentar
              </button>
            </p>
          </>
        ) : null}

        {!deadLink && !loading && view !== null ? (
          <>
            <dl className="booking__summary">
              <div>
                <dt>Salón</dt>
                <dd>{view.salon_name}</dd>
              </div>
              {view.service_name !== '' ? (
                <div>
                  <dt>Servicio</dt>
                  <dd>{view.service_name}</dd>
                </div>
              ) : null}
              {view.professional_name !== '' ? (
                <div>
                  <dt>Profesional</dt>
                  <dd>{view.professional_name}</dd>
                </div>
              ) : null}
              <div>
                <dt>Fecha y hora</dt>
                <dd>{formatLinkWhen(view, view.timezone)}</dd>
              </div>
              <div>
                <dt>Zona horaria</dt>
                <dd>{formatLinkZone(view.timezone)}</dd>
              </div>
            </dl>

            {change.error ? (
              <p className="notice notice--error" role="alert">
                {change.error}
              </p>
            ) : null}

            {change.step === 'done_cancel' ? (
              <section aria-labelledby="link-cancel-done">
                <h2 id="link-cancel-done" tabIndex={-1} ref={resultRef}>
                  {CANCEL_DONE}
                </h2>
                <p className="notice notice--success" role="status">
                  El horario vuelve a quedar libre en el salón. Si querés reservar otro día, entrá
                  al sitio del salón.
                </p>
                <p className="invite__actions">
                  <a className="btn btn--quiet" href="/">
                    Ir al sitio del salón
                  </a>
                </p>
              </section>
            ) : null}

            {change.step === 'done_rescheduled' && change.booking ? (
              <section aria-labelledby="link-move-done">
                <h2 id="link-move-done" tabIndex={-1} ref={resultRef}>
                  {RESCHEDULE_DONE}
                </h2>
                <p className="invite__meta">
                  <strong>{formatLinkWhen(change.booking, change.booking.timezone)}</strong>
                  <span>{formatLinkZone(change.booking.timezone)}</span>
                </p>
                <p className="notice notice--info" role="status">
                  {newLinkNotice(change.newLinkSent)}
                </p>
                <p className="invite__actions">
                  <a className="btn btn--quiet" href="/">
                    Ir al sitio del salón
                  </a>
                </p>
              </section>
            ) : null}

            {change.step === 'view' && !terminal ? (
              view.can_change ? (
                <>
                  <p>{cutOffNotice(view.min_notice_hours)}</p>
                  <div className="invite__actions">
                    <button type="button" className="btn btn--wine" onClick={openSlots}>
                      {CHANGE_ACTION}
                    </button>
                    <button
                      type="button"
                      className="btn btn--quiet"
                      disabled={!cancelGuard}
                      onClick={openCancel}
                    >
                      {CANCEL_ACTION}
                    </button>
                  </div>
                </>
              ) : (
                <p className="notice notice--info" role="status">
                  {BOOKING_LINK_MESSAGES.tooLate}
                </p>
              )
            ) : null}

            {change.step === 'confirm_cancel' ? (
              <section aria-labelledby="link-cancel-title">
                <h2 id="link-cancel-title">
                  {cancelConfirmQuestion(formatLinkWhen(view, view.timezone))}
                </h2>
                <p>{CANCEL_CONFIRM_NOTE}</p>
                <div className="invite__actions">
                  <button
                    type="button"
                    className="btn btn--wine"
                    disabled={!cancelGuard}
                    onClick={() => void handleCancel()}
                  >
                    {pending === 'cancel' ? 'Cancelando…' : 'Sí, cancelar el turno'}
                  </button>
                  <button type="button" className="btn btn--quiet" onClick={goBack}>
                    Volver
                  </button>
                </div>
              </section>
            ) : null}

            {change.step === 'slots' ? (
              <section aria-labelledby="link-slots-title">
                <h2 id="link-slots-title">{CHANGE_ACTION}</h2>
                <div className="field booking__field--date">
                  <label htmlFor="link-date">Día del turno nuevo</label>
                  <input
                    id="link-date"
                    className="input"
                    type="date"
                    value={change.date}
                    min={defaultPickDate(now, view.timezone)}
                    onChange={(event) => pickDate(event.target.value)}
                  />
                  <p className="field__hint">
                    Elegí un día por vez. Los horarios se muestran en la zona del salón.
                  </p>
                </div>

                <div className="booking__slots" ref={slotsRef}>
                  <p className="booking__slots-zone">
                    Zona del salón: <strong>{formatLinkZone(view.timezone)}</strong>
                  </p>
                  {slotsLoading ? <LoadingState label="Buscando horarios…" /> : null}
                  {!slotsLoading && groups.length === 0 ? (
                    <p className="booking__slots-empty">
                      No hay horarios libres ese día con esta profesional. Probá con otro día.
                    </p>
                  ) : null}
                  {!slotsLoading && groups.length > 0 ? (
                    <div className="slot-grid" role="group" aria-label="Horarios disponibles">
                      {groups.map((group) => (
                        <button
                          key={group.startsAt}
                          type="button"
                          className="slot-btn"
                          aria-pressed={group.startsAt === selectedStart}
                          onClick={() => setSelectedStart(group.startsAt)}
                        >
                          {group.time}
                        </button>
                      ))}
                    </div>
                  ) : null}
                </div>

                {selectedGroup ? (
                  <p className="notice notice--info" role="status">
                    {rescheduleConfirmQuestion(
                      `${formatLinkDay(selectedGroup.startsAt, view.timezone)}, ${selectedGroup.time}`,
                    )}
                  </p>
                ) : null}

                <div className="invite__actions">
                  <button
                    type="button"
                    className="btn btn--wine"
                    disabled={!rescheduleGuard}
                    onClick={() => void handleReschedule()}
                  >
                    {pending === 'reschedule' ? 'Cambiando…' : 'Confirmar cambio'}
                  </button>
                  <button type="button" className="btn btn--quiet" onClick={goBack}>
                    Volver
                  </button>
                </div>
              </section>
            ) : null}
          </>
        ) : null}
      </section>
    </main>
  )
}
