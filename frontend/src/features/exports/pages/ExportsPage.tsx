import { useEffect, useRef, useState } from 'react'
import { TIME_ZONE, todayInTimeZone } from '@/lib/datetime'
import {
  canExportCsv,
  checkExportRange,
  CLIENTS_PRIVACY_NOTICE,
  createExportGate,
  defaultExportRange,
  EXPORT_KIND_LABELS,
  exportRangeHint,
  loadExportContext,
  runExport,
  type ExportKind,
  type ExportRange,
  type ExportRequest,
} from '../lib/exports'
import { activeRole } from '@/lib/permissions'
import { useSalon } from '@/app/SalonContext'
import { SalonRequired } from '@/app/components/Feedback'
import { ManagerOnlyNotice } from '@/app/components/ManagerOnlyNotice'
import { PageHeader } from '@/app/components/PageHeader'

/** One-line description of each file, with the columns of the frozen contract. */
const CARD_COPY: Record<ExportKind, { title: string; description: string }> = {
  sales: {
    title: EXPORT_KIND_LABELS.sales,
    description:
      'Una fila por venta con fecha, profesional, cliente, método de pago, ítems y total. Sin teléfono ni email de la clienta.',
  },
  'cash-closes': {
    title: EXPORT_KIND_LABELS['cash-closes'],
    description:
      'Los cierres de caja de la ventana, con el efectivo del sistema, el contado, la diferencia (con su signo) y la nota.',
  },
  clients: {
    title: EXPORT_KIND_LABELS.clients,
    description: 'Nombre, teléfono, email, notas y alta de cada clienta, ordenados por nombre.',
  },
}

const DOWNLOAD_ACTION = 'Descargar CSV'

/**
 * `/panel/exportar` (contract F4): the three CSV exports of the salon. Only the
 * owner and the administration reach it; everyone else sees the same notice as
 * Nómina, Gastos and Ganancias.
 *
 * Each file is downloaded with the bearer token and handed to the browser as a
 * Blob: the CSV is never parsed nor rendered here. The default range is the last
 * 30 days ending today in the zone of the SALON (`GET /settings`), and an
 * unusable range is refused before any request with the sentence of the API.
 */
export function ExportsPage() {
  const { salonId, salons, loading: meLoading, refresh: refreshMe } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canExportCsv(role)

  // Zone of the active salon; the known one is the fallback while it loads.
  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const today = todayInTimeZone(timeZone)

  const [range, setRange] = useState<ExportRange>(() =>
    defaultExportRange(todayInTimeZone(TIME_ZONE)),
  )
  // Once the person picks a day, a late settings answer never moves the range.
  const rangeTouched = useRef(false)

  // Synchronous gate: two presses of the same render cannot both send a request.
  const gate = useRef(createExportGate())
  const [pending, setPending] = useState<ExportKind | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [saved, setSaved] = useState<string | null>(null)

  // The zone and today come from the settings of the salon, never from a fixed one.
  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    loadExportContext(salonId)
      .then((context) => {
        if (!active) {
          return
        }
        setTimeZone(context.timeZone)
        if (!rangeTouched.current) {
          setRange(context.range)
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
    if (!rangeTouched.current) {
      setRange(defaultExportRange(today))
    }
  }, [today])

  const rangeCheck = checkExportRange(range.from, range.to)

  const handleRangeChange = (patch: Partial<ExportRange>): void => {
    rangeTouched.current = true
    setRange((current) => ({ ...current, ...patch }))
    setSaved(null)
    setError(null)
  }

  const handleResetRange = (): void => {
    rangeTouched.current = false
    setRange(defaultExportRange(today))
    setSaved(null)
    setError(null)
  }

  const handleExport = async (request: ExportRequest): Promise<void> => {
    if (!salonId) {
      return
    }
    if (request.kind !== 'clients' && !rangeCheck.ok) {
      setError(rangeCheck.message)
      setSaved(null)
      return
    }
    // A press while another file is downloading never reaches the API.
    if (gate.current.busy() !== null) {
      return
    }
    setPending(request.kind)
    setError(null)
    setSaved(null)
    const outcome = await runExport(gate.current, salonId, request)
    setPending(null)
    if (outcome.status === 'done') {
      setSaved(outcome.filename)
    } else if (outcome.status === 'failed') {
      setError(outcome.message)
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Exportar" title="Exportar datos del salón" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Exportar" title="Exportar datos del salón" />
        <ManagerOnlyNotice
          role={role}
          what="las exportaciones de datos del salón"
          loading={meLoading}
          onRetry={refreshMe}
        />
      </>
    )
  }

  const rangeKinds: readonly ExportKind[] = ['sales', 'cash-closes']
  // Why the two range exports are blocked, or `null` when the range is usable.
  const rangeBlockReason = rangeCheck.ok ? null : rangeCheck.message

  return (
    <>
      <PageHeader
        eyebrow="Exportar"
        title="Exportar datos del salón"
        description="Descargá las ventas, los cierres de caja o las clientas en un archivo CSV. El archivo se arma con los datos del salón y se guarda en tu computadora: acá no se muestra su contenido."
      />

      <section className="panel-card" aria-labelledby="export-range-title">
        <div className="panel-card__head">
          <h2 id="export-range-title">Rango de fechas</h2>
          <span className="panel-card__meta">Ventas y cierres de caja · hasta 366 días</span>
        </div>
        <div className="filters">
          <div className="field">
            <label htmlFor="export-from">Desde</label>
            <input
              id="export-from"
              className="input"
              type="date"
              value={range.from}
              onChange={(event) => handleRangeChange({ from: event.target.value })}
              aria-describedby="export-range-hint"
            />
          </div>
          <div className="field">
            <label htmlFor="export-to">Hasta</label>
            <input
              id="export-to"
              className="input"
              type="date"
              value={range.to}
              onChange={(event) => handleRangeChange({ to: event.target.value })}
              aria-describedby="export-range-hint"
            />
          </div>
          <button type="button" className="btn btn--quiet" onClick={handleResetRange}>
            Últimos 30 días
          </button>
        </div>
        {rangeCheck.ok ? (
          <p className="field__hint" id="export-range-hint">
            {exportRangeHint(range, today)}
          </p>
        ) : (
          <p className="field__error" id="export-range-hint" role="alert">
            {rangeCheck.message}
          </p>
        )}
      </section>

      {error ? (
        <div className="notice notice--error" role="alert">
          <p>{error}</p>
        </div>
      ) : null}
      {saved ? (
        <p className="notice notice--success" role="status">
          Guardado como {saved}
        </p>
      ) : null}

      {rangeKinds.map((kind) => {
        const request: ExportRequest =
          kind === 'sales' ? { kind: 'sales', range } : { kind: 'cash-closes', range }
        return (
          <section className="panel-card" key={kind} aria-labelledby={`export-${kind}-title`}>
            <div className="panel-card__head">
              <h2 id={`export-${kind}-title`}>{CARD_COPY[kind].title}</h2>
              <span className="panel-card__meta">
                {range.from} a {range.to}
              </span>
            </div>
            <p className="field__hint">{CARD_COPY[kind].description}</p>
            <div className="filters">
              <button
                type="button"
                className="btn btn--wine"
                onClick={() => void handleExport(request)}
                disabled={pending !== null || rangeBlockReason !== null}
                aria-describedby={rangeBlockReason === null ? undefined : 'export-range-hint'}
              >
                {pending === kind
                  ? 'Descargando…'
                  : `${DOWNLOAD_ACTION} de ${CARD_COPY[kind].title.toLowerCase()}`}
              </button>
            </div>
          </section>
        )
      })}

      <section className="panel-card" aria-labelledby="export-clients-title">
        <div className="panel-card__head">
          <h2 id="export-clients-title">{CARD_COPY.clients.title}</h2>
          <span className="panel-card__meta">Sin rango: todas las clientas del salón</span>
        </div>
        <p className="field__hint">{CARD_COPY.clients.description}</p>
        <p className="notice notice--info">{CLIENTS_PRIVACY_NOTICE}</p>
        <div className="filters">
          <button
            type="button"
            className="btn btn--wine"
            onClick={() => void handleExport({ kind: 'clients' })}
            disabled={pending !== null}
          >
            {pending === 'clients' ? 'Descargando…' : 'Descargar clientas'}
          </button>
        </div>
      </section>
    </>
  )
}
