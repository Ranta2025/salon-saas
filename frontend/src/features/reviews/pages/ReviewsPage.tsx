import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { TIME_ZONE } from '@/lib/datetime'
import { activeRole, canManageReviews, canViewReviews } from '@/lib/permissions'
import { fetchSettings } from '@/features/settings/lib/settings'
import { useOffline } from '@/app/OfflineContext'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'
import { ReviewList } from '../components/ReviewList'
import { ReviewSettingsForm } from '../components/ReviewSettingsForm'
import { ReviewsNotice } from '../components/ReviewsNotice'
import {
  applyReviewHidden,
  draftFromReviewSettings,
  fetchReviewSettings,
  fetchReviews,
  mergeReviews,
  REVIEWS_EMPTY_MESSAGE,
  saveReviewSettings,
  setReviewHidden,
  type PanelReview,
  type ReviewSettings,
  type ReviewSettingsDraft,
} from '../lib/reviews'

const EMPTY_DRAFT: ReviewSettingsDraft = draftFromReviewSettings(null)

/**
 * `/panel/resenas` (contract F13, D6/D8). Owner, admin and reception read the reviews
 * of the salon — stars, comment, author, date and service — newest first and with
 * «Cargar más» by cursor; owner and admin also hide/show one and write the settings
 * (switch and demora 1..72 hours). A professional gets `ReviewsNotice` and no request
 * is sent (the server would answer 403).
 *
 * Online only. Hiding paints the row FIRST and rolls it back exactly if the server
 * refuses; the settings are read again from the server after a save. A response that
 * is not the documented shape is an error, never «no hay reseñas».
 */
export function ReviewsPage() {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewReviews(role)
  const canManage = canManageReviews(role)

  const [items, setItems] = useState<PanelReview[]>([])
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [moreError, setMoreError] = useState<string | null>(null)
  const [rowError, setRowError] = useState<string | null>(null)
  const [pendingId, setPendingId] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [settings, setSettings] = useState<ReviewSettings | null>(null)
  const [draft, setDraft] = useState<ReviewSettingsDraft>(EMPTY_DRAFT)
  const [settingsLoaded, setSettingsLoaded] = useState(false)
  const [savingSettings, setSavingSettings] = useState(false)
  /** The GET failed: there is nothing to edit, the page offers «Actualizar». */
  const [settingsLoadError, setSettingsLoadError] = useState<string | null>(null)
  /** The PUT was rejected: what was typed is kept and the sentence is printed. */
  const [settingsSaveError, setSettingsSaveError] = useState<string | null>(null)
  const [settingsNotice, setSettingsNotice] = useState<string | null>(null)

  // Bumped on every new query: an answer of an older one is ignored. The flags of
  // «Cargar más» and of the row in flight live in refs, so two quick presses send ONE
  // request each.
  const generation = useRef(0)
  const moreBusy = useRef(false)
  const toggling = useRef<string | null>(null)
  const savingRef = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    generation.current += 1
    const mine = generation.current
    moreBusy.current = false
    toggling.current = null
    setLoading(true)
    setLoaded(false)
    setLoadingMore(false)
    setError(null)
    setMoreError(null)
    setRowError(null)
    setPendingId(null)
    // Another salon: never keep the previous list on screen while this loads.
    setItems([])
    setNextCursor(null)
    fetchReviews(salonId)
      .then((page) => {
        if (generation.current === mine) {
          setItems(page.items)
          setNextCursor(page.next_cursor)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setError(describeApiError(err, 'No pudimos cargar las reseñas.'))
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
  }, [salonId, allowed, reloadKey])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setSettingsLoaded(false)
    setSettings(null)
    setDraft(EMPTY_DRAFT)
    setSettingsLoadError(null)
    setSettingsSaveError(null)
    setSettingsNotice(null)
    // The zone only changes how the dates read: a failure keeps the default.
    fetchSettings(salonId)
      .then((saved) => {
        if (active) {
          setTimeZone(saved.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback.
      })
    fetchReviewSettings(salonId)
      .then((saved) => {
        if (active) {
          setSettings(saved)
          setDraft(draftFromReviewSettings(saved))
          setSettingsLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setSettingsLoadError(
            describeApiError(err, 'No pudimos cargar la configuración de las reseñas.').message,
          )
          setSettingsLoaded(true)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Reseñas" title="Reseñas del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Reseñas" title="Reseñas del salón" />
        <ReviewsNotice role={role} />
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
    fetchReviews(salonId, { before: nextCursor })
      .then((page) => {
        if (generation.current === mine) {
          setItems((current) => mergeReviews(current, page.items))
          setNextCursor(page.next_cursor)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setMoreError(describeApiError(err, 'No pudimos cargar más reseñas.').message)
        }
      })
      .finally(() => {
        if (generation.current === mine) {
          moreBusy.current = false
          setLoadingMore(false)
        }
      })
  }

  const handleToggle = (review: PanelReview): void => {
    if (!canManage || toggling.current !== null) {
      return
    }
    const previous = review.hidden
    const next = !previous
    toggling.current = review.id
    setPendingId(review.id)
    setRowError(null)
    // Optimistic paint: the row flips now and is put back EXACTLY as it was on error.
    setItems((current) => applyReviewHidden(current, review.id, next))
    setReviewHidden(salonId, review.id, next)
      .catch((err: unknown) => {
        setItems((current) => applyReviewHidden(current, review.id, previous))
        setRowError(describeApiError(err, 'No pudimos cambiar la visibilidad de la reseña.').message)
      })
      .finally(() => {
        toggling.current = null
        setPendingId(null)
      })
  }

  const handleSaveSettings = (payload: ReviewSettings): void => {
    if (savingRef.current) {
      return
    }
    savingRef.current = true
    setSavingSettings(true)
    setSettingsSaveError(null)
    setSettingsNotice(null)
    saveReviewSettings(salonId, payload)
      .then((saved) => {
        // No optimistic state: the form shows what the server stored.
        const stored = saved ?? payload
        setSettings(stored)
        setDraft(draftFromReviewSettings(stored))
        setSettingsNotice('Configuración guardada.')
      })
      .catch((err: unknown) => {
        setSettingsSaveError(
          describeApiError(err, 'No pudimos guardar la configuración de las reseñas.').message,
        )
      })
      .finally(() => {
        savingRef.current = false
        setSavingSettings(false)
      })
  }

  return (
    <>
      <PageHeader
        eyebrow="Reseñas"
        title="Reseñas del salón"
        description="Lo que escriben las clientas después de un turno atendido. El salón puede ocultar una reseña de su página pública, pero no editarla ni borrarla."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. Las reseñas solo se consultan y se cambian en línea: lo que ves puede estar
          desactualizado y nada queda en cola.
        </p>
      ) : null}
      {rowError ? (
        <p className="notice notice--error" role="alert">
          {rowError}
        </p>
      ) : null}

      <section className="panel-card" aria-labelledby="review-settings-title">
        <div className="panel-card__head">
          <h2 id="review-settings-title">Pedido de reseñas</h2>
          <span className={`pill ${settings?.enabled ? 'pill--on' : 'pill--off'}`}>
            {settings?.enabled ? 'Activado' : 'Apagado'}
          </span>
        </div>
        {!settingsLoaded ? <LoadingState label="Cargando la configuración…" /> : null}
        {settingsLoadError ? (
          <p className="notice notice--error" role="alert">
            {settingsLoadError}
          </p>
        ) : null}
        {settingsNotice ? (
          <p className="notice notice--success" role="status">
            {settingsNotice}
          </p>
        ) : null}
        {settingsLoaded && settingsLoadError === null ? (
          <>
            {settings === null && settingsSaveError === null ? (
              <p className="field__hint">
                No hay una configuración guardada todavía: al guardarla se empieza a pedir reseña
                ese tiempo después de cada turno.
              </p>
            ) : null}
            <ReviewSettingsForm
              draft={draft}
              onChange={setDraft}
              readOnly={!canManage}
              saving={savingSettings}
              offline={!online}
              serverError={settingsSaveError}
              onSubmit={handleSaveSettings}
            />
          </>
        ) : null}
      </section>

      <section className="panel-card" aria-labelledby="reviews-title">
        <div className="panel-card__head">
          <h2 id="reviews-title">Reseñas recibidas</h2>
          {loaded ? <span className="pill pill--off">{items.length}</span> : null}
        </div>

        {loading ? <LoadingState label="Cargando las reseñas…" /> : null}
        {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

        {loaded && items.length === 0 ? <p className="empty-state">{REVIEWS_EMPTY_MESSAGE}</p> : null}

        {items.length > 0 ? (
          <ReviewList
            items={items}
            timeZone={timeZone}
            canManage={canManage}
            pendingId={pendingId}
            onToggle={handleToggle}
          />
        ) : null}

        {moreError ? (
          <p className="notice notice--error" role="alert">
            {moreError}
          </p>
        ) : null}

        {loaded && nextCursor !== null ? (
          <div className="reviews-more">
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
