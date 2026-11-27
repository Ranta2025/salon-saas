import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import {
  AUDIT_FILTER_OPTIONS,
  auditEmptyMessage,
  fetchAuditLog,
  mergeAuditEvents,
  type AuditEvent,
} from '../lib/audit'
import { TIME_ZONE } from '@/lib/datetime'
import { activeRole, canViewAuditLog } from '@/lib/permissions'
import { fetchSettings } from '@/features/settings/lib/settings'
import { useOffline } from '@/app/OfflineContext'
import { useSalon } from '@/app/SalonContext'
import { ActivityList } from '../components/ActivityList'
import { ActivityNotice } from '../components/ActivityNotice'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'

/**
 * `/panel/actividad` (contract F10, D3/D13): the audit log of the salon, newest
 * first, with an action filter and «Cargar más» by cursor. Owner and admin only;
 * everyone else gets `ActivityNotice` and no request is sent (the server would
 * answer 403 `sin permiso`).
 *
 * Online only and read only. A response that is not the documented shape is an
 * error, never «no hay actividad». Changing the filter or the salon starts over and
 * a late answer of the previous query is dropped.
 */
export function ActivityPage() {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewAuditLog(role)

  const [action, setAction] = useState('')
  const [events, setEvents] = useState<AuditEvent[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [moreError, setMoreError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  // Bumped on every new query: an answer of an older one is ignored. The flag of
  // «Cargar más» lives in a ref so two quick presses send ONE request.
  const generation = useRef(0)
  const moreBusy = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // The zone of the salon only changes how the dates read: a failure keeps the default.
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
        // TIME_ZONE stays as the fallback.
      })
    return () => {
      active = false
    }
  }, [salonId, allowed])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    generation.current += 1
    const mine = generation.current
    moreBusy.current = false
    setLoading(true)
    setLoaded(false)
    setLoadingMore(false)
    setError(null)
    setMoreError(null)
    // Another salon or filter: never keep the previous list on screen while this loads.
    setEvents([])
    setNextCursor(null)
    fetchAuditLog(salonId, { action })
      .then((page) => {
        if (generation.current === mine) {
          setEvents(page.events)
          setNextCursor(page.next_cursor)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setError(describeApiError(err, 'No pudimos cargar la actividad.'))
        }
      })
      .finally(() => {
        if (generation.current === mine) {
          setLoading(false)
        }
      })
    return () => {
      // Invalidates this query when the effect re-runs or the page unmounts.
      generation.current += 1
    }
  }, [salonId, allowed, action, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Gestión" title="Actividad" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Gestión" title="Actividad" />
        <ActivityNotice role={role} />
      </>
    )
  }

  const handleLoadMore = (): void => {
    if (nextCursor === null || moreBusy.current) {
      return
    }
    moreBusy.current = true
    const mine = generation.current
    setLoadingMore(true)
    setMoreError(null)
    fetchAuditLog(salonId, { action, before: nextCursor })
      .then((page) => {
        if (generation.current === mine) {
          setEvents((current) => mergeAuditEvents(current, page.events))
          setNextCursor(page.next_cursor)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setMoreError(describeApiError(err, 'No pudimos cargar más actividad.').message)
        }
      })
      .finally(() => {
        if (generation.current === mine) {
          moreBusy.current = false
          setLoadingMore(false)
        }
      })
  }

  return (
    <>
      <PageHeader
        eyebrow="Gestión"
        title="Actividad"
        description="Quién hizo qué en la gestión del salón: equipo, liquidaciones, gastos, cierres de caja, exportes y ajustes. Solo lo ven la dueña y la administración."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. La actividad solo se consulta en línea: lo que ves puede estar
          desactualizado.
        </p>
      ) : null}

      <section className="panel-card" aria-labelledby="activity-title">
        <div className="panel-card__head">
          <h2 id="activity-title">Registro</h2>
          <div className="field activity-filter">
            <label htmlFor="activity-action">Acción</label>
            <select
              id="activity-action"
              className="select"
              value={action}
              onChange={(event) => setAction(event.target.value)}
            >
              {AUDIT_FILTER_OPTIONS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {loading ? <LoadingState label="Cargando la actividad…" /> : null}
        {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

        {loaded && events.length === 0 ? (
          <p className="empty-state">{auditEmptyMessage(action)}</p>
        ) : null}

        {events.length > 0 ? <ActivityList events={events} timeZone={timeZone} /> : null}

        {moreError ? (
          <p className="notice notice--error" role="alert">
            {moreError}
          </p>
        ) : null}

        {loaded && nextCursor !== null ? (
          <div className="activity-more">
            <button
              type="button"
              className="btn btn--ghost"
              onClick={handleLoadMore}
              disabled={loadingMore}
            >
              {loadingMore ? 'Cargando…' : 'Cargar más'}
            </button>
          </div>
        ) : null}
      </section>
    </>
  )
}
