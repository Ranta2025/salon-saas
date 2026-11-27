import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { TIME_ZONE } from '@/lib/datetime'
import { formatCents } from '@/lib/money'
import { activeRole, canManageInventory, canViewInventory } from '@/lib/permissions'
import {
  createInventoryGate,
  fetchProductMovements,
  fetchProducts,
  LOW_STOCK_LABEL,
  lowStockMark,
  MOVEMENTS_DEFAULT_LIMIT,
  writeProduct,
  type AdjustPayload,
  type Product,
  type ProductPayload,
  type ProductUpdatePayload,
  type StockMovement,
} from '../lib/products'
import { fetchSettings } from '@/features/settings/lib/settings'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { ManagerOnlyNotice } from '@/app/components/ManagerOnlyNotice'
import { PageHeader } from '@/app/components/PageHeader'
import { ProductAdjustForm } from '../components/ProductAdjustForm'
import { ProductForm } from '../components/ProductForm'
import { ProductMovements } from '../components/ProductMovements'

type FormMode = { kind: 'closed' } | { kind: 'create' } | { kind: 'edit'; product: Product }

/**
 * `/panel/inventario` (contract F6): the products of the salon with their price, the
 * stock the SERVER reported and the «Stock bajo» mark of its `low` flag.
 *
 * Owner and admin create, edit and adjust the stock and read the movements; reception
 * only reads the list; a professional gets `ManagerOnlyNotice` (the API answers 403
 * to all of them, this page only avoids offering a button that would fail).
 *
 * No optimistic stock: after every write the list is read again from the server, so
 * the units on screen are never the ones the client guessed.
 */
export function InventoryPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canViewInventory(role)
  const canManage = canManageInventory(role)

  const [timeZone, setTimeZone] = useState(TIME_ZONE)
  const [products, setProducts] = useState<Product[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [mode, setMode] = useState<FormMode>({ kind: 'closed' })
  const [adjusting, setAdjusting] = useState<Product | null>(null)
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [formVersion, setFormVersion] = useState(0)

  const [movementsFor, setMovementsFor] = useState<Product | null>(null)
  const [movements, setMovements] = useState<StockMovement[]>([])
  const [movementsLoading, setMovementsLoading] = useState(false)
  const [movementsError, setMovementsError] = useState<string | null>(null)
  const [movementsLimit, setMovementsLimit] = useState(MOVEMENTS_DEFAULT_LIMIT)

  // The gate lives outside React state: two presses of the same render would both
  // see `saving === false` and send two requests.
  const gate = useRef(createInventoryGate())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  // The zone of the salon is the one that prints the instants of the movements.
  useEffect(() => {
    if (!salonId) {
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
  }, [salonId])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchProducts(salonId)
      .then((list) => {
        if (active) {
          setProducts(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar los productos del salón.'))
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

  useEffect(() => {
    if (!salonId || !canManage || !movementsFor) {
      setMovements([])
      return
    }
    let active = true
    setMovementsLoading(true)
    setMovementsError(null)
    fetchProductMovements(salonId, movementsFor.id, movementsLimit)
      .then((list) => {
        if (active) {
          setMovements(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setMovementsError(
            describeApiError(err, 'No pudimos cargar los movimientos del producto.').message,
          )
        }
      })
      .finally(() => {
        if (active) {
          setMovementsLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, canManage, movementsFor, movementsLimit, reloadKey])

  /**
   * One write at a time: the gate refuses the second press, the sentence of the
   * server reaches the form verbatim and the list is read again from the server.
   */
  const runWrite = async (
    write:
      | { kind: 'create'; payload: ProductPayload }
      | { kind: 'update'; productId: string; payload: ProductUpdatePayload }
      | { kind: 'adjust'; productId: string; payload: AdjustPayload },
    successCopy: string,
  ): Promise<boolean> => {
    if (!salonId) {
      return false
    }
    setSaving(true)
    setFormError(null)
    setNotice(null)
    try {
      const outcome = await writeProduct(gate.current, salonId, write)
      if (outcome.status === 'saved') {
        setNotice(successCopy)
        return true
      }
      setFormError(outcome.message)
      return false
    } finally {
      setSaving(false)
      // No optimistic stock: even a rejected write re-reads the server, which is the
      // only source of the stock and of the low-stock mark.
      reload()
    }
  }

  const handleCreate = async (payload: ProductPayload): Promise<void> => {
    const saved = await runWrite(
      { kind: 'create', payload },
      'Producto creado. Ya aparece en el catálogo del POS.',
    )
    if (saved) {
      setMode({ kind: 'closed' })
      setFormVersion((version) => version + 1)
    }
  }

  const handleUpdate = async (productId: string, payload: ProductUpdatePayload): Promise<void> => {
    const saved = await runWrite({ kind: 'update', productId, payload }, 'Producto actualizado.')
    if (saved) {
      setMode({ kind: 'closed' })
    }
  }

  const handleAdjust = async (product: Product, payload: AdjustPayload): Promise<void> => {
    const saved = await runWrite(
      { kind: 'adjust', productId: product.id, payload },
      'Stock ajustado. El listado muestra el stock que devolvió el servidor.',
    )
    if (saved) {
      setAdjusting(null)
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Inventario" title="Productos y stock" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Inventario" title="Productos y stock" />
        <ManagerOnlyNotice role={role} />
      </>
    )
  }

  const lowCount = products.filter((product) => product.low).length
  // The open forms and the ledger read the LAST row the server sent, so a reload
  // after a write never leaves a stale stock on screen.
  const currentOf = (product: Product): Product =>
    products.find((row) => row.id === product.id) ?? product
  const adjustingProduct = adjusting ? currentOf(adjusting) : null
  const movementsProduct = movementsFor ? currentOf(movementsFor) : null

  return (
    <>
      <PageHeader
        eyebrow="Inventario"
        title="Productos y stock"
        description="Productos que el salón vende en el POS, con su precio y el stock que informa el servidor. El stock cambia con las ventas y con los ajustes de reposición, pérdida o corrección."
        actions={
          <>
            {canManage ? (
              <button
                type="button"
                className="btn btn--wine btn--sm"
                onClick={() => {
                  setMode({ kind: 'create' })
                  setFormError(null)
                }}
              >
                Nuevo producto
              </button>
            ) : null}
            <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
              Actualizar
            </button>
          </>
        }
      />

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {canManage && mode.kind !== 'closed' ? (
        <section className="panel-card" aria-labelledby="product-form-title">
          <div className="panel-card__head">
            <h2 id="product-form-title">
              {mode.kind === 'edit' ? 'Editar producto' : 'Nuevo producto'}
            </h2>
            <span className="panel-card__meta">
              {mode.kind === 'edit' ? 'El stock no se edita acá' : 'Precio y stock inicial'}
            </span>
          </div>
          <ProductForm
            key={mode.kind === 'edit' ? `edit-${mode.product.id}` : `new-${formVersion}`}
            product={mode.kind === 'edit' ? mode.product : undefined}
            saving={saving}
            serverError={formError}
            onCancel={() => {
              setMode({ kind: 'closed' })
              setFormError(null)
            }}
            onSubmit={(payload) => {
              if (mode.kind === 'edit') {
                void handleUpdate(mode.product.id, payload as ProductUpdatePayload)
              } else {
                void handleCreate(payload as ProductPayload)
              }
            }}
          />
        </section>
      ) : null}

      {canManage && adjustingProduct ? (
        <section className="panel-card" aria-labelledby="product-adjust-title">
          <div className="panel-card__head">
            <h2 id="product-adjust-title">Ajustar el stock</h2>
            <span className="panel-card__meta">{adjustingProduct.name}</span>
          </div>
          <ProductAdjustForm
            key={`adjust-${adjustingProduct.id}`}
            product={adjustingProduct}
            saving={saving}
            serverError={formError}
            onCancel={() => {
              setAdjusting(null)
              setFormError(null)
            }}
            onSubmit={(payload) => void handleAdjust(adjustingProduct, payload)}
          />
        </section>
      ) : null}

      {canManage && movementsProduct ? (
        <ProductMovements
          product={movementsProduct}
          movements={movements}
          loading={movementsLoading}
          error={movementsError}
          limit={movementsLimit}
          timeZone={timeZone}
          onLimitChange={setMovementsLimit}
          onClose={() => setMovementsFor(null)}
        />
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando los productos…' : 'Cargando los productos…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="products-table-title">
          <div className="panel-card__head">
            <h2 id="products-table-title">Productos del salón</h2>
            <span className="panel-card__meta">
              {products.length === 1 ? '1 producto' : `${products.length} productos`}
              {lowCount > 0 ? ` · ${lowCount} ${LOW_STOCK_LABEL.toLowerCase()}` : ''}
            </span>
          </div>

          {products.length === 0 ? (
            <p className="empty-state">
              {canManage
                ? 'El salón todavía no tiene productos. Crea el primero con «Nuevo producto».'
                : 'El salón todavía no tiene productos cargados.'}
            </p>
          ) : (
            <div className="table-wrap">
              <table className="table table--stack">
                <thead>
                  <tr>
                    <th scope="col">Producto</th>
                    <th scope="col" className="table__num">
                      Precio
                    </th>
                    <th scope="col" className="table__num">
                      Stock
                    </th>
                    <th scope="col" className="table__num">
                      Umbral
                    </th>
                    <th scope="col">Estado</th>
                    {canManage ? (
                      <th scope="col">
                        <span className="visually-hidden">Acciones</span>
                      </th>
                    ) : null}
                  </tr>
                </thead>
                <tbody>
                  {products.map((product) => {
                    const mark = lowStockMark(product)
                    return (
                      <tr key={product.id} data-inactive={!product.active}>
                        <td data-label="Producto">
                          <span>
                            <strong>{product.name}</strong>
                            <span className="table__id">{product.id}</span>
                          </span>
                        </td>
                        <td className="table__num" data-label="Precio">
                          {formatCents(product.price_cents)}
                        </td>
                        <td className="table__num" data-label="Stock">
                          <span className="service-count">
                            <span className="service-count__label">{product.stock}</span>
                            {mark ? (
                              <span className="service-count__alert" role="status">
                                {mark}
                              </span>
                            ) : null}
                          </span>
                        </td>
                        <td className="table__num" data-label="Umbral">
                          {product.low_stock_threshold === 0 ? (
                            <span className="muted">Sin alerta</span>
                          ) : (
                            product.low_stock_threshold
                          )}
                        </td>
                        <td data-label="Estado">
                          <span className={`pill ${product.active ? 'pill--on' : 'pill--off'}`}>
                            {product.active ? 'Activo' : 'Inactivo'}
                          </span>
                        </td>
                        {canManage ? (
                          <td className="table__actions" data-label="Acciones">
                            <div className="team-confirm__buttons">
                              <button
                                type="button"
                                className="btn btn--quiet"
                                onClick={() => {
                                  setMode({ kind: 'edit', product })
                                  setAdjusting(null)
                                  setFormError(null)
                                }}
                              >
                                Editar
                              </button>
                              <button
                                type="button"
                                className="btn btn--quiet"
                                onClick={() => {
                                  setAdjusting(product)
                                  setMode({ kind: 'closed' })
                                  setFormError(null)
                                }}
                              >
                                Ajustar stock
                              </button>
                              <button
                                type="button"
                                className="btn btn--quiet"
                                onClick={() => {
                                  setMovementsFor(product)
                                  setMovementsLimit(MOVEMENTS_DEFAULT_LIMIT)
                                }}
                              >
                                Movimientos
                              </button>
                            </div>
                          </td>
                        ) : null}
                      </tr>
                    )
                  })}
                </tbody>
              </table>
            </div>
          )}

          {!canManage ? (
            <p className="field__hint">
              Tu rol ve el listado y el stock. Crear, editar, ajustar y ver los
              movimientos queda en manos de la dueña o de la administración.
            </p>
          ) : null}
          {canManage ? (
            <p className="field__hint">
              Un producto no se borra: se desactiva para que deje de venderse y conserve su
              historial.
            </p>
          ) : null}
        </section>
      ) : null}
    </>
  )
}
