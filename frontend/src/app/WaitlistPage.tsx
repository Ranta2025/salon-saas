import { useCallback, useEffect, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '../lib/api'
import { activeRole, canViewWaitlist } from '../lib/permissions'
import {
  WAITLIST_EMPTY,
  WAITLIST_LOAD_FALLBACK,
  WAITLIST_REMOVE_FALLBACK,
  fetchWaitlist,
  removeWaitlistEntry,
  waitlistCountLabel,
  type WaitlistEntry,
} from '../lib/waitlist'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { WaitlistNotice } from './components/WaitlistNotice'
import { WaitlistTable } from './components/WaitlistTable'

/**
 * `/panel/lista-de-espera` (contract F7, D8 and D9c): the avisos of the salon
 * from today on, with the service, the professional (or «Cualquiera»), the day,
 * the contact and the status of each one.
 *
 * Owner, admin and reception read it; anyone else gets the explanation instead
 * of the table (the API answers 403 first anyway). «Quitar» asks for a
 * confirmation and the list is read again from the server after the DELETE: no
 * optimistic paint, so the table never shows a row the server did not remove.
 */
export function WaitlistPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canViewWaitlist(role)

  const [entries, setEntries] = useState<WaitlistEntry[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [removingId, setRemovingId] = useState<string | null>(null)
  const [actionError, setActionError] = useState<string | null>(null)
  const [success, setSuccess] = useState<string | null>(null)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    const controller = new AbortController()
    let active = true
    setLoading(true)
    setError(null)
    fetchWaitlist(salonId, { signal: controller.signal })
      .then((rows) => {
        if (active) {
          setEntries(rows)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(describeApiError(err, WAITLIST_LOAD_FALLBACK))
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
  }, [salonId, allowed, reloadKey])

  const handleRemove = async (entry: WaitlistEntry): Promise<void> => {
    if (!salonId || removingId !== null) {
      return
    }
    setRemovingId(entry.id)
    setActionError(null)
    setSuccess(null)
    try {
      await removeWaitlistEntry(salonId, entry.id)
      setSuccess('El aviso se quitó de la lista: esa persona ya no recibe el aviso de ese día.')
      // The list comes back from the server, never from a local removal.
      reload()
    } catch (err: unknown) {
      setActionError(describeApiError(err, WAITLIST_REMOVE_FALLBACK).message)
      // A 404 means somebody else already removed it: the table must follow.
      reload()
    } finally {
      setRemovingId(null)
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Lista de espera" title="Lista de espera" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Lista de espera" title="Lista de espera" />
        <WaitlistNotice role={role} />
      </>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Lista de espera"
        title="Lista de espera"
        description="Personas que pidieron que les avisemos si se libera un turno. El aviso sale por correo cuando el hueco aparece; la reserva no queda hecha y el horario sigue libre hasta que alguien lo toma."
        actions={
          <button type="button" className="btn btn--quiet" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {success !== null ? (
        <p className="notice notice--success" role="status">
          {success}
        </p>
      ) : null}
      {loading && !loaded ? <LoadingState label="Cargando la lista de espera…" /> : null}
      {loading && loaded ? <LoadingState label="Actualizando la lista de espera…" /> : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="waitlist-list-title">
          <div className="panel-card__head">
            <h2 id="waitlist-list-title">Avisos de hoy en adelante</h2>
            <span className="panel-card__meta">{waitlistCountLabel(entries.length)}</span>
          </div>
          {entries.length === 0 ? (
            <p className="empty-state">{WAITLIST_EMPTY}</p>
          ) : (
            <WaitlistTable
              entries={entries}
              removingId={removingId}
              error={actionError}
              onRemove={(entry) => void handleRemove(entry)}
            />
          )}
        </section>
      ) : null}
    </>
  )
}
