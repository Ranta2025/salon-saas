import { useCallback, useEffect, useState } from 'react'
import { Link } from 'react-router-dom'
import { fetchClients } from '../lib/api'
import type { Client } from '../types'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'

export function ClientsPage() {
  const { salonId } = useSalon()
  const [clients, setClients] = useState<Client[]>([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

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
          setError(err instanceof Error ? err.message : 'No pudimos cargar los clientes.')
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

  const term = search.trim().toLowerCase()
  const visible = term
    ? clients.filter((client) =>
        [client.name, client.phone ?? '', client.email ?? ''].some((value) =>
          value.toLowerCase().includes(term),
        ),
      )
    : clients

  return (
    <>
      <PageHeader
        eyebrow="Clientes"
        title="Fichas de clientes"
        description="Cada ficha guarda las fotos de referencia del trabajo: cabello, uñas o maquillaje, con su variante WebP lista para revisar."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando clientes…' : 'Cargando clientes…'} />
      ) : null}
      {error ? <ErrorNotice message={error} onRetry={reload} /> : null}

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
                Las fichas se crean cuando alguien reserva desde el sitio público. Cuando llegue
                la primera clienta, aquí podrás abrir su ficha y subir fotos de referencia.
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
                      {client.phone ?? 'Sin teléfono'}
                      {client.email ? ` · ${client.email}` : ''}
                    </span>
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
