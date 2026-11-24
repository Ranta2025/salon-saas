import { useCallback, useEffect, useState } from 'react'
import {
  createSale,
  describeApiError,
  fetchClients,
  fetchSales,
  fetchServices,
  fetchStaff,
  NetworkError,
  type ApiErrorNotice,
} from '../lib/api'
import type { PickableClient } from '../lib/clientRecord'
import { dayRange } from '../lib/datetime'
import { formatCents } from '../lib/money'
import { newClientOpId, type QueuedOperation } from '../lib/offlineQueue'
import type { StaffMember } from '../lib/staff'
import { addItem, type Cart } from '../lib/posCart'
import {
  addProduct,
  canAddProduct,
  emptyProductCart,
  sellableProducts,
  type ProductCart,
} from '../lib/productCart'
import { fetchProducts, type Product } from '../lib/products'
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

/** Product units of a queued sale (contract F6: `products: [{product_id, qty}]`). */
function pendingSaleProducts(operation: QueuedOperation): number {
  return (pendingSalePayload(operation).products ?? []).reduce(
    (count, line) => count + line.qty,
    0,
  )
}

/**
 * A queued sale's payload never carries a product price (the catalogue price is the
 * server's), so the row can only sum the services and says the products are there.
 */
function pendingSaleHasProducts(operation: QueuedOperation): boolean {
  return (pendingSalePayload(operation).products ?? []).length > 0
}

export function PosPage() {
  const { salonId } = useSalon()
  const { online, pending, dropped, enqueueSale, syncedTotal } = useOffline()
  const [services, setServices] = useState<Service[]>([])
  const [sales, setSales] = useState<Sale[]>([])
  const [staff, setStaff] = useState<StaffMember[]>([])
  const [staffError, setStaffError] = useState<string | null>(null)
  // F5: the optional client picker of the checkout. Its own request, so a failure
  // here never hides the catalogue nor blocks a free-text sale.
  const [clients, setClients] = useState<PickableClient[]>([])
  const [clientsError, setClientsError] = useState<string | null>(null)
  const [clientsLoading, setClientsLoading] = useState(true)
  const [cart, setCart] = useState<Cart>([])
  // F6: products of the inventory, with their own request. A failure here must not
  // hide the catalogue, the sales nor the checkout of services.
  const [products, setProducts] = useState<Product[]>([])
  const [productsError, setProductsError] = useState<string | null>(null)
  const [productCart, setProductCart] = useState<ProductCart>(emptyProductCart())
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
    setClientsError(null)
    setProductsError(null)
    setClientsLoading(true)
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
    fetchClients(salonId)
      .then((list) => {
        if (active) {
          setClients(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setClientsError(
            err instanceof Error ? err.message : 'No pudimos cargar las fichas de clientas.',
          )
        }
      })
      .finally(() => {
        if (active) {
          setClientsLoading(false)
        }
      })
    fetchProducts(salonId)
      .then((list) => {
        if (active) {
          setProducts(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setProductsError(
            err instanceof Error ? err.message : 'No pudimos cargar los productos.',
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

  // The products section lists only active products, with the stock the server
  // reported. `sellable` keeps its name for the services of the catalogue.
  const sellableProductList = sellableProducts(products)

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

            {/* F6: only active products, with the stock the server reported. The
                button stops at that stock; the server still decides the sale. */}
            <div className="pos-catalog__head">
              <h2 id="pos-products-title">Productos</h2>
            </div>
            {productsError ? (
              <p className="notice notice--error" role="alert">
                No pudimos cargar los productos: {productsError}
              </p>
            ) : null}
            {!productsError && sellableProductList.length === 0 ? (
              <p className="muted">No hay productos cargados en el inventario.</p>
            ) : null}
            {sellableProductList.length > 0 ? (
              <ul className="pos-services" aria-labelledby="pos-products-title">
                {sellableProductList.map((product) => {
                  const canAdd = canAddProduct(productCart, product)
                  return (
                    <li key={product.id}>
                      <button
                        type="button"
                        className="pos-service"
                        disabled={!canAdd}
                        // The page styles `.btn:disabled`; a row of the catalogue
                        // says the same thing with the same opacity.
                        style={canAdd ? undefined : { opacity: 0.55, cursor: 'not-allowed' }}
                        aria-label={`Agregar ${product.name} a la venta`}
                        onClick={() => setProductCart((current) => addProduct(current, product))}
                      >
                        <span className="pos-service__name">{product.name}</span>
                        <span className="pos-service__meta">
                          {product.stock === 0 ? 'Sin stock' : `${product.stock} en stock`}
                        </span>
                        <span className="pos-service__price">
                          {formatCents(product.price_cents)}
                        </span>
                        <span className="pos-service__add" aria-hidden="true">
                          +
                        </span>
                      </button>
                    </li>
                  )
                })}
              </ul>
            ) : null}
          </section>

          <aside className="pos-checkout" aria-label="Cobro de la venta">
            <POSPanel
              cart={cart}
              productCart={productCart}
              products={sellableProductList}
              onProductCartChange={setProductCart}
              onStockConflict={reload}
              staff={staff}
              staffError={staffError}
              clients={clients}
              clientsError={clientsError}
              clientsLoading={clientsLoading}
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
                          <td>{pendingSaleItems(operation) + pendingSaleProducts(operation)}</td>
                          <td className="table__num">
                            {formatCents(pendingSaleTotal(operation))}
                            {pendingSaleHasProducts(operation) ? (
                              <span className="muted"> + productos</span>
                            ) : null}
                          </td>
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
