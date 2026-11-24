import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, fetchStaff, type ApiErrorNotice } from '../../lib/api'
import { TIME_ZONE } from '../../lib/datetime'
import { formatCents } from '../../lib/money'
import {
  canRedeemPackage,
  clientPackageStatus,
  createOpAttempt,
  createPackagesGate,
  describePackageError,
  expiryText,
  fetchClientPackages,
  fetchPackageMovements,
  fetchPackages,
  redeemFingerprint,
  sellFingerprint,
  sessionsLabel,
  validateRedeemDraft,
  writePackage,
  type ClientPackage,
  type PackageAdjustPayload,
  type PackageMovement,
  type PackageWrite,
  type PackageWriteOutcome,
  type RedeemDraft,
  type SellPayload,
  type ServicePackage,
} from '../../lib/packages'
import {
  activeRole,
  canAdjustPackages,
  canManagePackages,
  canViewPackages,
} from '../../lib/permissions'
import { fetchSettings } from '../../lib/settings'
import type { StaffMember } from '../../lib/staff'
import { useOffline } from '../OfflineContext'
import { useSalon } from '../SalonContext'
import { ErrorNotice, LoadingState } from './Feedback'
import { PackageAdjustForm } from './PackageAdjustForm'
import { PackageMovements } from './PackageMovements'
import { PackageSellForm } from './PackageSellForm'
import { PackagesNotice } from './PackagesNotice'

type Panel =
  | { kind: 'none' }
  | { kind: 'sell' }
  | { kind: 'redeem'; id: string }
  | { kind: 'adjust'; id: string }

type ClientPackagesSectionProps = {
  clientId: string
  /** Changes when the page reloads the file: the balances are read again too. */
  refreshKey: number
  /** A sale also changes the history of the client: the page reloads it. */
  onSold: () => void
}

const EMPTY_REDEEM: RedeemDraft = { professional_id: '', note: '' }

/**
 * «Paquetes» of the client file (contract F8, D5/D7/D8/D9/D11-b): the balances
 * (sessions left of the total, expiry day, «Vencido»), «Vender paquete»,
 * «Descontar una sesión» with an explicit confirmation and, for owner and admin,
 * «Ajustar saldo» and the movements.
 *
 * Online only: offline every action is disabled with a notice and nothing is
 * queued. After each action the balances are read again from the server (no
 * optimistic update). Every sale and redeem sends a `client_op_id` per attempt and
 * reuses it when the SAME attempt is retried after a network failure.
 */
export function ClientPackagesSection({
  clientId,
  refreshKey,
  onSold,
}: ClientPackagesSectionProps) {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewPackages(role)
  const canAdjust = canAdjustPackages(role)
  const canCreatePackages = canManagePackages(role)

  const [balances, setBalances] = useState<ClientPackage[]>([])
  const [catalog, setCatalog] = useState<ServicePackage[]>([])
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [localKey, setLocalKey] = useState(0)

  const [panel, setPanel] = useState<Panel>({ kind: 'none' })
  const [saving, setSaving] = useState(false)
  const [panelError, setPanelError] = useState<string | null>(null)
  const [redeemDraft, setRedeemDraft] = useState<RedeemDraft>(EMPTY_REDEEM)
  const [redeemFieldError, setRedeemFieldError] = useState<string | null>(null)

  const [movementsFor, setMovementsFor] = useState<string | null>(null)
  const [movements, setMovements] = useState<PackageMovement[]>([])
  const [movementsLoading, setMovementsLoading] = useState(false)
  const [movementsError, setMovementsError] = useState<string | null>(null)

  // Gate and attempts live outside React state: two presses of the same render
  // would both see `saving === false`, and the op id must survive a re-render.
  const gate = useRef(createPackagesGate())
  const sellAttempt = useRef(createOpAttempt())
  const redeemAttempt = useRef(createOpAttempt())

  const reload = useCallback(() => setLocalKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    // The zone of the salon prints the expiry day and the movements; the team and
    // the catalog feed the sale form. Each has its own request: a failure of one
    // never hides the balances.
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
    fetchPackages(salonId)
      .then((list) => {
        if (active) {
          setCatalog(list)
        }
      })
      .catch(() => {
        if (active) {
          setCatalog([])
        }
      })
    fetchClientPackages(salonId, clientId, controller.signal)
      .then((list) => {
        if (active) {
          setBalances(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(describeApiError(err, 'No pudimos cargar los paquetes de la clienta.'))
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

  useEffect(() => {
    if (!salonId || !canAdjust || movementsFor === null) {
      setMovements([])
      return
    }
    let active = true
    const controller = new AbortController()
    setMovementsLoading(true)
    setMovementsError(null)
    fetchPackageMovements(salonId, clientId, movementsFor, controller.signal)
      .then((list) => {
        if (active) {
          setMovements(list)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setMovementsError(describePackageError(err, 'No pudimos cargar los movimientos.').message)
        }
      })
      .finally(() => {
        if (active) {
          setMovementsLoading(false)
        }
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [salonId, canAdjust, clientId, movementsFor, refreshKey, localKey])

  const closePanel = useCallback((): void => {
    // Closing the panel abandons the attempt: the next press is a new operation.
    sellAttempt.current.settle()
    redeemAttempt.current.settle()
    setPanel({ kind: 'none' })
    setPanelError(null)
    setRedeemDraft(EMPTY_REDEEM)
    setRedeemFieldError(null)
  }, [])

  /**
   * One write at a time. The sentence of the server (or the offline / network
   * notice) reaches the open panel verbatim, and the balances are read again from
   * the server whatever the result: there is no optimistic balance.
   */
  const runWrite = async (write: PackageWrite): Promise<PackageWriteOutcome | null> => {
    if (!salonId) {
      return null
    }
    setSaving(true)
    setPanelError(null)
    setNotice(null)
    try {
      const outcome = await writePackage(gate.current, salonId, write)
      if (outcome.status === 'saved') {
        return outcome
      }
      setPanelError(outcome.message)
      return outcome
    } finally {
      setSaving(false)
      reload()
    }
  }

  const handleSell = async (payload: SellPayload): Promise<void> => {
    const clientOpId = sellAttempt.current.idFor(sellFingerprint(clientId, payload))
    const outcome = await runWrite({ kind: 'sell', clientId, payload, clientOpId })
    if (outcome === null || outcome.status === 'busy') {
      return
    }
    if (outcome.status === 'saved') {
      sellAttempt.current.settle()
      setNotice('Paquete vendido. La venta quedó registrada y el saldo ya está en la ficha.')
      closePanel()
      onSold()
    } else if (outcome.status === 'failed' && outcome.definitive) {
      // A 4xx: the server rejected it and stored nothing, so another press is a new
      // attempt. Offline, network errors and 5xx keep the attempt open: the retry
      // reuses its client_op_id.
      sellAttempt.current.settle()
    }
  }

  const handleRedeem = async (pkg: ClientPackage): Promise<void> => {
    const check = validateRedeemDraft(redeemDraft)
    if (!check.ok) {
      setRedeemFieldError(check.message)
      return
    }
    setRedeemFieldError(null)
    const clientOpId = redeemAttempt.current.idFor(redeemFingerprint(clientId, pkg.id, check.payload))
    const outcome = await runWrite({
      kind: 'redeem',
      clientId,
      clientPackageId: pkg.id,
      payload: check.payload,
      clientOpId,
    })
    if (outcome === null || outcome.status === 'busy') {
      return
    }
    if (outcome.status === 'saved') {
      redeemAttempt.current.settle()
      setNotice(`Sesión descontada de «${pkg.name}». El saldo es el que informa el servidor.`)
      closePanel()
    } else if (outcome.status === 'failed' && outcome.definitive) {
      redeemAttempt.current.settle()
    }
  }

  const handleAdjust = async (pkg: ClientPackage, payload: PackageAdjustPayload): Promise<void> => {
    const outcome = await runWrite({
      kind: 'adjust',
      clientId,
      clientPackageId: pkg.id,
      payload,
    })
    if (outcome?.status === 'saved') {
      setNotice(`Saldo de «${pkg.name}» ajustado. El servidor guardó el movimiento.`)
      closePanel()
    }
  }

  if (!salonId) {
    return null
  }

  if (!allowed) {
    return (
      <section className="panel-card" aria-labelledby="client-packages-title">
        <div className="panel-card__head">
          <h2 id="client-packages-title">Paquetes</h2>
          <span className="panel-card__meta">Sesiones prepagas</span>
        </div>
        <PackagesNotice role={role} />
      </section>
    )
  }

  const professionalName = (id: string): string =>
    staff.find((member) => member.id === id)?.name ?? 'Profesional del salón'
  const sellable = catalog.filter((pkg) => pkg.active)
  const balanceOf = (id: string): ClientPackage | null =>
    balances.find((pkg) => pkg.id === id) ?? null
  const redeeming = panel.kind === 'redeem' ? balanceOf(panel.id) : null
  const adjusting = panel.kind === 'adjust' ? balanceOf(panel.id) : null
  const movementsPackage = movementsFor !== null ? balanceOf(movementsFor) : null
  const actionsOff = !online || saving

  return (
    <section className="panel-card" aria-labelledby="client-packages-title">
      <div className="panel-card__head">
        <h2 id="client-packages-title">Paquetes</h2>
        <span className="panel-card__meta">
          {balances.length === 1 ? '1 paquete' : `${balances.length} paquetes`} · días en la zona
          del salón ({timeZone})
        </span>
      </div>

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. Los paquetes solo se venden y se descuentan en línea: no se guarda nada
          para más tarde ni queda en la cola de ventas.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button
          type="button"
          className="btn btn--wine btn--sm"
          disabled={actionsOff}
          onClick={() => {
            closePanel()
            setPanel({ kind: 'sell' })
          }}
        >
          Vender paquete
        </button>
      </div>

      {panel.kind === 'sell' ? (
        <PackageSellForm
          packages={sellable}
          staff={staff}
          canCreatePackages={canCreatePackages}
          saving={saving}
          serverError={panelError}
          onCancel={closePanel}
          onSubmit={(payload) => void handleSell(payload)}
        />
      ) : null}

      {redeeming ? (
        <div className="team-confirm" role="group" aria-label="Confirmar descuento de una sesión">
          <p className="team-confirm__question">
            ¿Descontar UNA sesión de «{redeeming.name}»?
          </p>
          <p className="team-confirm__note">
            Hoy tiene {sessionsLabel(redeeming)}. Se descuenta una sola sesión y queda en los
            movimientos; no es una venta ni cobra dinero.
          </p>
          <div className="field">
            <label htmlFor="package-redeem-professional">Profesional que la atendió (opcional)</label>
            <select
              id="package-redeem-professional"
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
            <label htmlFor="package-redeem-note">Nota (opcional)</label>
            <input
              id="package-redeem-note"
              className="input"
              type="text"
              autoComplete="off"
              value={redeemDraft.note}
              onChange={(event) => setRedeemDraft({ ...redeemDraft, note: event.target.value })}
              aria-invalid={redeemFieldError ? true : undefined}
              aria-describedby={redeemFieldError ? 'package-redeem-note-error' : undefined}
            />
            {redeemFieldError ? (
              <p className="field__error" id="package-redeem-note-error" role="alert">
                {redeemFieldError}
              </p>
            ) : null}
          </div>
          {panelError ? (
            <p className="notice notice--error" role="alert">
              {panelError}
            </p>
          ) : null}
          <div className="team-confirm__buttons">
            <button
              type="button"
              className="btn btn--wine btn--sm"
              disabled={actionsOff}
              onClick={() => void handleRedeem(redeeming)}
            >
              {saving ? 'Descontando…' : 'Sí, descontar una sesión'}
            </button>
            <button type="button" className="btn btn--quiet btn--sm" onClick={closePanel} disabled={saving}>
              Cancelar
            </button>
          </div>
        </div>
      ) : null}

      {canAdjust && adjusting ? (
        <PackageAdjustForm
          key={`adjust-${adjusting.id}`}
          pkg={adjusting}
          saving={saving}
          serverError={panelError}
          onCancel={closePanel}
          onSubmit={(payload) => void handleAdjust(adjusting, payload)}
        />
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando los paquetes…' : 'Cargando los paquetes…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded && balances.length === 0 ? (
        <p className="empty-state">
          Esta clienta todavía no tiene paquetes. Vende el primero con «Vender paquete».
        </p>
      ) : null}

      {balances.length > 0 ? (
        <div className="table-wrap">
          <table className="table table--stack">
            <thead>
              <tr>
                <th scope="col">Paquete</th>
                <th scope="col">Saldo</th>
                <th scope="col">Vencimiento</th>
                <th scope="col">Estado</th>
                <th scope="col">
                  <span className="visually-hidden">Acciones</span>
                </th>
              </tr>
            </thead>
            <tbody>
              {balances.map((pkg) => {
                const status = clientPackageStatus(pkg)
                return (
                  <tr key={pkg.id} data-inactive={!status.on}>
                    <td data-label="Paquete">
                      <span>
                        <strong>{pkg.name}</strong>
                        <span className="table__id">
                          {pkg.service_name === '' ? 'Servicio del paquete' : pkg.service_name} ·
                          pagó {formatCents(pkg.price_cents)}
                        </span>
                      </span>
                    </td>
                    <td data-label="Saldo">
                      <strong>{sessionsLabel(pkg)}</strong>
                    </td>
                    <td data-label="Vencimiento">{expiryText(pkg, timeZone)}</td>
                    <td data-label="Estado">
                      <span className={`pill ${status.on ? 'pill--on' : 'pill--off'}`}>
                        {status.label}
                      </span>
                    </td>
                    <td className="table__actions" data-label="Acciones">
                      <div className="team-confirm__buttons">
                        {canRedeemPackage(pkg) ? (
                          <button
                            type="button"
                            className="btn btn--quiet"
                            disabled={actionsOff}
                            onClick={() => {
                              closePanel()
                              setPanel({ kind: 'redeem', id: pkg.id })
                            }}
                          >
                            Descontar una sesión
                          </button>
                        ) : null}
                        {canAdjust ? (
                          <>
                            <button
                              type="button"
                              className="btn btn--quiet"
                              disabled={actionsOff}
                              onClick={() => {
                                closePanel()
                                setPanel({ kind: 'adjust', id: pkg.id })
                              }}
                            >
                              Ajustar saldo
                            </button>
                            <button
                              type="button"
                              className="btn btn--quiet"
                              onClick={() => setMovementsFor(pkg.id)}
                            >
                              Movimientos
                            </button>
                          </>
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

      {canAdjust && movementsPackage ? (
        <PackageMovements
          pkg={movementsPackage}
          movements={movements}
          loading={movementsLoading}
          error={movementsError}
          timeZone={timeZone}
          professionalName={professionalName}
          onClose={() => setMovementsFor(null)}
        />
      ) : null}

      {!canAdjust ? (
        <p className="field__hint">
          Tu rol vende paquetes y descuenta sesiones. Ajustar un saldo y ver los movimientos queda
          en manos de la dueña o de la administración.
        </p>
      ) : null}
    </section>
  )
}
