import { useState, type FormEvent } from 'react'
import { formatCents } from '../../lib/money'
import { changeQty, itemCount, lineSubtotal, removeItem, totalCents, type Cart } from '../../lib/posCart'
import {
  SALE_METHODS,
  type SaleInput,
  type SaleMethod,
  type SaleOutcome,
} from '../../types'

type POSPanelProps = {
  cart: Cart
  onCartChange: (cart: Cart) => void
  onSubmit: (input: SaleInput) => Promise<SaleOutcome>
  onCompleted: () => void
  /** Sales of this device still waiting in the offline queue. */
  pendingCount?: number
}

/**
 * Cart + checkout. The UI computes a display total from integer cents, but the
 * amount that counts is the one the server returns. When the network is down the
 * parent queues the sale and returns a `queued` outcome instead.
 */
export function POSPanel({
  cart,
  onCartChange,
  onSubmit,
  onCompleted,
  pendingCount = 0,
}: POSPanelProps) {
  const [clientName, setClientName] = useState('')
  const [professionalId, setProfessionalId] = useState('')
  const [method, setMethod] = useState<SaleMethod>('cash')
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const [receipt, setReceipt] = useState<{ outcome: SaleOutcome; uiTotal: number } | null>(null)

  const uiTotal = totalCents(cart)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)
    setReceipt(null)
    if (cart.length === 0) {
      setError('Agrega al menos un servicio a la venta.')
      return
    }
    if (professionalId.trim() === '') {
      setError('La API exige el ID del profesional que atendió la venta.')
      return
    }
    if (clientName.trim() === '') {
      setError('Escribe el nombre del cliente para el ticket.')
      return
    }
    const input: SaleInput = {
      professional_id: professionalId.trim(),
      client_name: clientName.trim(),
      method,
      items: cart.map((line) => ({
        service_id: line.service_id,
        qty: line.qty,
        unit_price_cents: line.unit_price_cents,
      })),
    }
    setPending(true)
    try {
      const outcome = await onSubmit(input)
      setReceipt({ outcome, uiTotal })
      onCartChange([])
      setClientName('')
      onCompleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : 'No pudimos registrar la venta.')
    } finally {
      setPending(false)
    }
  }

  return (
    <form className="pos-panel" onSubmit={handleSubmit} noValidate>
      <h2 className="pos-panel__title">Venta actual</h2>

      {cart.length === 0 ? (
        <p className="pos-panel__empty">Elige servicios del catálogo para armar la venta.</p>
      ) : (
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
      )}

      <dl className="pos-total">
        <div>
          <dt>Artículos</dt>
          <dd>{itemCount(cart)}</dd>
        </div>
        <div className="pos-total__grand">
          <dt>Total a cobrar</dt>
          <dd>{formatCents(uiTotal)}</dd>
        </div>
      </dl>

      <div className="pos-fields">
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
        </div>
        <div className="field">
          <label htmlFor="pos-professional">ID del profesional</label>
          <input
            id="pos-professional"
            className="input"
            value={professionalId}
            onChange={(event) => setProfessionalId(event.target.value)}
            autoComplete="off"
            placeholder="prof-123"
          />
          <p className="field__hint">
            La API de ventas lo exige y aún no hay endpoint para listar profesionales.
          </p>
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
