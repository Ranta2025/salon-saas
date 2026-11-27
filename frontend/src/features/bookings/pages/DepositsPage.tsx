import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { TIME_ZONE } from '@/lib/datetime'
import {
  createDepositGate,
  DEPOSIT_CASH_NOTE,
  DEPOSIT_STATUS_FILTERS,
  depositPreview,
  draftFromPolicy,
  fetchDepositPolicy,
  fetchDeposits,
  mergeDepositRows,
  writeDepositPolicy,
  type DepositPolicy,
  type DepositPolicyDraft,
  type DepositPolicyPayload,
  type DepositRow,
} from '../lib/deposits'
import { activeRole, canEditDepositPolicy, canViewDeposits } from '@/lib/permissions'
import { fetchSettings } from '@/lib/settings'
import { useOffline } from '@/app/OfflineContext'
import { useSalon } from '@/app/SalonContext'
import { DepositList } from '../components/DepositList'
import { DepositNotice } from '../components/DepositNotice'
import { DepositPolicyForm } from '../components/DepositPolicyForm'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'

const EMPTY_DRAFT: DepositPolicyDraft = draftFromPolicy(null)

/**
 * `/panel/senas` (contract F11, D3, D10, D13-a): the deposit policy of the salon,
 * the preview of what the client sees and the list of deposits with its status
 * filter and «Cargar más» by cursor. Owner and admin edit the policy; reception
 * reads it and sees the list, because it is the one that discounts the paid deposit
 * at the register. A professional gets `DepositNotice` and no request is sent.
 *
 * Online only: offline the policy is not sent and nothing is queued. After a write
 * the policy is read again from the server (no optimistic state) and a rejected
 * write keeps what was typed.
 */
export function DepositsPage() {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewDeposits(role)
  const canEdit = canEditDepositPolicy(role)

  const [policy, setPolicy] = useState<DepositPolicy | null>(null)
  const [draft, setDraft] = useState<DepositPolicyDraft>(EMPTY_DRAFT)
  const [deposits, setDeposits] = useState<DepositRow[]>([])
  const [status, setStatus] = useState('')
  const [nextCursor, setNextCursor] = useState<string | null>(null)
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [loadingMore, setLoadingMore] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [listError, setListError] = useState<ApiErrorNotice | null>(null)
  const [moreError, setMoreError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  // The gate lives outside React state: two presses of the same render would both
  // see `saving === false` and send two requests.
  const gate = useRef(createDepositGate())
  // Bumped on every new query: an answer of an older one is ignored. The flag of
  // «Cargar más» lives in a ref so two quick presses send ONE request.
  const generation = useRef(0)
  const moreBusy = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // The policy, the zone of the salon and the first page of the list.
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
    setListError(null)
    setMoreError(null)
    // Another salon: never keep showing the previous salon's policy or list.
    setPolicy(null)
    setDraft(EMPTY_DRAFT)
    setDeposits([])
    setNextCursor(null)

    // The zone only changes how the dates read: a failure keeps the default.
    fetchSettings(salonId)
      .then((settings) => {
        if (generation.current === mine) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback.
      })

    fetchDepositPolicy(salonId)
      .then((saved) => {
        if (generation.current === mine) {
          setPolicy(saved)
          setDraft(draftFromPolicy(saved))
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setError(describeApiError(err, 'No pudimos cargar la política de señas.'))
        }
      })
      .finally(() => {
        if (generation.current === mine) {
          setLoading(false)
        }
      })

    fetchDeposits(salonId, { status })
      .then((page) => {
        if (generation.current === mine) {
          setDeposits(page.deposits)
          setNextCursor(page.next_cursor)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setListError(describeApiError(err, 'No pudimos cargar las señas.'))
        }
      })
    return () => {
      // Invalidates this query when the effect re-runs or the page unmounts.
      generation.current += 1
    }
  }, [salonId, allowed, status, reloadKey])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Cobros" title="Señas" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Cobros" title="Señas" />
        <DepositNotice role={role} />
      </>
    )
  }

  const handleSubmit = (payload: DepositPolicyPayload): void => {
    void (async () => {
      setSaving(true)
      setFormError(null)
      setNotice(null)
      try {
        const outcome = await writeDepositPolicy(gate.current, salonId, payload)
        if (outcome.status === 'busy') {
          return
        }
        if (outcome.status !== 'saved') {
          setFormError(outcome.message)
          return
        }
        setNotice(
          payload.enabled
            ? 'Política guardada. La página pública ya muestra la seña de cada servicio.'
            : 'Política guardada. La seña quedó apagada: la reserva pública es la de siempre.',
        )
        reload()
      } finally {
        setSaving(false)
      }
    })()
  }

  const handleLoadMore = (): void => {
    if (nextCursor === null || moreBusy.current) {
      return
    }
    moreBusy.current = true
    const mine = generation.current
    setLoadingMore(true)
    setMoreError(null)
    fetchDeposits(salonId, { status, before: nextCursor })
      .then((page) => {
        if (generation.current === mine) {
          setDeposits((current) => mergeDepositRows(current, page.deposits))
          setNextCursor(page.next_cursor)
        }
      })
      .catch((err: unknown) => {
        if (generation.current === mine) {
          setMoreError(describeApiError(err, 'No pudimos cargar más señas.').message)
        }
      })
      .finally(() => {
        if (generation.current === mine) {
          moreBusy.current = false
          setLoadingMore(false)
        }
      })
  }

  const percent = Number(draft.percent.trim())
  const preview = draft.percent.trim() === '' ? null : depositPreview(percent)

  return (
    <>
      <PageHeader
        eyebrow="Cobros"
        title="Señas"
        description="La clienta paga una parte del servicio al reservar en línea. El cobro pasa por el banco simulado de esta instalación: no se cobra dinero real."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. Las señas solo se consultan y se configuran en línea: lo que ves puede estar
          desactualizado y lo que guardes no queda en cola.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando las señas…' : 'Cargando las señas…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="deposit-policy-title">
          <div className="panel-card__head">
            <h2 id="deposit-policy-title">Política de señas</h2>
            <span className={`pill ${policy?.enabled ? 'pill--on' : 'pill--off'}`}>
              {policy?.enabled ? 'Activada' : 'Apagada'}
            </span>
          </div>
          <DepositPolicyForm
            draft={draft}
            onChange={setDraft}
            readOnly={!canEdit}
            saving={saving}
            offline={!online}
            serverError={formError}
            preview={preview}
            onSubmit={handleSubmit}
          />
        </section>
      ) : null}

      <section className="panel-card" aria-labelledby="deposit-list-title">
        <div className="panel-card__head">
          <h2 id="deposit-list-title">Señas cobradas</h2>
          <div className="field deposit-filter">
            <label htmlFor="deposit-status">Estado</label>
            <select
              id="deposit-status"
              className="select"
              value={status}
              onChange={(event) => setStatus(event.target.value)}
            >
              {DEPOSIT_STATUS_FILTERS.map((option) => (
                <option key={option.value} value={option.value}>
                  {option.label}
                </option>
              ))}
            </select>
          </div>
        </div>

        {/* D13-a: the POS does not discount the deposit by itself. */}
        <p className="notice notice--info deposit-cash-note" role="note">
          {DEPOSIT_CASH_NOTE}
        </p>

        {listError ? <ErrorNotice error={listError} onRetry={reload} /> : null}

        {!listError && deposits.length === 0 && !loading ? (
          <p className="empty-state__text">
            {status === ''
              ? 'Todavía no hay señas cobradas en este salón.'
              : 'No hay señas con ese estado. Probá con «Todas».'}
          </p>
        ) : null}

        {deposits.length > 0 ? <DepositList deposits={deposits} timeZone={timeZone} /> : null}

        {moreError ? (
          <p className="notice notice--error" role="alert">
            {moreError}
          </p>
        ) : null}

        {nextCursor !== null ? (
          <button
            type="button"
            className="btn btn--quiet btn--sm"
            onClick={handleLoadMore}
            disabled={loadingMore}
          >
            {loadingMore ? 'Cargando…' : 'Cargar más'}
          </button>
        ) : null}
      </section>
    </>
  )
}
