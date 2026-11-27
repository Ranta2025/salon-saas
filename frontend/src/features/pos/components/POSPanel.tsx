import { useRef, useState, type FormEvent } from 'react'
import { formatCents } from '@/lib/money'
import { dropReasonLabel, findDropped, type DroppedOperation } from '@/lib/offlineQueue'
import type { StaffMember } from '@/lib/staff'
import {
  changeQty,
  itemCount,
  lineSubtotal,
  removeItem,
  saleWithClient,
  totalCents,
  type Cart,
  type PosPickedClient,
} from '../lib/posCart'
import {
  changeProductQty,
  productCartQty,
  productCartTotalCents,
  productLineSubtotal,
  productQtyCap,
  removeProduct,
  saleWithProducts,
  stockAvailable,
  type PosProduct,
  type ProductCart,
} from '@/features/products/lib/productCart'
import { isInsufficientStockMessage } from '@/features/products/lib/products'
import type { PickableClient } from '@/features/clients/lib/clientRecord'
import { ClientPicker } from './ClientPicker'
import {
  SALE_METHODS,
  type SaleInput,
  type SaleMethod,
  type SaleOutcome,
} from '@/types'

type POSPanelProps = {
  cart: Cart
  /** Team of the salon, from GET /salons/:id/staff. */
  staff: StaffMember[]
  /** Message of the failed /staff request, shown next to the select. */
  staffError: string | null
  onCartChange: (cart: Cart) => void
  onSubmit: (input: SaleInput) => Promise<SaleOutcome>
  onCompleted: () => void
  /** Sales of this device still waiting in the offline queue. */
  pendingCount?: number
  /** Operations the queue gave up on: a queued sale here never reached the API. */
  dropped?: readonly DroppedOperation[]
  /**
   * Client files of the salon for the OPTIONAL picker (F5, D3c). Absent means the
   * panel behaves exactly as before: a free-text name and no `client_id`.
   */
  clients?: readonly PickableClient[]
  /** Message of the failed /clients request, shown next to the picker. */
  clientsError?: string | null
  clientsLoading?: boolean
  /**
   * Product lines of this same sale (contract F6). Absent means the panel behaves
   * exactly as before: services only, and no `products` key in the body.
   */
  productCart?: ProductCart
  /** Active products with the stock the server last reported, to cap the cart. */
  products?: readonly PosProduct[]
  /** Parent that owns the product cart. */
  onProductCartChange?: (cart: ProductCart) => void
  /**
   * Called when the API answered the 409 of insufficient stock, so the POS asks the
   * server for the catalogue again: the client never fixes its own stock figure.
   */
  onStockConflict?: () => void
}

/**
 * Cart + checkout. The UI computes a display total from integer cents, but the
 * amount that counts is the one the server returns. When the network is down the
 * parent queues the sale and returns a `queued` outcome instead.
 */
const ROLE_LABEL: Record<StaffMember['role'], string> = {
  owner: 'Dueña o dueño',
  admin: 'Administración',
  professional: 'Profesional',
}

export function POSPanel({
  cart,
  staff,
  staffError,
  onCartChange,
  onSubmit,
  onCompleted,
  pendingCount = 0,
  dropped = [],
  clients = [],
  clientsError = null,
  clientsLoading = false,
  productCart,
  products = [],
  onProductCartChange,
  onStockConflict,
}: POSPanelProps) {
  const [clientName, setClientName] = useState('')
  // F5: the client file is optional. Picking one fills the name and sends
  // `client_id`; with nobody picked the body and the flow are exactly as before.
  const [pickedClient, setPickedClient] = useState<PickableClient | null>(null)
  const [professionalId, setProfessionalId] = useState('')
  const [method, setMethod] = useState<SaleMethod>('cash')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [receipt, setReceipt] = useState<{ outcome: SaleOutcome; uiTotal: number } | null>(null)
  // The 409 of insufficient stock, printed verbatim and with a retry. The cart is
  // kept whole: the sale is not lost, the person fixes the quantity and retries.
  const [stockConflict, setStockConflict] = useState<string | null>(null)
  // React state alone is not enough to stop a double click: both presses can read
  // the same render, so the guard is synchronous.
  const submitting = useRef(false)
  // A sale that the server rejected never syncs: saying "se enviará sola" would
  // be a lie, so the receipt repeats the reason the API gave.
  const droppedReceipt =
    receipt && receipt.outcome.kind === 'queued'
      ? findDropped(dropped, receipt.outcome.client_op_id)
      : null

  const productLines = productCart ?? []
  const knownStock = (productId: string): number => stockAvailable(products, productId)
  const uiTotal = totalCents(cart) + productCartTotalCents(productLines)
  const productCount = productCartQty(productLines)

  const runSale = async (): Promise<void> => {
    setError(null)
    setReceipt(null)
    setStockConflict(null)
    if (cart.length === 0 && productLines.length === 0) {
      setError(
        productCart === undefined
          ? 'Agrega al menos un servicio a la venta.'
          : 'Agrega al menos un servicio o un producto a la venta.',
      )
      return
    }
    if (professionalId.trim() === '' || !staff.some((member) => member.id === professionalId)) {
      setError('Elige al profesional que atendió la venta.')
      return
    }
    if (clientName.trim() === '' && !pickedClient) {
      setError('Escribe el nombre del cliente para el ticket.')
      return
    }
    if (submitting.current) {
      return
    }
    const picked: PosPickedClient | null = pickedClient
    // F6: the product lines travel as `{product_id, qty}` only, and without any of
    // them the body is EXACTLY the one of today (no `products` key).
    const input: SaleInput = saleWithProducts(
      saleWithClient(
        {
          professional_id: professionalId.trim(),
          client_name: clientName.trim(),
          method,
          items: cart.map((line) => ({
            service_id: line.service_id,
            qty: line.qty,
            unit_price_cents: line.unit_price_cents,
          })),
        },
        picked,
      ),
      productLines,
    )
    submitting.current = true
    setPending(true)
    try {
      const outcome = await onSubmit(input)
      setReceipt({ outcome, uiTotal })
      onCartChange([])
      onProductCartChange?.([])
      setClientName('')
      setPickedClient(null)
      onCompleted()
    } catch (err) {
      const message = err instanceof Error ? err.message : 'No pudimos registrar la venta.'
      if (isInsufficientStockMessage(message)) {
        setStockConflict(message)
        // The stock the panel knew is stale: the catalogue is read again.
        onStockConflict?.()
      } else {
        setError(message)
      }
    } finally {
      submitting.current = false
      setPending(false)
    }
  }

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    void runSale()
  }

  return (
    <form className="pos-panel" onSubmit={handleSubmit} noValidate>
      <h2 className="pos-panel__title">Venta actual</h2>

      {cart.length === 0 && productLines.length === 0 ? (
        <p className="pos-panel__empty">
          {productCart === undefined
            ? 'Elige servicios del catálogo para armar la venta.'
            : 'Elige servicios o productos del catálogo para armar la venta.'}
        </p>
      ) : null}

      {cart.length > 0 ? (
        <ul className="pos-lines">
          {cart.map((line) => (
            <li className="pos-line" key={line.service_id}>
              <div className="pos-line__info">
                <p className="pos-line__name">{line.name}</p>
                <p className="pos-line__price">
                  {formatCents(line.unit_price_cents)} × {line.qty} ={' '}
                  <strong>{formatCents(lineSubtotal(line))}</strong>
                </p>
              </div>
              <div className="pos-line__qty">
                <button
                  type="button"
                  className="qty-btn"
                  aria-label={`Quitar una unidad de ${line.name}`}
                  onClick={() => onCartChange(changeQty(cart, line.service_id, line.qty - 1))}
                >
                  −
                </button>
                <span aria-live="polite">{line.qty}</span>
                <button
                  type="button"
                  className="qty-btn"
                  aria-label={`Agregar una unidad de ${line.name}`}
                  onClick={() => onCartChange(changeQty(cart, line.service_id, line.qty + 1))}
                >
                  +
                </button>
                <button
                  type="button"
                  className="pos-line__remove"
                  onClick={() => onCartChange(removeItem(cart, line.service_id))}
                >
                  Quitar
                </button>
              </div>
            </li>
          ))}
        </ul>
      ) : null}

      {/* F6: the product lines of the same sale. The quantity is never above the
          stock the server last reported. */}
      {productLines.length > 0 ? (
        <>
          <h3 style={{ fontSize: 'var(--step-0)' }}>Productos</h3>
          <ul className="pos-lines">
            {productLines.map((line) => {
              const cap = productQtyCap(knownStock(line.product_id))
              return (
                <li className="pos-line" key={line.product_id}>
                  <div className="pos-line__info">
                    <p className="pos-line__name">{line.name}</p>
                    <p className="pos-line__price">
                      {formatCents(line.unit_price_cents)} × {line.qty} ={' '}
                      <strong>{formatCents(productLineSubtotal(line))}</strong>
                    </p>
                  </div>
                  <div className="pos-line__qty">
                    <button
                      type="button"
                      className="btn btn--quiet"
                      aria-label={`Quitar una unidad de ${line.name}`}
                      onClick={() =>
                        onProductCartChange?.(
                          changeProductQty(productLines, line.product_id, line.qty - 1, cap),
                        )
                      }
                    >
                      −
                    </button>
                    <span aria-live="polite">{line.qty}</span>
                    <button
                      type="button"
                      className="btn btn--quiet"
                      aria-label={`Agregar una unidad de ${line.name}`}
                      disabled={line.qty >= cap}
                      onClick={() =>
                        onProductCartChange?.(
                          changeProductQty(productLines, line.product_id, line.qty + 1, cap),
                        )
                      }
                    >
                      +
                    </button>
                    <button
                      type="button"
                      className="btn btn--quiet"
                      onClick={() =>
                        onProductCartChange?.(removeProduct(productLines, line.product_id))
                      }
                    >
                      Quitar
                    </button>
                  </div>
                  {line.qty >= cap ? (
                    <p className="field__hint">
                      Es todo el stock conocido de {line.name}: {cap}.
                    </p>
                  ) : null}
                </li>
              )
            })}
          </ul>
        </>
      ) : null}

      <dl className="pos-total">
        <div>
          <dt>Artículos</dt>
          <dd>{itemCount(cart) + productCount}</dd>
        </div>
        {productLines.length > 0 ? (
          <div>
            <dt>Servicios</dt>
            <dd>{formatCents(totalCents(cart))}</dd>
          </div>
        ) : null}
        {productLines.length > 0 ? (
          <div>
            <dt>Productos</dt>
            <dd>{formatCents(productCartTotalCents(productLines))}</dd>
          </div>
        ) : null}
        <div className="pos-total__grand">
          <dt>Total a cobrar</dt>
          <dd>{formatCents(uiTotal)}</dd>
        </div>
      </dl>

      <div className="pos-fields">
        <ClientPicker
          clients={clients}
          error={clientsError}
          loading={clientsLoading}
          selected={pickedClient}
          onSelect={(client) => {
            setPickedClient(client)
            if (client) {
              setClientName(client.name)
            }
          }}
        />
        <div className="field">
          <label htmlFor="pos-client">Cliente</label>
          <input
            id="pos-client"
            className="input"
            value={clientName}
            onChange={(event) => setClientName(event.target.value)}
            autoComplete="off"
            placeholder="Nombre de la clienta"
          />
          {pickedClient ? (
            <p className="field__hint">
              La venta queda vinculada a la ficha de {pickedClient.name}.
            </p>
          ) : null}
        </div>
        <div className="field">
          <label htmlFor="pos-professional">Profesional que atendió</label>
          <select
            id="pos-professional"
            className="select"
            value={professionalId}
            onChange={(event) => setProfessionalId(event.target.value)}
            disabled={staff.length === 0}
          >
            <option value="">Elige a la persona</option>
            {staff.map((member) => (
              <option key={member.id} value={member.id}>
                {member.name} · {ROLE_LABEL[member.role]}
              </option>
            ))}
          </select>
          {staffError && staff.length === 0 ? (
            <p className="field__error" role="alert">
              No pudimos cargar el equipo: {staffError}
            </p>
          ) : staff.length === 0 ? (
            <p className="field__hint">Cargando el equipo del salón…</p>
          ) : (
            <p className="field__hint">Lista del equipo del salón activo.</p>
          )}
        </div>
        <div className="field">
          <label htmlFor="pos-method">Método de pago</label>
          <select
            id="pos-method"
            className="select"
            value={method}
            onChange={(event) => setMethod(event.target.value as SaleMethod)}
          >
            {Object.entries(SALE_METHODS).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
        </div>
      </div>

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      {stockConflict ? (
        <>
          <p className="notice notice--error" role="alert">
            {stockConflict} La venta no se registró y el carrito quedó como estaba.
          </p>
          <div className="page-head__actions">
            <button
              type="button"
              className="btn btn--wine"
              disabled={pending}
              onClick={() => void runSale()}
            >
              Reintentar la venta
            </button>
          </div>
        </>
      ) : null}

      {receipt ? (
        receipt.outcome.kind === 'confirmed' ? (
          <div className="notice notice--success" role="status">
            <p>
              Venta <strong>{receipt.outcome.server.sale_id}</strong> registrada. El servidor
              calculó <strong>{formatCents(receipt.outcome.server.total)}</strong>; la interfaz
              mostró {formatCents(receipt.uiTotal)}.
              {receipt.outcome.server.total !== receipt.uiTotal
                ? ' El total del servidor es el que vale: revisa el catálogo.'
                : ''}
            </p>
          </div>
        ) : droppedReceipt ? (
          <div className="notice notice--error" role="alert">
            <p>
              <strong>Esta venta no se registró.</strong> {dropReasonLabel(droppedReceipt)} Vuelve a
              cobrarla corrigiendo el dato. Referencia{' '}
              <code>{droppedReceipt.client_op_id.slice(0, 8)}</code>.
            </p>
          </div>
        ) : (
          <div className="notice notice--info" role="status">
            <p>
              <strong>Venta guardada en este dispositivo.</strong> Todavía no está en el
              servidor: se enviará sola al volver la conexión. Referencia{' '}
              <code>{receipt.outcome.client_op_id.slice(0, 8)}</code>.
            </p>
          </div>
        )
      ) : null}

      {pendingCount > 0 ? (
        <p className="pos-panel__pending" role="status">
          {pendingCount} {pendingCount === 1 ? 'venta espera' : 'ventas esperan'} sincronizarse
          desde este dispositivo.
        </p>
      ) : null}

      <button type="submit" className="btn btn--wine pos-panel__submit" disabled={pending}>
        {pending ? 'Registrando…' : `Cobrar ${formatCents(uiTotal)}`}
      </button>
    </form>
  )
}
