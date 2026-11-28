import { useCallback, useEffect, useRef, useState, type FormEvent } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { isValidEmail, normalizeEmail } from '@/lib/email'
import type { SalonMember } from '../lib/members'
import { roleLabel, type Role } from '@/lib/permissions'
import {
  INVITATION_MAX_PENDING,
  INVITATION_MESSAGES,
  LINK_ONCE_NOTE,
  applyRevoke,
  canInvite,
  copyInvitationLink,
  createInvitation,
  dismissLinkOnce,
  emailIsMember,
  expiryLabel,
  fetchInvitations,
  invitableRoles,
  invitationErrorMessage,
  invitationRoleLabel,
  linkDeliveryNotice,
  linkOnceFrom,
  revokeInvitation,
  rollbackRevoke,
  type InvitationRole,
  type LinkOnceState,
  type PendingInvitation,
} from '../lib/invitations'
import { ErrorNotice, LoadingState } from '@/app/components/Feedback'

type TeamInvitationsProps = {
  salonId: string
  actorRole: Role | null
  members: readonly SalonMember[]
}

/**
 * "Invitar a alguien" of `/panel/equipo` (S-F2g1-6): the form, the link that is
 * shown ONCE and the pending list with its revoke. The server sentences of the
 * contract are printed verbatim; the link lives only in this component's state
 * (never in storage, never in the URL) and disappears when dismissed.
 */
export function TeamInvitations({ salonId, actorRole, members }: TeamInvitationsProps) {
  const allowed = canInvite(actorRole)
  const options = invitableRoles(actorRole)

  const [invitations, setInvitations] = useState<PendingInvitation[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [listError, setListError] = useState<ApiErrorNotice | null>(null)
  const [email, setEmail] = useState('')
  const [role, setRole] = useState<InvitationRole>(options[0] ?? 'professional')
  const [formError, setFormError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [link, setLink] = useState<LinkOnceState>(null)
  const [copyState, setCopyState] = useState<'idle' | 'copied' | 'manual'>('idle')
  const [confirmRevokeId, setConfirmRevokeId] = useState<string | null>(null)
  const [revokingId, setRevokingId] = useState<string | null>(null)
  const [revokeError, setRevokeError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const linkInput = useRef<HTMLInputElement | null>(null)
  const [now] = useState(() => new Date())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setListError(null)
    fetchInvitations(salonId)
      .then((list) => {
        if (active) {
          setInvitations(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setListError(describeApiError(err, 'No pudimos cargar las invitaciones pendientes.'))
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

  // The owner may hand out every role; an admin never invites another admin.
  useEffect(() => {
    if (options.length > 0 && !options.includes(role)) {
      setRole(options[0] ?? 'professional')
    }
  }, [options, role])

  if (!allowed) {
    return null
  }

  const atLimit = invitations.length >= INVITATION_MAX_PENDING
  const memberAlready = emailIsMember(members, email)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setFormError(null)
    setNotice(null)
    const value = normalizeEmail(email)
    if (value === '') {
      setFormError('Escribí el correo de la persona.')
      return
    }
    if (!isValidEmail(value)) {
      // The same sentence of the endpoint: «Email inválido».
      setFormError(INVITATION_MESSAGES.email)
      return
    }
    setSaving(true)
    try {
      const created = await createInvitation(salonId, { email: value, role })
      // The link of the answer is the only copy that will ever exist (S-F2g1-3).
      setLink(linkOnceFrom(created))
      setCopyState('idle')
      // The delivery of the email is told next to the link, with the note that
      // the link is shown once: the notice only confirms the creation.
      setNotice(`Invitación creada para ${value}.`)
      setEmail('')
      reload()
    } catch (err: unknown) {
      setFormError(invitationErrorMessage(err, 'No pudimos crear la invitación.'))
    } finally {
      setSaving(false)
    }
  }

  const handleCopy = async (): Promise<void> => {
    if (!link) {
      return
    }
    const copied = await copyInvitationLink(link.link)
    setCopyState(copied ? 'copied' : 'manual')
    if (!copied) {
      // Visible fallback: the text is selected so it can be copied by hand.
      linkInput.current?.focus()
      linkInput.current?.select()
    }
  }

  const handleRevoke = async (invitation: PendingInvitation): Promise<void> => {
    setRevokeError(null)
    setNotice(null)
    setConfirmRevokeId(null)
    setRevokingId(invitation.id)
    // Optimistic: the row leaves before the answer and comes back on rejection.
    setInvitations((list) => applyRevoke(list, invitation.id))
    try {
      await revokeInvitation(salonId, invitation.id)
      setNotice(`La invitación de ${invitation.email || 'esa persona'} quedó revocada.`)
    } catch (err: unknown) {
      setInvitations((list) => rollbackRevoke(list, invitation))
      setRevokeError(invitationErrorMessage(err, 'No pudimos revocar la invitación.'))
    } finally {
      setRevokingId(null)
    }
  }

  return (
    <section className="panel-card" aria-labelledby="team-invite-title">
      <div className="panel-card__head">
        <h2 id="team-invite-title">Invitar a alguien</h2>
        <span className="panel-card__meta">{invitations.length} pendientes</span>
      </div>
      <p className="page-head__desc">
        La invitación llega por correo con un enlace de un solo uso que vence a los 7 días: la
        persona crea su cuenta y entra con el rol que elijas. Si el correo no sale, copiá el enlace
        y compartilo por WhatsApp.
      </p>

      <form className="team-form invite-form" onSubmit={handleSubmit} noValidate>
        <div className="team-form__fields">
          <div className="field">
            <label htmlFor="invite-email">Correo</label>
            <input
              id="invite-email"
              className="input"
              type="email"
              name="email"
              autoComplete="email"
              placeholder="persona@correo.com"
              value={email}
              onChange={(event) => setEmail(event.target.value)}
              aria-describedby="invite-email-hint"
              required
            />
            <p id="invite-email-hint" className="field__hint">
              Tiene que ser el correo con el que va a entrar al panel.
            </p>
            {memberAlready ? (
              <p className="notice notice--info" role="status">
                Esa persona ya es parte del equipo: no hace falta invitarla.
              </p>
            ) : null}
          </div>
          <div className="field">
            <label htmlFor="invite-role">Rol</label>
            <select
              id="invite-role"
              className="select"
              value={role}
              onChange={(event) => setRole(event.target.value as InvitationRole)}
            >
              {options.map((option) => (
                <option key={option} value={option}>
                  {roleLabel(option)}
                </option>
              ))}
            </select>
            {options.includes('admin') ? (
              <p className="field__hint">
                Solo la dueña o el dueño puede invitar con el rol Administración.
              </p>
            ) : (
              <p className="field__hint">
                Desde una cuenta de administración se invita a profesionales y recepción.
              </p>
            )}
          </div>
        </div>
        {atLimit ? (
          <p className="notice notice--info" role="status">
            {INVITATION_MESSAGES.maxPending}: revocá alguna antes de invitar a otra persona.
          </p>
        ) : null}
        {formError ? (
          <p className="notice notice--error" role="alert">
            {formError}
          </p>
        ) : null}
        <button type="submit" className="btn btn--wine" disabled={saving || atLimit}>
          {saving ? 'Invitando…' : 'Crear invitación'}
        </button>
      </form>

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {link ? (
        <div className="invite-link" aria-labelledby="invite-link-title">
          <h3 id="invite-link-title">Enlace de la invitación</h3>
          <p className="invite-link__note">{LINK_ONCE_NOTE}</p>
          <div className="invite-link__row">
            <label className="visually-hidden" htmlFor="invite-link-input">
              Enlace de la invitación
            </label>
            <input
              id="invite-link-input"
              ref={linkInput}
              className="input invite-link__input"
              type="text"
              inputMode="url"
              readOnly
              value={link.link}
              onFocus={(event) => event.currentTarget.select()}
            />
            <button type="button" className="btn btn--wine btn--sm" onClick={() => void handleCopy()}>
              Copiar enlace
            </button>
          </div>
          <p className="field__hint">{linkDeliveryNotice(link.emailSent)}</p>
          {copyState === 'manual' ? (
            <p className="notice notice--info">
              El navegador no dejó copiar: el enlace está seleccionado, copialo con Ctrl + C.
            </p>
          ) : null}
          <p className="visually-hidden" role="status">
            {copyState === 'copied'
              ? 'Enlace copiado.'
              : copyState === 'manual'
                ? 'No pudimos copiar el enlace: está seleccionado para copiarlo a mano.'
                : ''}
          </p>
          <button
            type="button"
            className="btn btn--quiet btn--sm"
            onClick={() => {
              setLink(dismissLinkOnce())
              setCopyState('idle')
            }}
          >
            Ya lo guardé
          </button>
        </div>
      ) : null}

      <h3 className="invite-pending__title">Invitaciones pendientes</h3>
      {loading && !loaded ? <LoadingState label="Cargando invitaciones…" /> : null}
      {listError ? <ErrorNotice error={listError} onRetry={reload} /> : null}
      {revokeError ? (
        <p className="notice notice--error" role="alert">
          {revokeError}
        </p>
      ) : null}
      {loaded && invitations.length === 0 && !listError ? (
        <p className="empty-state">No hay invitaciones pendientes.</p>
      ) : null}
      {invitations.length > 0 ? (
        <ul className="invite-list">
          {invitations.map((invitation) => {
            const expires = expiryLabel(invitation.expires_at, { now })
            return (
              <li key={invitation.id} className="invite-list__item">
                <span className="invite-list__email">{invitation.email || 'Sin correo'}</span>
                <span className="pill pill--on">{invitationRoleLabel({ role: invitation.role })}</span>
                <span className="invite-list__expiry">{expires ?? 'Sin fecha de vencimiento'}</span>
                {confirmRevokeId === invitation.id ? (
                  <span className="invite-list__actions" role="group" aria-label="Confirmar revocación">
                    <span>¿Revocar la invitación?</span>
                    <button
                      type="button"
                      className="btn btn--wine btn--sm"
                      disabled={revokingId === invitation.id}
                      onClick={() => void handleRevoke(invitation)}
                    >
                      Sí, revocar
                    </button>
                    <button
                      type="button"
                      className="btn btn--quiet btn--sm"
                      onClick={() => setConfirmRevokeId(null)}
                    >
                      Volver
                    </button>
                  </span>
                ) : (
                  <button
                    type="button"
                    className="btn btn--quiet btn--sm"
                    onClick={() => setConfirmRevokeId(invitation.id)}
                    aria-label={`Revocar la invitación de ${invitation.email || 'esa persona'}`}
                  >
                    Revocar
                  </button>
                )}
              </li>
            )
          })}
        </ul>
      ) : null}
    </section>
  )
}
