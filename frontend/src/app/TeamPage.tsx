import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { describeApiError, friendlyApiMessage, type ApiErrorNotice } from '../lib/api'
import type { SalonRole } from '../lib/me'
import {
  addMember,
  fetchMembers,
  normalizeMemberEmail,
  removeMember,
  setMemberAttends,
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
    </>
  )
}
