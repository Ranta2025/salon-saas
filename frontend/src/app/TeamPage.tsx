import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError, fetchServices, friendlyApiMessage, type ApiErrorNotice } from '../lib/api'
import type { SalonRole } from '../lib/me'
import type { Service } from '../types'
import {
  addMember,
  deactivateMember,
  fetchMembers,
  normalizeMemberEmail,
  reactivateMember,
  setMemberAttends,
  setMemberServices,
  setMemberTrade,
  type SalonMember,
} from '../lib/members'
import {
  DEACTIVATE_CONFIRM_NOTE,
  DEACTIVATE_SUCCESS,
  REACTIVATE_CONFIRM_NOTE,
  activeMembers,
  applyMemberEndedAt,
  deactivateConfirmQuestion,
  endedOnLabel,
  formerMembers,
  formerTeamLabel,
  reactivateConfirmQuestion,
  reactivatedSuccess,
  restoreMember,
} from '../lib/teamStatus'
import {
  applyAttends,
  ATTENDS_NOTICE,
  attendsControl,
  memberAttends,
  nobodyAttends,
} from '../lib/attends'
import {
  applyMemberServices,
  bookableMembers,
  NO_SERVICES_HINT,
  normalizeTrade,
  TRADE_MAX_LENGTH,
  TRADE_PLACEHOLDER,
} from '../lib/tradeServices'
import {
  activeRole,
  assignableRoles,
  canAssignRole,
  isManager,
  roleLabel,
} from '../lib/permissions'
import { fetchSalonHours, fetchSettings } from '../lib/settings'
import { TIME_ZONE, addDaysToDateValue, fromDateInputValue, toDateInputValue } from '../lib/datetime'
import { fetchTimeOff, type TimeOff } from '../lib/timeOff'
import { fetchCoverAbsences, type CoverAbsence } from '../lib/coverAbsence'
import type { SalonDay } from '../lib/weeklyHours'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { MemberHoursCard } from './components/MemberHoursCard'
import { PageHeader } from './components/PageHeader'
import { TeamInvitations } from './components/TeamInvitations'
import { TimeOffCard } from './components/TimeOffCard'

/** `/panel/equipo`: team of the salon (owner/admin). */
export function TeamPage() {
  const { salonId, salons, user } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = isManager(role)

  const [members, setMembers] = useState<SalonMember[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [email, setEmail] = useState('')
  const [newRole, setNewRole] = useState<SalonRole>('professional')
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [confirmRemoveId, setConfirmRemoveId] = useState<string | null>(null)
  const [removingId, setRemovingId] = useState<string | null>(null)
  // Verbatim sentence of a refused leave (the 409 of pending turns) or of a
  // refused reactivation, shown next to the row that asked for it (F2g2).
  const [leaveError, setLeaveError] = useState<{ id: string; message: string } | null>(null)
  const [confirmReactivateId, setConfirmReactivateId] = useState<string | null>(null)
  const [reactivatingId, setReactivatingId] = useState<string | null>(null)
  const [exTeamOpen, setExTeamOpen] = useState(false)
  const [pendingAttendsId, setPendingAttendsId] = useState<string | null>(null)
  const [attendsError, setAttendsError] = useState<string | null>(null)
  const [workServices, setWorkServices] = useState<Service[]>([])
  const [workServicesError, setWorkServicesError] = useState<string | null>(null)
  // Weekly hours of the salon (F2d1): the default of every professional card.
  const [salonDays, setSalonDays] = useState<readonly SalonDay[]>([])
  const [salonHoursError, setSalonHoursError] = useState<string | null>(null)
  // Absences of the team (F2d2): one list for every card, read for the window
  // the API allows and converted with the zone of the salon.
  const [timeZone, setTimeZone] = useState<string>(TIME_ZONE)
  const [absences, setAbsences] = useState<TimeOff[]>([])
  const [absencesError, setAbsencesError] = useState<string | null>(null)
  const [absencesLoaded, setAbsencesLoaded] = useState(false)
  // Turns each absence leaves without a professional (F2d3): the "Cubrir" panel
  // of every card reads them from here, one request for the whole team.
  const [coverAbsences, setCoverAbsences] = useState<CoverAbsence[]>([])
  const [coverReady, setCoverReady] = useState(false)
  const [now] = useState(() => new Date())
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchMembers(salonId)
      .then((list) => {
        if (active) {
          setMembers(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar el equipo del salón.'))
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
  }, [salonId, allowed, reloadKey])

  const options = assignableRoles(role)
  // Who is still in the team and who left (F2g2). The list keeps both: only the
  // active ones are offered in the pickers and in the table of the page.
  const team = activeMembers(members)
  const exTeam = formerMembers(members)
  // The column only exists when someone can attend or be marked as attending.
  const showAttends = team.some((member) => attendsControl(role, member.role) !== 'none')
  // Only who attends clients can have a trade and services (F2c).
  const bookable = bookableMembers(members)

  // Active services are the choices of the "Servicios que hace" control.
  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setWorkServicesError(null)
    fetchServices(salonId, false)
      .then((list) => {
        if (active) {
          setWorkServices(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setWorkServicesError(describeApiError(err, 'No pudimos cargar los servicios.').message)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setSalonHoursError(null)
    fetchSalonHours(salonId)
      .then((hours) => {
        if (active) {
          setSalonDays(hours.days)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setSalonHoursError(
            describeApiError(err, 'No pudimos cargar el horario del salón.').message,
          )
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed])

  // The zone of the salon converts the local dates of an absence to UTC.
  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback
      })
    return () => {
      active = false
    }
  }, [salonId, allowed])

  // Absences to come and in progress: today plus the 91 days the API allows.
  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setAbsencesError(null)
    setAbsencesLoaded(false)
    const today = toDateInputValue(now, timeZone)
    fetchTimeOff(
      salonId,
      fromDateInputValue(today, timeZone),
      fromDateInputValue(addDaysToDateValue(today, 91), timeZone),
    )
      .then((list) => {
        if (active) {
          setAbsences(list)
          setAbsencesLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setAbsencesError(
            describeApiError(err, 'No pudimos cargar las ausencias del equipo.').message,
          )
        }
      })
    // The turns of those absences are extra information: without them the cards
    // list the absences and explain that there is nothing to cover, never an
    // invented turn. This endpoint is management-only, like the page itself.
    fetchCoverAbsences(
      salonId,
      fromDateInputValue(today, timeZone),
      fromDateInputValue(addDaysToDateValue(today, 91), timeZone),
    )
      .then((list) => {
        if (active) {
          setCoverAbsences(list)
          setCoverReady(true)
        }
      })
      .catch(() => {
        if (active) {
          setCoverAbsences([])
          setCoverReady(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed, timeZone, now, reloadKey])

  useEffect(() => {
    if (options.length > 0 && !options.includes(newRole)) {
      setNewRole(options[0] ?? 'professional')
    }
  }, [options, newRole])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Equipo" title="Equipo del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Equipo" title="Equipo del salón" />
        <div className="empty-state">
          <h2>Esta sección es de la administración</h2>
          <p>
            Tu rol ({roleLabel(role)}) no puede ver ni cambiar el equipo del salón. Si necesitas
            acceso, pídeselo a la dueña o a la administración.
          </p>
        </div>
      </>
    )
  }

  const handleAdd = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setFormError(null)
    setNotice(null)
    const normalized = normalizeMemberEmail(email)
    if (!normalized.includes('@') || normalized.length < 5) {
      setFormError('Escribe el correo con el que la persona inició sesión.')
      return
    }
    setSaving(true)
    try {
      const member = await addMember(salonId, { email: normalized, role: newRole })
      setNotice(`${member.name} ya forma parte del equipo como ${roleLabel(member.role)}.`)
      setEmail('')
      reload()
    } catch (err: unknown) {
      setFormError(friendlyApiMessage(err, 'No pudimos agregar a esa persona al equipo.'))
    } finally {
      setSaving(false)
    }
  }

  /**
   * Switch of "Atiende clientes" (S-F2a-6): the row flips before the answer and
   * comes back to its previous value if the server rejects the change, showing
   * the server sentence verbatim («Las profesionales siempre atienden»).
   */
  const handleAttendsToggle = async (member: SalonMember): Promise<void> => {
    if (pendingAttendsId !== null) {
      return
    }
    const next = !memberAttends(member)
    setAttendsError(null)
    setNotice(null)
    setPendingAttendsId(member.id)
    setMembers((list) => applyAttends(list, member.id, next))
    try {
      const saved = await setMemberAttends(salonId, member.id, next)
      setMembers((list) => applyAttends(list, member.id, saved.attends))
      setNotice(
        saved.attends
          ? `${saved.name} atiende clientes desde ahora.`
          : `${saved.name} ya no atiende clientes.`,
      )
    } catch (err: unknown) {
      setMembers((list) => applyAttends(list, member.id, member.attends))
      setAttendsError(describeApiError(err, 'No pudimos cambiar quién atiende clientes.').message)
    } finally {
      setPendingAttendsId(null)
    }
  }

  /**
   * Trade save (S-F2c-6): PATCH with the trimmed text; the server sentence is
   * shown verbatim when it rejects («Ese rol no tiene oficio»).
   */
  const handleSaveTrade = async (member: SalonMember, trade: string): Promise<SalonMember> => {
    const saved = await setMemberTrade(salonId, member.id, trade)
    setMembers((list) => list.map((row) => (row.id === saved.id ? saved : row)))
    return saved
  }

  /**
   * Services save (S-F2c-6): the list flips before the answer and comes back to
   * the previous ids if the server rejects the change.
   */
  const handleSaveServices = async (
    member: SalonMember,
    serviceIds: string[],
  ): Promise<string[]> => {
    const previous = member.service_ids
    setMembers((list) => applyMemberServices(list, member.id, serviceIds))
    try {
      const saved = await setMemberServices(salonId, member.id, serviceIds)
      setMembers((list) => applyMemberServices(list, member.id, saved))
      return saved
    } catch (err: unknown) {
      setMembers((list) => applyMemberServices(list, member.id, previous))
      throw err
    }
  }

  /**
   * Give somebody de baja (S-F2g2-5): the row leaves the table before the answer
   * and comes back untouched if the server refuses (the 409 of pending turns is
   * shown verbatim). The notice repeats what is kept, as the contract says.
   */
  const handleDeactivate = async (member: SalonMember): Promise<void> => {
    setNotice(null)
    setLeaveError(null)
    setRemovingId(member.id)
    setMembers((list) => applyMemberEndedAt(list, member.id, new Date().toISOString()))
    try {
      await deactivateMember(salonId, member.id)
      setNotice(DEACTIVATE_SUCCESS)
      setConfirmRemoveId(null)
      reload()
    } catch (err: unknown) {
      setMembers((list) => restoreMember(list, member))
      setLeaveError({
        id: member.id,
        message: describeApiError(err, 'No pudimos dar de baja a esa persona.').message,
      })
    } finally {
      setRemovingId(null)
    }
  }

  /**
   * Bring somebody back (S-F2g2-5): the person returns with the same role on the
   * server; the row is restored exactly when the answer is an error.
   */
  const handleReactivate = async (member: SalonMember): Promise<void> => {
    setNotice(null)
    setLeaveError(null)
    setReactivatingId(member.id)
    setMembers((list) => applyMemberEndedAt(list, member.id, null))
    try {
      const saved = await reactivateMember(salonId, member.id)
      setMembers((list) => restoreMember(list, saved))
      setNotice(reactivatedSuccess(saved.name))
      setConfirmReactivateId(null)
      reload()
    } catch (err: unknown) {
      setMembers((list) => restoreMember(list, member))
      setLeaveError({
        id: member.id,
        message: describeApiError(err, 'No pudimos reactivar a esa persona.').message,
      })
    } finally {
      setReactivatingId(null)
    }
  }

  return (
    <>
      <PageHeader
        eyebrow="Equipo"
        title="Equipo del salón"
        description="Quien tenga cuenta puede entrar al panel con el rol que le asignes. Solo la dueña o el dueño puede nombrar administración. Con el interruptor «Atiende clientes» decidís quién aparece en la agenda para recibir turnos."
      />
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      <form className="panel-card team-form" onSubmit={handleAdd} noValidate>
        <h2>Agregar a alguien del equipo</h2>
        <p className="page-head__desc">
          La persona ya debe tener cuenta en el salón: se identifica por su correo.
        </p>
        <div className="team-form__fields">
          <div className="field">
            <label htmlFor="member-email">Correo</label>
            <input
              id="member-email"
              className="input"
              type="email"
              name="email"
              autoComplete="email"
              placeholder="persona@correo.com"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              required
            />
          </div>
          <div className="field">
            <label htmlFor="member-role">Rol</label>
            <select
              id="member-role"
              className="select"
              value={newRole}
              onChange={(event) => setNewRole(event.target.value as SalonRole)}
            >
              {options.map((option) => (
                <option key={option} value={option}>
                  {roleLabel(option)}
                </option>
              ))}
            </select>
            {!canAssignRole(role, 'admin') ? (
              <p className="field__hint">Solo la dueña o el dueño puede agregar administración.</p>
            ) : null}
          </div>
        </div>
        {formError ? (
          <p className="notice notice--error" role="alert">
            {formError}
          </p>
        ) : null}
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving ? 'Agregando…' : 'Agregar al equipo'}
        </button>
      </form>

      <TeamInvitations salonId={salonId} actorRole={role} members={team} />

      <section className="panel-card" aria-labelledby="team-list-title">
        <div className="panel-card__head">
          <h2 id="team-list-title">Quiénes tienen acceso</h2>
          <span className="panel-card__meta">{team.length} personas</span>
        </div>
        {loading && !loaded ? <LoadingState label="Cargando equipo…" /> : null}
        {loaded && nobodyAttends(members) ? (
          <p className="notice notice--info" role="status">
            {ATTENDS_NOTICE}
          </p>
        ) : null}
        {attendsError ? (
          <p className="notice notice--error" role="alert">
            {attendsError}
          </p>
        ) : null}
        {loaded && team.length === 0 ? (
          <p className="empty-state">Todavía no hay nadie más en el equipo.</p>
        ) : null}
        {team.length > 0 ? (
          <div className="table-wrap">
            <table className="table table--stack">
              <thead>
                <tr>
                  <th scope="col">Persona</th>
                  <th scope="col">Correo</th>
                  <th scope="col">Rol</th>
                  {showAttends ? <th scope="col">Atiende</th> : null}
                  <th scope="col" className="table__actions">
                    Acciones
                  </th>
                </tr>
              </thead>
              <tbody>
                {team.map((member) => {
                  const control = attendsControl(role, member.role)
                  const pending = pendingAttendsId === member.id
                  return (
                    <tr key={member.id}>
                      <td data-label="Persona">
                        {member.name}
                        {member.id === user?.id ? <span className="table__id">tú</span> : null}
                      </td>
                      <td data-label="Correo">{member.email || '—'}</td>
                      <td data-label="Rol">
                        <span className="pill pill--on">{roleLabel(member.role)}</span>
                      </td>
                      {showAttends ? (
                        <td data-label="Atiende" className="table__attends">
                          {control === 'switch' ? (
                            <label className="attends" data-pending={pending}>
                              <input
                                type="checkbox"
                                role="switch"
                                className="attends__input"
                                checked={memberAttends(member)}
                                aria-checked={memberAttends(member)}
                                aria-busy={pending}
                                aria-label={`Atiende clientes: ${member.name}`}
                                onChange={() => void handleAttendsToggle(member)}
                              />
                              <span className="attends__track" aria-hidden="true">
                                <span className="attends__thumb" />
                              </span>
                              <span className="attends__text">Atiende clientes</span>
                            </label>
                          ) : control === 'fixed' ? (
                            <span className="pill pill--on">Atiende</span>
                          ) : null}
                        </td>
                      ) : null}
                      <td data-label="Acciones" className="table__actions">
                        {confirmRemoveId === member.id ? (
                          <span
                            className="team-confirm"
                            role="group"
                            aria-label={`Confirmar baja de ${member.name}`}
                          >
                            <span className="team-confirm__question">
                              {deactivateConfirmQuestion(member.name)}
                            </span>
                            <span className="team-confirm__note">
                              {DEACTIVATE_CONFIRM_NOTE}
                            </span>
                            <span className="team-confirm__buttons">
                              <button
                                type="button"
                                className="btn btn--wine btn--sm"
                                disabled={removingId === member.id}
                                onClick={() => void handleDeactivate(member)}
                              >
                                {removingId === member.id ? 'Dando de baja…' : 'Sí, dar de baja'}
                              </button>
                              <button
                                type="button"
                                className="btn btn--quiet btn--sm"
                                onClick={() => {
                                  setConfirmRemoveId(null)
                                  setLeaveError(null)
                                }}
                              >
                                Volver
                              </button>
                            </span>
                            {leaveError?.id === member.id ? (
                              <span className="team-confirm__error" role="alert">
                                {leaveError.message}
                              </span>
                            ) : null}
                          </span>
                        ) : (
                          <button
                            type="button"
                            className="btn btn--quiet btn--sm"
                            onClick={() => {
                              setConfirmRemoveId(member.id)
                              setLeaveError(null)
                            }}
                            aria-label={`Dar de baja a ${member.name}`}
                          >
                            Dar de baja
                          </button>
                        )}
                      </td>
                    </tr>
                  )
                })}
              </tbody>
            </table>
          </div>
        ) : null}
      </section>

      {loaded && exTeam.length > 0 ? (
        <section className="panel-card ex-team" aria-labelledby="ex-team-title">
          <div className="panel-card__head">
            <h2 id="ex-team-title">
              <button
                type="button"
                className="ex-team__toggle"
                aria-expanded={exTeamOpen}
                aria-controls="ex-team-list"
                onClick={() => setExTeamOpen((open) => !open)}
              >
                <span className="ex-team__chevron" aria-hidden="true">
                  {exTeamOpen ? '▾' : '▸'}
                </span>
                {formerTeamLabel(exTeam.length)}
              </button>
            </h2>
            <span className="panel-card__meta">
              {exTeamOpen ? 'Ocultar' : 'Ver a quiénes salieron'}
            </span>
          </div>
          {exTeamOpen ? (
            <>
              <p className="page-head__desc">
                Ya no tienen acceso al salón, pero su historial de turnos y ventas se conserva.
                Reactivar a alguien le devuelve el acceso con el mismo rol.
              </p>
              <ul id="ex-team-list" className="ex-team__list">
              {exTeam.map((member) => {
                const left = endedOnLabel(member.ended_at, { timeZone })
                const pending = reactivatingId === member.id
                return (
                  <li key={member.id} className="ex-team__item">
                    <div className="ex-team__person">
                      <span className="ex-team__name">
                        {member.name}
                        {member.id === user?.id ? <span className="table__id">tú</span> : null}
                      </span>
                      <span className="ex-team__mail">{member.email || '—'}</span>
                      <span className="ex-team__meta">
                        <span className="pill pill--off">{roleLabel(member.role)}</span>
                        <span className="ex-team__date">{left ?? 'Sin fecha de baja'}</span>
                      </span>
                    </div>
                    <div className="ex-team__actions">
                      {confirmReactivateId === member.id ? (
                        <span
                          className="team-confirm"
                          role="group"
                          aria-label={`Confirmar reactivación de ${member.name}`}
                        >
                          <span className="team-confirm__question">
                            {reactivateConfirmQuestion(member.name)}
                          </span>
                          <span className="team-confirm__note">{REACTIVATE_CONFIRM_NOTE}</span>
                          <span className="team-confirm__buttons">
                            <button
                              type="button"
                              className="btn btn--wine btn--sm"
                              disabled={pending}
                              onClick={() => void handleReactivate(member)}
                            >
                              {pending ? 'Reactivando…' : 'Sí, reactivar'}
                            </button>
                            <button
                              type="button"
                              className="btn btn--quiet btn--sm"
                              onClick={() => {
                                setConfirmReactivateId(null)
                                setLeaveError(null)
                              }}
                            >
                              Volver
                            </button>
                          </span>
                          {leaveError?.id === member.id ? (
                            <span className="team-confirm__error" role="alert">
                              {leaveError.message}
                            </span>
                          ) : null}
                        </span>
                      ) : (
                        <button
                          type="button"
                          className="btn btn--quiet btn--sm ex-team__reactivate"
                          onClick={() => {
                            setConfirmReactivateId(member.id)
                            setLeaveError(null)
                          }}
                          aria-label={`Reactivar a ${member.name}`}
                        >
                          Reactivar
                        </button>
                      )}
                    </div>
                  </li>
                )
                })}
              </ul>
            </>
          ) : null}
        </section>
      ) : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="team-work-title">
          <div className="panel-card__head">
            <h2 id="team-work-title">Oficio y servicios de quien atiende</h2>
            <span className="panel-card__meta">{bookable.length} personas</span>
          </div>
          <p className="page-head__desc">
            El oficio es un dato visible del equipo (barbero, estilista…), no un permiso. Una
            clienta solo recibe turnos con las profesionales que hacen el servicio que pidió.
          </p>
          {workServicesError ? (
            <p className="notice notice--error" role="alert">
              {workServicesError}
            </p>
          ) : null}
          {bookable.length === 0 ? (
            <p className="empty-state">
              Nadie atiende clientes todavía, así que no hay oficios ni servicios que asignar.
            </p>
          ) : null}
          <div className="work-grid">
            {bookable.map((member) => (
              <MemberWorkCard
                key={member.id}
                member={member}
                services={workServices}
                onSaveTrade={(trade) => handleSaveTrade(member, trade)}
                onSaveServices={(serviceIds) => handleSaveServices(member, serviceIds)}
              />
            ))}
          </div>
        </section>
      ) : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="team-timeoff-title">
          <div className="panel-card__head">
            <h2 id="team-timeoff-title">Ausencias de quien atiende</h2>
            <span className="panel-card__meta">{absences.length} cargadas</span>
          </div>
          <p className="page-head__desc">
            Vacaciones y días libres puntuales. Una ausencia bloquea la disponibilidad pública de esa
            persona; los turnos que ya tenía no se cancelan solos: «Cubrir» lista esos turnos para
            reasignarlos a otra profesional que haga el servicio, o para cancelarlos.
          </p>
          {absencesError ? (
            <p className="notice notice--error" role="alert">
              {absencesError}
            </p>
          ) : null}
          {absencesLoaded && bookable.length === 0 ? (
            <p className="empty-state">
              Nadie atiende clientes todavía, así que no hay ausencias que cargar.
            </p>
          ) : null}
          <div className="hours-grid">
            {bookable.map((member) => (
              <TimeOffCard
                key={member.id}
                salonId={salonId}
                member={member}
                entries={absences}
                coverAbsences={coverAbsences}
                coverReady={coverReady}
                timeZone={timeZone}
                now={now}
                onChanged={reload}
                onCovered={(message) => {
                  setNotice(message)
                  reload()
                }}
              />
            ))}
          </div>
        </section>
      ) : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="team-hours-title">
          <div className="panel-card__head">
            <h2 id="team-hours-title">Horario de quien atiende</h2>
            <span className="panel-card__meta">{bookable.length} personas</span>
          </div>
          <p className="page-head__desc">
            Por defecto cada persona trabaja con el horario del salón. Con «Horario propio» define
            sus días, hasta cuatro tramos por día y los descansos entre tramos: la reserva pública
            solo ofrece turnos que caben enteros en un tramo.
          </p>
          {salonHoursError ? (
            <p className="notice notice--error" role="alert">
              {salonHoursError}
            </p>
          ) : null}
          {bookable.length === 0 ? (
            <p className="empty-state">
              Nadie atiende clientes todavía, así que no hay horarios que asignar.
            </p>
          ) : null}
          <div className="hours-grid">
            {bookable.map((member) => (
              <MemberHoursCard
                key={member.id}
                salonId={salonId}
                member={member}
                salonDays={salonDays}
              />
            ))}
          </div>
        </section>
      ) : null}
    </>
  )
}

type MemberWorkCardProps = {
  member: SalonMember
  services: readonly Service[]
  onSaveTrade: (trade: string) => Promise<SalonMember>
  onSaveServices: (serviceIds: string[]) => Promise<string[]>
}

/**
 * "Oficio" and "Servicios que hace" of one bookable member (S-F2c-6). Both save
 * against the API; the services list uses the optimistic update of the parent.
 */
function MemberWorkCard({ member, services, onSaveTrade, onSaveServices }: MemberWorkCardProps) {
  const [trade, setTrade] = useState(member.trade)
  const [tradePending, setTradePending] = useState(false)
  const [tradeError, setTradeError] = useState<string | null>(null)
  const [tradeSaved, setTradeSaved] = useState(false)
  const [selected, setSelected] = useState<string[]>(member.service_ids)
  const [servicesPending, setServicesPending] = useState(false)
  const [servicesError, setServicesError] = useState<string | null>(null)
  const [servicesNotice, setServicesNotice] = useState<string | null>(null)

  useEffect(() => {
    setTrade(member.trade)
  }, [member.trade])

  useEffect(() => {
    setSelected(member.service_ids)
  }, [member.service_ids])

  const commitTrade = async (): Promise<void> => {
    if (tradePending) {
      return
    }
    const normalized = normalizeTrade(trade)
    setTrade(normalized)
    setTradeSaved(false)
    if (normalized === member.trade) {
      setTradeError(null)
      return
    }
    setTradePending(true)
    setTradeError(null)
    try {
      const saved = await onSaveTrade(normalized)
      setTrade(saved.trade)
      setTradeSaved(true)
    } catch (err: unknown) {
      setTradeError(describeApiError(err, 'No pudimos guardar el oficio.').message)
    } finally {
      setTradePending(false)
    }
  }

  const toggleService = (serviceId: string): void => {
    setServicesNotice(null)
    setSelected((ids) =>
      ids.includes(serviceId) ? ids.filter((id) => id !== serviceId) : [...ids, serviceId],
    )
  }

  const changed =
    selected.length !== member.service_ids.length ||
    selected.some((id) => !member.service_ids.includes(id))

  const saveServices = async (): Promise<void> => {
    if (servicesPending) {
      return
    }
    setServicesPending(true)
    setServicesError(null)
    setServicesNotice(null)
    try {
      const saved = await onSaveServices(selected)
      setSelected(saved)
      setServicesNotice('Servicios guardados.')
    } catch (err: unknown) {
      setServicesError(
        describeApiError(err, 'No pudimos guardar los servicios de esta persona.').message,
      )
    } finally {
      setServicesPending(false)
    }
  }

  const tradeId = `member-trade-${member.id}`

  return (
    <article className="panel-card work-card" aria-labelledby={`member-work-${member.id}`}>
      <div className="work-card__head">
        <h3 id={`member-work-${member.id}`}>{member.name}</h3>
        <span className="pill pill--on">{roleLabel(member.role)}</span>
      </div>

      <div className="work-card__fields">
        <div className="field">
          <label htmlFor={tradeId}>Oficio</label>
          <input
            id={tradeId}
            className="input"
            value={trade}
            maxLength={TRADE_MAX_LENGTH}
            placeholder={TRADE_PLACEHOLDER}
            aria-describedby={`${tradeId}-hint`}
            aria-busy={tradePending}
            onChange={(event) => {
              setTrade(event.target.value)
              setTradeSaved(false)
            }}
            onBlur={() => void commitTrade()}
            onKeyDown={(event) => {
              if (event.key === 'Enter') {
                event.preventDefault()
                event.currentTarget.blur()
              }
              if (event.key === 'Escape') {
                setTrade(member.trade)
                setTradeError(null)
                event.currentTarget.blur()
              }
            }}
          />
          <p id={`${tradeId}-hint`} className="field__hint">
            Se guarda al salir del campo o con Enter. Hasta {TRADE_MAX_LENGTH} caracteres.
          </p>
          {tradeError ? (
            <p className="field__error" role="alert">
              {tradeError}
            </p>
          ) : null}
          {tradeSaved ? (
            <p className="notice notice--success" role="status">
              Oficio guardado.
            </p>
          ) : null}
        </div>

        <fieldset className="check-group" aria-busy={servicesPending}>
          <legend>Servicios que hace</legend>
          {services.length === 0 ? (
            <p className="field__hint">
              El salón todavía no tiene servicios activos. Crea uno en Servicios y vuelve acá.
            </p>
          ) : (
            <div className="check-group__list">
              {services.map((service) => (
                <label key={service.id} className="check-group__option">
                  <input
                    type="checkbox"
                    checked={selected.includes(service.id)}
                    onChange={() => toggleService(service.id)}
                  />
                  <span>{service.name}</span>
                </label>
              ))}
            </div>
          )}
          {member.service_ids.length === 0 ? (
            <p className="notice notice--info" role="status">
              {NO_SERVICES_HINT}
            </p>
          ) : null}
          {servicesError ? (
            <p className="notice notice--error" role="alert">
              {servicesError}
            </p>
          ) : null}
          {servicesNotice ? (
            <p className="notice notice--success" role="status">
              {servicesNotice}
            </p>
          ) : null}
          <div className="work-card__actions">
            <button
              type="button"
              className="btn btn--wine btn--sm"
              disabled={servicesPending || !changed || services.length === 0}
              onClick={() => void saveServices()}
            >
              {servicesPending ? 'Guardando…' : 'Guardar servicios'}
            </button>
            <span className="field__hint">
              {selected.length} de {services.length} marcados
            </span>
          </div>
        </fieldset>
      </div>
    </article>
  )
}
