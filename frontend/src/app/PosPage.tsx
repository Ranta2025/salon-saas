import { useCallback, useEffect, useState } from 'react'
import {
  createSale,
  describeApiError,
  fetchSales,
  fetchServices,
  fetchStaff,
  NetworkError,
  type ApiErrorNotice,
} from '../lib/api'
import { dayRange } from '../lib/datetime'
import { formatCents } from '../lib/money'
import { newClientOpId, type QueuedOperation } from '../lib/offlineQueue'
import type { StaffMember } from '../lib/staff'
import { addItem, type Cart } from '../lib/posCart'
import {
  SALE_METHODS,
  type Sale,
  type SaleInput,
  type SaleOutcome,
  type Service,
} from '../types'
import { useSalon } from './SalonContext'
import { useOffline } from './OfflineContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { POSPanel } from './components/POSPanel'

function pendingSalePayload(operation: QueuedOperation): SaleInput {
  return operation.payload as SaleInput
}

function pendingSaleTotal(operation: QueuedOperation): number {
  return pendingSalePayload(operation).items.reduce(
    (total, item) => total + item.unit_price_cents * item.qty,
    0,
  )
}

function pendingSaleItems(operation: QueuedOperation): number {
  return pendingSalePayload(operation).items.reduce((count, item) => count + item.qty, 0)
}

export function PosPage() {
  const { salonId } = useSalon()
  const { online, pending, dropped, enqueueSale, syncedTotal } = useOffline()
  const [services, setServices] = useState<Service[]>([])
  const [sales, setSales] = useState<Sale[]>([])
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffError, setStaffError] = useState<string | null>(null)
  const [cart, setCart] = useState<Cart>([])
  const [search, setSearch] = useState('')
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    setStaffError(null)
    const { from, to } = dayRange(new Date())
    // The team comes from its own endpoint: a failure here must not hide the
    // catalogue, so it is reported next to the professional select.
    fetchStaff(salonId)
      .then((list) => {
        if (active) {
          setStaff(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setStaffError(
            err instanceof Error ? err.message : 'No pudimos cargar el equipo del salón.',
          )
        }
      })
    Promise.all([fetchServices(salonId), fetchSales(salonId, from, to)])
      .then(([serviceList, saleList]) => {
        if (!active) {
          return
        }
        setServices(serviceList)
        setSales(saleList)
        setLoaded(true)
      })
      .catch((err: unknown) => {
        if (!active) {
          return
        }
        setError(
          !online
            ? {
                message:
                  'Sin conexión: no pudimos traer las ventas del servidor. Puedes cobrar igual y la venta se sincroniza al volver la red.',
                retryable: true,
              }
            : describeApiError(err, 'No pudimos cargar el punto de venta.'),
        )
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, reloadKey, online])

  // A queued sale reached the server: refresh the confirmed list.
  useEffect(() => {
    if (syncedTotal > 0) {
      reload()
    }
  }, [syncedTotal, reload])

  /**
   * Tries the API first; when the request cannot reach the server the sale is
   * queued locally with a `client_op_id` and shown as "pending sync" until the
   * queue confirms it. The amount is always the server's when it answers.
   */
  const handleSubmit = async (input: SaleInput): Promise<SaleOutcome> => {
    if (!salonId) {
      throw new Error('Falta el salón activo.')
    }
    const clientOpId = newClientOpId()
    const saleInput: SaleInput = { ...input, client_op_id: clientOpId }
    if (!online) {
      enqueueSale(salonId, saleInput)
      return { kind: 'queued', client_op_id: clientOpId }
    }
    try {
      const created = await createSale(salonId, saleInput)
      return { kind: 'confirmed', server: created }
    } catch (err) {
      if (err instanceof NetworkError) {
        enqueueSale(salonId, saleInput)
        return { kind: 'queued', client_op_id: clientOpId }
      }
      throw err
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Punto de venta" title="Cobrar servicios" />
        <SalonRequired />
      </>
    )
  }

  const term = search.trim().toLowerCase()
  // The POS only sells active services, even if the API returned inactive ones.
  const sellable = services.filter((service) => service.active)
  const visibleServices = term
    ? sellable.filter(
        (service) =>
          service.name.toLowerCase().includes(term) ||
          formatCents(service.price_cents).includes(term),
      )
    : sellable

  const pendingSales = pending.filter(
    (operation) => operation.kind === 'sale' && operation.salon_id === salonId,
  )

  return (
    <>
      <PageHeader
        eyebrow="Punto de venta"
        title="Cobrar servicios"
        description="El total que ves se calcula en la interfaz; el servidor recalcula y es la autoridad. Sin conexión, la venta queda pendiente en este dispositivo."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando ventas…' : 'Cargando catálogo y ventas…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <div className="pos-layout">
          <section className="pos-catalog" aria-labelledby="pos-catalog-title">
            <div className="pos-catalog__head">
              <h2 id="pos-catalog-title">Catálogo</h2>
              <div className="field pos-catalog__search">
                <label className="visually-hidden" htmlFor="pos-search">
                  Buscar servicio
                </label>
                <input
                  id="pos-search"
                  className="input"
                  placeholder="Buscar servicio…"
                  value={search}
                  onChange={(event) => setSearch(event.target.value)}
                />
              </div>
            </div>

            {visibleServices.length === 0 ? (
              <p className="muted">No hay servicios activos que coincidan con la búsqueda.</p>
            ) : (
              <ul className="pos-services">
                {visibleServices.map((service) => (
                  <li key={service.id}>
                    <button
                      type="button"
                      className="pos-service"
                      onClick={() => setCart((current) => addItem(current, service))}
                    >
                      <span className="pos-service__name">{service.name}</span>
                      <span className="pos-service__meta">{service.duration_min} min</span>
                      <span className="pos-service__price">{formatCents(service.price_cents)}</span>
                      <span className="pos-service__add" aria-hidden="true">
                        +
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>

          <aside className="pos-checkout" aria-label="Cobro de la venta">
            <POSPanel
              cart={cart}
              staff={staff}
              staffError={staffError}
              onCartChange={setCart}
              onSubmit={handleSubmit}
              onCompleted={reload}
              pendingCount={pendingSales.length}
              dropped={dropped}
            />
          </aside>

          <section className="panel-card pos-sales" aria-labelledby="pos-sales-title">
            <div className="panel-card__head">
              <h2 id="pos-sales-title">Ventas de hoy</h2>
              <span className="panel-card__meta">
                {sales.length} confirmadas · {pendingSales.length} por sincronizar
              </span>
            </div>
            {sales.length === 0 && pendingSales.length === 0 ? (
              <p className="muted">Todavía no hay ventas hoy.</p>
            ) : (
              <div className="table-wrap">
                <table className="table">
                  <thead>
                    <tr>
                      <th scope="col">Cliente</th>
                      <th scope="col">Método</th>
                      <th scope="col">Artículos</th>
                      <th scope="col" className="table__num">
                        Total
                      </th>
                      <th scope="col">Estado</th>
                    </tr>
                  </thead>
                  <tbody>
                    {pendingSales.map((operation) => {
                      const payload = pendingSalePayload(operation)
                      return (
                        <tr key={operation.client_op_id} data-pending="true">
                          <td>
                            {payload.client_name}{' '}
                            <span className="table__id">
                              {operation.client_op_id.slice(0, 8)}
                            </span>
                          </td>
                          <td>{SALE_METHODS[payload.method] ?? payload.method}</td>
                          <td>{pendingSaleItems(operation)}</td>
                          <td className="table__num">{formatCents(pendingSaleTotal(operation))}</td>
                          <td>
                            <span className="pill pill--pending">Pendiente de sincronizar</span>
                          </td>
                        </tr>
                      )
                    })}
                    {sales.map((sale) => (
                      <tr key={sale.id}>
                        <td>{sale.client_name}</td>
                        <td>{SALE_METHODS[sale.method] ?? sale.method}</td>
                        <td>{sale.items.reduce((count, item) => count + item.qty, 0)}</td>
                        <td className="table__num">{formatCents(sale.total_cents)}</td>
                        <td>
                          <span className="pill pill--on">Registrada</span>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </section>
        </div>
      ) : null}
    </>
  )
}
