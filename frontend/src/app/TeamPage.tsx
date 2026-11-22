import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError, fetchServices, friendlyApiMessage, type ApiErrorNotice } from '../lib/api'
import type { SalonRole } from '../lib/me'
import type { Service } from '../types'
import {
  addMember,
  fetchMembers,
  normalizeMemberEmail,
  removeMember,
  setMemberAttends,
  setMemberServices,
  setMemberTrade,
  type SalonMember,
} from '../lib/members'
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
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'

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
  const [pendingAttendsId, setPendingAttendsId] = useState<string | null>(null)
  const [attendsError, setAttendsError] = useState<string | null>(null)
  const [workServices, setWorkServices] = useState<Service[]>([])
  const [workServicesError, setWorkServicesError] = useState<string | null>(null)
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
  // The column only exists when someone can attend or be marked as attending.
  const showAttends = members.some((member) => attendsControl(role, member.role) !== 'none')
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

  const handleRemove = async (memberId: string): Promise<void> => {
    setError(null)
    setNotice(null)
    setRemovingId(memberId)
    try {
      await removeMember(salonId, memberId)
      setNotice('La persona ya no tiene acceso al salón.')
      setConfirmRemoveId(null)
      reload()
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos quitar a esa persona del equipo.'))
    } finally {
      setRemovingId(null)
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

      <section className="panel-card" aria-labelledby="team-list-title">
        <div className="panel-card__head">
          <h2 id="team-list-title">Quiénes tienen acceso</h2>
          <span className="panel-card__meta">{members.length} personas</span>
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
        {loaded && members.length === 0 ? (
          <p className="empty-state">Todavía no hay nadie más en el equipo.</p>
        ) : null}
        {members.length > 0 ? (
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
                {members.map((member) => {
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
                          <span className="photo-confirm" role="group" aria-label="Confirmar baja">
                            <span>¿Quitar el acceso?</span>
                            <button
                              type="button"
                              className="btn btn--wine btn--sm"
                              disabled={removingId === member.id}
                              onClick={() => void handleRemove(member.id)}
                            >
                              Sí, quitar
                            </button>
                            <button
                              type="button"
                              className="btn btn--quiet btn--sm"
                              onClick={() => setConfirmRemoveId(null)}
                            >
                              Volver
                            </button>
                          </span>
                        ) : (
                          <button
                            type="button"
                            className="btn btn--quiet btn--sm"
                            onClick={() => setConfirmRemoveId(member.id)}
                            aria-label={`Quitar el acceso de ${member.name}`}
                          >
                            Quitar
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
