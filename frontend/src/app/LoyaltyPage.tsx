import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { describeApiError, fetchServices, type ApiErrorNotice } from '../lib/api'
import { toDateInputValue, TIME_ZONE } from '../lib/datetime'
import {
  createLoyaltyGate,
  draftFromProgram,
  fetchLoyaltyProgram,
  loyaltyServiceOptions,
  writeLoyalty,
  type LoyaltyDraft,
  type LoyaltyProgram,
  type LoyaltyProgramPayload,
} from '../lib/loyalty'
import { activeRole, canEditLoyaltyProgram, canViewLoyalty } from '../lib/permissions'
import { fetchSettings } from '../lib/settings'
import type { Service } from '../types'
import { useOffline } from './OfflineContext'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { LoyaltyNotice } from './components/LoyaltyNotice'
import { LoyaltyProgramForm } from './components/LoyaltyProgramForm'
import { PageHeader } from './components/PageHeader'

const EMPTY_DRAFT: LoyaltyDraft = draftFromProgram(null)

/**
 * `/panel/fidelidad` (contract F9, D4/D9-a): the loyalty program of the salon,
 * «cada N visitas, un servicio de regalo». Owner and admin edit it; reception reads
 * it; a professional gets `LoyaltyNotice` (the client routes answer 403 to them).
 *
 * Online only: offline the write is not sent and nothing is queued. After a write
 * the program is read again from the server and the form shows what it stored; a
 * rejected write keeps what was typed so nobody has to type it twice.
 */
export function LoyaltyPage() {
  const { salonId, salons } = useSalon()
  const { online } = useOffline()
  const role = activeRole(salons, salonId)
  const allowed = canViewLoyalty(role)
  const canEdit = canEditLoyaltyProgram(role)

  const [program, setProgram] = useState<LoyaltyProgram | null>(null)
  const [draft, setDraft] = useState<LoyaltyDraft>(EMPTY_DRAFT)
  const [services, setServices] = useState<Service[]>([])
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  // The gate lives outside React state: two presses of the same render would both
  // see `saving === false` and send two requests.
  const gate = useRef(createLoyaltyGate())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    // Another salon: never keep showing the previous salon's form while this loads.
    setLoaded(false)
    setProgram(null)
    setDraft(EMPTY_DRAFT)
    // The picker and the zone are only help: a failure never hides the program.
    fetchServices(salonId, true)
      .then((list) => {
        if (active) {
          setServices(list)
        }
      })
      .catch(() => {
        if (active) {
          setServices([])
        }
      })
    fetchSettings(salonId)
      .then((settings) => {
        if (active) {
          setTimeZone(settings.timezone)
        }
      })
      .catch(() => {
        // TIME_ZONE stays as the fallback.
      })
    fetchLoyaltyProgram(salonId)
      .then((saved) => {
        if (active) {
          setProgram(saved)
          setDraft(draftFromProgram(saved))
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar el programa de fidelidad.'))
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

  const options = useMemo(
    () =>
      loyaltyServiceOptions(
        services,
        program?.reward_service_id
          ? { id: program.reward_service_id, name: program.reward_service_name ?? '' }
          : null,
      ),
    [services, program],
  )

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Fidelidad" title="Programa de fidelidad" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Fidelidad" title="Programa de fidelidad" />
        <LoyaltyNotice role={role} />
      </>
    )
  }

  const handleSubmit = (payload: LoyaltyProgramPayload): void => {
    void (async () => {
      setSaving(true)
      setFormError(null)
      setNotice(null)
      try {
        const outcome = await writeLoyalty(gate.current, salonId, { kind: 'program', payload })
        if (outcome.status === 'busy') {
          return
        }
        if (outcome.status !== 'saved') {
          setFormError(outcome.message)
          return
        }
        setNotice('Programa guardado. Si está activado, tu página pública ya lo muestra.')
        // No optimistic state: the form shows what the server stored.
        reload()
      } finally {
        setSaving(false)
      }
    })()
  }

  const countingFrom = program?.counting_from
    ? Number.isNaN(Date.parse(program.counting_from))
      ? null
      : toDateInputValue(new Date(program.counting_from), timeZone)
    : null

  return (
    <>
      <PageHeader
        eyebrow="Fidelidad"
        title="Programa de fidelidad"
        description="Cada cierto número de visitas, la clienta gana un servicio de regalo. El premio es un vale: se entrega y se usa desde su ficha, y no es una venta ni mueve dinero."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {!online ? (
        <p className="notice notice--info" role="status">
          Sin conexión. El programa de fidelidad solo se guarda en línea: no se guarda nada para
          más tarde ni queda en cola.
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando el programa…' : 'Cargando el programa…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="loyalty-program-title">
          <div className="panel-card__head">
            <h2 id="loyalty-program-title">Programa del salón</h2>
            <span className={`pill ${program?.enabled ? 'pill--on' : 'pill--off'}`}>
              {program?.enabled ? 'Activado' : 'Apagado'}
            </span>
          </div>
          <LoyaltyProgramForm
            draft={draft}
            onChange={setDraft}
            options={options}
            readOnly={!canEdit}
            saving={saving}
            offline={!online}
            serverError={formError}
            countingFrom={countingFrom}
            onSubmit={handleSubmit}
          />
        </section>
      ) : null}
    </>
  )
}
