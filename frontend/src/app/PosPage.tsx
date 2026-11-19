import { useCallback, useEffect, useState } from 'react'
import { createSale, fetchSales, fetchServices } from '../lib/api'
import { dayRange } from '../lib/datetime'
import { formatCents } from '../lib/money'
import { addItem, type Cart } from '../lib/posCart'
import { SALE_METHODS, type Sale, type SaleCreated, type SaleInput, type Service } from '../types'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { POSPanel } from './components/POSPanel'

export function PosPage() {
  const { salonId } = useSalon()
  const [services, setServices] = useState<Service[]>([])
  const [sales, setSales] = useState<Sale[]>([])
  const [cart, setCart] = useState<Cart>([])
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
    setLoading(true)
    setError(null)
    const { from, to } = dayRange(new Date())
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
        if (active) {
          setError(err instanceof Error ? err.message : 'No pudimos cargar el punto de venta.')
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
  }, [salonId, reloadKey])

  const handleSubmit = async (input: SaleInput): Promise<SaleCreated> => {
    if (!salonId) {
      throw new Error('Falta el salón activo.')
    }
    return createSale(salonId, input)
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

  return (
    <>
      <PageHeader
        eyebrow="Punto de venta"
        title="Cobrar servicios"
        description="El total que ves se calcula en la interfaz; el servidor recalcula y es la autoridad."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Actualizar
          </button>
        }
      />

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando ventas…' : 'Cargando catálogo y ventas…'} />
      ) : null}
      {error ? <ErrorNotice message={error} onRetry={reload} /> : null}

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
              onCartChange={setCart}
              onSubmit={handleSubmit}
              onCompleted={reload}
            />
          </aside>

          <section className="panel-card pos-sales" aria-labelledby="pos-sales-title">
            <div className="panel-card__head">
              <h2 id="pos-sales-title">Ventas de hoy</h2>
              <span className="panel-card__meta">{sales.length} registradas</span>
            </div>
            {sales.length === 0 ? (
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
                    </tr>
                  </thead>
                  <tbody>
                    {sales.map((sale) => (
                      <tr key={sale.id}>
                        <td>{sale.client_name}</td>
                        <td>{SALE_METHODS[sale.method] ?? sale.method}</td>
                        <td>{sale.items.reduce((count, item) => count + item.qty, 0)}</td>
                        <td className="table__num">{formatCents(sale.total_cents)}</td>
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
