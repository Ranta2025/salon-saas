import { useCallback, useEffect, useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { PhoneInput } from '@/components/PhoneInput'
import { createClient, describeApiError, fetchClients, type ApiErrorNotice } from '@/lib/api'
import { EMAIL_INVALID_MESSAGE, isValidEmail } from '@/lib/email'
import { activeRole, canCreateClient } from '@/lib/permissions'
import { displayPhone } from '@/lib/phone'
import type { Client, ClientInput } from '@/types'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'

const EMPTY_DRAFT: ClientInput = { name: '', phone: '', email: '', notes: '' }

export function ClientsPage() {
  const { salonId, salons } = useSalon()
  const [clients, setClients] = useState<Client[]>([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [draft, setDraft] = useState<ClientInput>(EMPTY_DRAFT)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [createdName, setCreatedName] = useState<string | null>(null)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    fetchClients(salonId, controller.signal)
      .then((list) => {
        if (active) {
          setClients(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(describeApiError(err, 'No pudimos cargar los clientes.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [salonId, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Clientes" title="Fichas de clientes" />
        <SalonRequired />
      </>
    )
  }

  const role = activeRole(salons, salonId)
  // The API only accepts owner, admin and reception; a professional never sees
  // a form that would answer 403.
  const canCreate = canCreateClient(role)
  const term = search.trim().toLowerCase()
  const visible = term
    ? clients.filter((client) =>
        [client.name, client.phone ?? '', client.email ?? ''].some((value) =>
          value.toLowerCase().includes(term),
        ),
      )
    : clients

  const handleCreate = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    setFormError(null)
    setCreatedName(null)
    if (draft.name.trim() === '') {
      setFormError('Escribe el nombre de la clienta.')
      return
    }
    // Same rule and message as the server (and as the public booking form).
    if (!isValidEmail(draft.email)) {
      setFormError(EMAIL_INVALID_MESSAGE)
      return
    }
    setSaving(true)
    try {
      const created = await createClient(salonId, draft)
      // The list stays alphabetical, like the server returns it.
      setClients((current) =>
        [...current, created].sort((a, b) => a.name.localeCompare(b.name, 'es')),
      )
      setLoaded(true)
      setCreatedName(created.name)
      setDraft(EMPTY_DRAFT)
    } catch (err: unknown) {
      setFormError(describeApiError(err, 'No pudimos crear la ficha.').message)
    } finally {
      setSaving(false)
    }
  }

  return (
    <>
      <PageHeader
        eyebrow="Clientes"
        title="Fichas de clientes"
        description="Cada ficha guarda el contacto y las fotos de referencia del trabajo: cabello, uñas o maquillaje, con su variante WebP lista para revisar."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {canCreate ? (
        <section className="panel-card clients-form" aria-labelledby="clients-form-title">
          <div className="panel-card__head">
            <h2 id="clients-form-title">Nueva clienta</h2>
            <span className="panel-card__meta">Solo el nombre es obligatorio</span>
          </div>
          <form className="clients-form__form" onSubmit={handleCreate} noValidate>
            <div className="clients-form__fields">
              <div className="field">
                <label htmlFor="new-client-name">Nombre</label>
                <input
                  id="new-client-name"
                  className="input"
                  value={draft.name}
                  onChange={(event) => setDraft({ ...draft, name: event.target.value })}
                  autoComplete="name"
                  required
                />
              </div>
              <PhoneInput
                id="new-client-phone"
                label="Teléfono"
                value={draft.phone}
                onChange={(phone) => setDraft({ ...draft, phone })}
              />
              <div className="field">
                <label htmlFor="new-client-email">Correo</label>
                <input
                  id="new-client-email"
                  className="input"
                  type="email"
                  value={draft.email}
                  onChange={(event) => setDraft({ ...draft, email: event.target.value })}
                  autoComplete="email"
                  placeholder="clienta@correo.com"
                />
                <p className="field__hint">
                  Opcional. La reserva pública lo pide para confirmar y recordar el turno.
                </p>
              </div>
              <div className="field clients-form__notes">
                <label htmlFor="new-client-notes">Notas</label>
                <textarea
                  id="new-client-notes"
                  className="input"
                  rows={2}
                  value={draft.notes}
                  onChange={(event) => setDraft({ ...draft, notes: event.target.value })}
                  placeholder="Alergias, preferencias de horario, coloración usada…"
                />
              </div>
            </div>
            {formError ? (
              <p className="notice notice--error" role="alert">
                {formError}
              </p>
            ) : null}
            {createdName ? (
              <p className="notice notice--success" role="status">
                Ficha de {createdName} creada. Ya puedes abrirla para subir fotos de referencia.
              </p>
            ) : null}
            <button type="submit" className="btn btn--wine" disabled={saving}>
              {saving ? 'Creando ficha…' : 'Crear ficha'}
            </button>
          </form>
        </section>
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando clientes…' : 'Cargando clientes…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="clients-title">
          <div className="panel-card__head">
            <h2 id="clients-title">Clientes del salón</h2>
            <span className="panel-card__meta">
              {visible.length} de {clients.length}
            </span>
          </div>

          <div className="field clients-search">
            <label className="visually-hidden" htmlFor="clients-search">
              Buscar cliente
            </label>
            <input
              id="clients-search"
              className="input"
              placeholder="Buscar por nombre, teléfono o correo…"
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </div>

          {clients.length === 0 ? (
            <div className="empty-state">
              <h2>Todavía no hay clientes</h2>
              <p>
                {canCreate
                  ? 'Las fichas se crean solas cuando alguien reserva desde el sitio público, o aquí mismo con el formulario «Nueva clienta». Cuando llegue la primera, podrás abrir su ficha y subir fotos de referencia.'
                  : 'Las fichas se crean cuando alguien reserva desde el sitio público o cuando la administración del salón registra una clienta a mano.'}
              </p>
            </div>
          ) : visible.length === 0 ? (
            <p className="muted">Ningún cliente coincide con la búsqueda.</p>
          ) : (
            <ul className="clients-grid">
              {visible.map((client) => (
                <li key={client.id}>
                  <Link
                    className="client-card"
                    to={`/panel/clientes/${encodeURIComponent(client.id)}`}
                  >
                    <span className="client-card__name">{client.name}</span>
                    <span className="client-card__meta">
                      {displayPhone(client.phone ?? '') || 'Sin teléfono'}
                    </span>
                    <span className="client-card__meta">{client.email ?? 'Sin correo'}</span>
                    <span className="client-card__id">{client.id}</span>
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </section>
      ) : null}
    </>
  )
}
