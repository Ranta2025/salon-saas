import { useCallback, useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { describeApiError, fetchStaff, type ApiErrorNotice } from '@/lib/api'
import { TIME_ZONE } from '@/lib/datetime'
import {
  canGrantReward,
  canUseReward,
  createLoyaltyGate,
  createOpAttempt,
  fetchClientLoyalty,
  grantFingerprint,
  loyaltyProgress,
  nextInLabel,
  redeemFingerprint,
  rewardDateText,
  rewardGrantedDay,
  rewardsAvailableLabel,
  rewardStateLabel,
  validateRedeemDraft,
  writeLoyalty,
  type ClientLoyalty,
  type LoyaltyReward,
  type LoyaltyWrite,
  type LoyaltyWriteOutcome,
  type RedeemRewardDraft,
} from '../lib/loyalty'
import { activeRole, canEditLoyaltyProgram, canViewLoyalty } from '@/lib/permissions'
import { fetchSettings } from '@/features/settings/lib/settings'
import type { StaffMember } from '@/lib/staff'
import { useOffline } from '@/app/OfflineContext'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState } from '@/app/components/Feedback'
import { LoyaltyNotice } from './LoyaltyNotice'

type ClientLoyaltySectionProps = {
  clientId: string
  /** Changes when the page reloads the file: the loyalty status is read again too. */
  refreshKey: number
}

const EMPTY_REDEEM: RedeemRewardDraft = { professional_id: '', note: '' }

/**
 * «Fidelidad» of the client file (contract F9, D5-D7/D9-b): progress «x de N
 * visitas» with its bar, rewards available, «Entregar premio» (disabled with none
 * available), the list of vouchers with their state and expiry, and «Usar premio»
 * with an explicit confirmation (optional professional and note).
 *
 * Online only: offline every action is disabled with a notice and nothing is
 * queued. After each action the status is read again from the server (no optimistic
 * update). Every grant and redeem sends a `client_op_id` per attempt and reuses it
 * when the SAME attempt is retried after a network failure.
 */
export function ClientLoyaltySection({ clientId, refreshKey }: ClientLoyaltySectionProps) {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewLoyalty(role)
  const canEditProgram = canEditLoyaltyProgram(role)

  const [status, setStatus] = useState<ClientLoyalty | null>(null)
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [localKey, setLocalKey] = useState(0)

  const [saving, setSaving] = useState(false)
  const [grantError, setGrantError] = useState<string | null>(null)
  const [redeemId, setRedeemId] = useState<string | null>(null)
  const [redeemError, setRedeemError] = useState<string | null>(null)
  const [redeemDraft, setRedeemDraft] = useState<RedeemRewardDraft>(EMPTY_REDEEM)
  const [redeemFieldError, setRedeemFieldError] = useState<string | null>(null)

  // Gate and attempts live outside React state: two presses of the same render would
  // both see `saving === false`, and the op id must survive a re-render.
  const gate = useRef(createLoyaltyGate())
  const grantAttempt = useRef(createOpAttempt())
  const redeemAttempt = useRef(createOpAttempt())

  const reload = useCallback(() => setLocalKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    // The zone prints the days and the team feeds the redeem form. Each has its own
    // request: a failure of one never hides the status.
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback.
      })
    fetchStaff(salonId)
      .then((list) => {
        if (active) {
          setStaff(list)
        }
      })
      .catch(() => {
        if (active) {
          setStaff([])
        }
      })
    return () => {
      active = false
    }
  }, [salonId, allowed])

  useEffect(() => {
    if (!salonId || !allowed || clientId === '') {
      return
    }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    fetchClientLoyalty(salonId, clientId, controller.signal)
      .then((loadedStatus) => {
        if (!active) {
          return
        }
        if (loadedStatus === null) {
          setError({ message: 'La respuesta de fidelidad no es válida.', retryable: true })
          return
        }
        setStatus(loadedStatus)
        setLoaded(true)
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(describeApiError(err, 'No pudimos cargar la fidelidad de la clienta.'))
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
  }, [salonId, allowed, clientId, refreshKey, localKey])

  const closeRedeem = useCallback((): void => {
    // Closing the confirmation abandons the attempt: the next press is a new operation.
    redeemAttempt.current.settle()
    setRedeemId(null)
    setRedeemError(null)
    setRedeemDraft(EMPTY_REDEEM)
    setRedeemFieldError(null)
  }, [])

  /**
   * One write at a time. The sentence of the server (or the offline / network
   * notice) reaches the screen verbatim, and the status is read again from the
   * server whatever the result: there is no optimistic update.
   */
  const runWrite = async (
    write: LoyaltyWrite,
    showError: (message: string | null) => void,
  ): Promise<LoyaltyWriteOutcome | null> => {
    if (!salonId) {
      return null
    }
    setSaving(true)
    setGrantError(null)
    setRedeemError(null)
    setNotice(null)
    try {
      const outcome = await writeLoyalty(gate.current, salonId, write)
      if (outcome.status !== 'saved') {
        showError(outcome.message)
      }
      return outcome
    } finally {
      setSaving(false)
      reload()
    }
  }

  const handleGrant = async (current: ClientLoyalty): Promise<void> => {
    const clientOpId = grantAttempt.current.idFor(grantFingerprint(clientId, current.rewards_granted))
    const outcome = await runWrite({ kind: 'grant', clientId, clientOpId }, setGrantError)
    if (outcome === null || outcome.status === 'busy') {
      return
    }
    if (outcome.status === 'saved') {
      grantAttempt.current.settle()
      setNotice(
        outcome.reward
          ? `Premio entregado: ${outcome.reward.service_name} (premio n.º ${outcome.reward.reward_number}). La lista es la que informa el servidor.`
          : 'Premio entregado. La lista es la que informa el servidor.',
      )
    } else if (outcome.status === 'failed' && outcome.definitive) {
      // A 4xx: the server rejected it and stored nothing, so another press is a new
      // attempt. Offline, network errors and 5xx keep the attempt open: the retry
      // reuses its client_op_id.
      grantAttempt.current.settle()
    }
  }

  const handleRedeem = async (reward: LoyaltyReward): Promise<void> => {
    const check = validateRedeemDraft(redeemDraft)
    if (!check.ok) {
      setRedeemFieldError(check.message)
      return
    }
    setRedeemFieldError(null)
    const clientOpId = redeemAttempt.current.idFor(redeemFingerprint(clientId, reward.id, check.payload))
    const outcome = await runWrite(
      { kind: 'redeem', clientId, rewardId: reward.id, payload: check.payload, clientOpId },
      setRedeemError,
    )
    if (outcome === null || outcome.status === 'busy') {
      return
    }
    if (outcome.status === 'saved') {
      redeemAttempt.current.settle()
      setNotice(
        `Premio usado: ${reward.service_name}. Quedó registrado; no es una venta ni cobra dinero.`,
      )
      closeRedeem()
    } else if (outcome.status === 'failed' && outcome.definitive) {
      redeemAttempt.current.settle()
    }
  }

  if (!salonId) {
    return null
  }

  if (!allowed) {
    return (
      <section className="panel-card" aria-labelledby="client-loyalty-title">
        <div className="panel-card__head">
          <h2 id="client-loyalty-title">Fidelidad</h2>
          <span className="panel-card__meta">Visitas y premios</span>
        </div>
        <LoyaltyNotice role={role} />
      </section>
    )
  }

  const progress = status ? loyaltyProgress(status) : null
  const missing = status ? nextInLabel(status.next_in) : null
  const redeeming = status?.rewards.find((reward) => reward.id === redeemId) ?? null
  const actionsOff = !online || saving || loading
  const grantable = status ? canGrantReward(status) : false

  return (
    <section className="panel-card" aria-labelledby="client-loyalty-title">
      <div className="panel-card__head">
        <h2 id="client-loyalty-title">Fidelidad</h2>
        <span className="panel-card__meta">
          Visitas y premios · días en la zona del salón ({timeZone})
        </span>
      </div>

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. Los premios solo se entregan y se usan en línea: no se guarda nada para
          más tarde ni queda en cola.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando la fidelidad…' : 'Cargando la fidelidad…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {status && !status.enabled ? (
        <p className="notice notice--info" role="status">
          La fidelidad no está activada en este salón: no se entregan ni se usan premios, pero los
          ya entregados se conservan.{' '}
          {canEditProgram ? <Link to="/panel/fidelidad">Activala en «Fidelidad»</Link> : null}
        </p>
      ) : null}

      {status && status.enabled ? (
        <div className="loyalty-status">
          {progress ? (
            <div className="loyalty-progress">
              <p className="loyalty-progress__label">
                <strong>{progress.label}</strong>
                {missing ? <span className="loyalty-progress__missing">{missing}</span> : null}
              </p>
              <div
                className="loyalty-progress__bar"
                role="progressbar"
                aria-label="Visitas hacia el próximo premio"
                aria-valuemin={0}
                aria-valuemax={progress.required}
                aria-valuenow={progress.inCycle}
                aria-valuetext={progress.label}
              >
                <span className="loyalty-progress__fill" style={{ width: `${progress.percent}%` }} />
              </div>
            </div>
          ) : null}

          <dl className="loyalty-stats">
            <div>
              <dt>Visitas en total</dt>
              <dd>{status.visits}</dd>
            </div>
            <div>
              <dt>Premios ganados</dt>
              <dd>{status.rewards_earned}</dd>
            </div>
            <div>
              <dt>Para entregar</dt>
              <dd>{rewardsAvailableLabel(status.rewards_available)}</dd>
            </div>
          </dl>
        </div>
      ) : null}

      {status ? (
        <div className="loyalty-actions">
          <button
            type="button"
            className="btn btn--wine btn--sm"
            disabled={actionsOff || !grantable}
            onClick={() => void handleGrant(status)}
          >
            {saving && !redeemId ? 'Entregando…' : 'Entregar premio'}
          </button>
          {!grantable && status.enabled ? (
            <span className="field__hint">No hay premios disponibles para entregar.</span>
          ) : null}
        </div>
      ) : null}
      {grantError ? (
        <p className="notice notice--error" role="alert">
          {grantError}
        </p>
      ) : null}

      {redeeming ? (
        <div className="team-confirm" role="group" aria-label="Confirmar el uso de un premio">
          <p className="team-confirm__question">
            ¿Usar el premio «{redeeming.service_name}» (n.º {redeeming.reward_number})?
          </p>
          <p className="team-confirm__note">
            Se marca como usado y queda registrado: no se puede deshacer. No es una venta ni cobra
            dinero.
          </p>
          <div className="field">
            <label htmlFor="loyalty-redeem-professional">Profesional que lo atendió (opcional)</label>
            <select
              id="loyalty-redeem-professional"
              className="select"
              value={redeemDraft.professional_id}
              onChange={(event) =>
                setRedeemDraft({ ...redeemDraft, professional_id: event.target.value })
              }
            >
              <option value="">Sin indicar</option>
              {staff.map((member) => (
                <option key={member.id} value={member.id}>
                  {member.name}
                </option>
              ))}
            </select>
          </div>
          <div className="field">
            <label htmlFor="loyalty-redeem-note">Nota (opcional)</label>
            <input
              id="loyalty-redeem-note"
              className="input"
              type="text"
              autoComplete="off"
              value={redeemDraft.note}
              onChange={(event) => setRedeemDraft({ ...redeemDraft, note: event.target.value })}
              aria-invalid={redeemFieldError ? true : undefined}
              aria-describedby={redeemFieldError ? 'loyalty-redeem-note-error' : undefined}
            />
            {redeemFieldError ? (
              <p className="field__error" id="loyalty-redeem-note-error" role="alert">
                {redeemFieldError}
              </p>
            ) : null}
          </div>
          {redeemError ? (
            <p className="notice notice--error" role="alert">
              {redeemError}
            </p>
          ) : null}
          <div className="team-confirm__buttons">
            <button
              type="button"
              className="btn btn--wine btn--sm"
              disabled={actionsOff}
              onClick={() => void handleRedeem(redeeming)}
            >
              {saving ? 'Usando…' : 'Sí, usar el premio'}
            </button>
            <button type="button" className="btn btn--quiet btn--sm" onClick={closeRedeem} disabled={saving}>
              Cancelar
            </button>
          </div>
        </div>
      ) : null}

      {loaded && status && status.rewards.length === 0 ? (
        <p className="empty-state">Todavía no se entregó ningún premio a esta clienta.</p>
      ) : null}

      {status && status.rewards.length > 0 ? (
        <div className="table-wrap">
          <table className="table table--stack">
            <thead>
              <tr>
                <th scope="col">Premio</th>
                <th scope="col">Entregado</th>
                <th scope="col">Vencimiento</th>
                <th scope="col">Estado</th>
                <th scope="col">
                  <span className="visually-hidden">Acciones</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {status.rewards.map((reward) => {
                const state = rewardStateLabel(reward.state)
                return (
                  <tr key={reward.id} data-inactive={!state.on}>
                    <td data-label="Premio">
                      <span>
                        <strong>{reward.service_name === '' ? 'Servicio de regalo' : reward.service_name}</strong>
                        <span className="table__id">Premio n.º {reward.reward_number}</span>
                      </span>
                    </td>
                    <td data-label="Entregado">
                      {rewardGrantedDay(reward, timeZone)}
                    </td>
                    <td data-label="Vencimiento">{rewardDateText(reward, timeZone)}</td>
                    <td data-label="Estado">
                      <span className={`pill ${state.on ? 'pill--on' : 'pill--off'}`}>{state.label}</span>
                    </td>
                    <td className="table__actions" data-label="Acciones">
                      <div className="team-confirm__buttons">
                        {canUseReward(status, reward) ? (
                          <button
                            type="button"
                            className="btn btn--quiet"
                            disabled={actionsOff}
                            onClick={() => {
                              closeRedeem()
                              setGrantError(null)
                              setRedeemId(reward.id)
                            }}
                          >
                            Usar premio
                          </button>
                        ) : null}
                      </div>
                    </td>
                  </tr>
                )
              })}
            </tbody>
          </table>
        </div>
      ) : null}
    </section>
  )
}
