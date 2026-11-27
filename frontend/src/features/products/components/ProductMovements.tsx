import { formatHistoryInstant } from '@/features/clients/lib/clientHistory'
import {
  LOW_STOCK_LABEL,
  movementReasonLabel,
  MOVEMENTS_DEFAULT_LIMIT,
  MOVEMENTS_MAX_LIMIT,
  type Product,
  type StockMovement,
} from '../lib/products'

type ProductMovementsProps = {
  product: Product
  movements: readonly StockMovement[]
  loading: boolean
  /** Spanish sentence of the failed request, shown verbatim. */
  error: string | null
  /** How many rows were asked to the server (1..100). */
  limit: number
  /** Zone of the salon, used to print each instant in local time. */
  timeZone: string
  onLimitChange: (limit: number) => void
  onClose: () => void
}

/** Options of the list: the default of the contract and its two neighbours. */
const LIMIT_OPTIONS: readonly number[] = [20, MOVEMENTS_DEFAULT_LIMIT, MOVEMENTS_MAX_LIMIT]

/**
 * Ledger of one product (contract F6): every entry of `stock_movements`, newest
 * first, with its signed delta, its reason and the sale that produced it. It is only
 * a reading of what the server stored: the stock of the product is never computed
 * here from the movements.
 */
export function ProductMovements({
  product,
  movements,
  loading,
  error,
  limit,
  timeZone,
  onLimitChange,
  onClose,
}: ProductMovementsProps) {
  return (
    <section className="panel-card" aria-labelledby="product-movements-title">
      <div className="panel-card__head">
        <h2 id="product-movements-title">Movimientos de {product.name}</h2>
        <span className="panel-card__meta">
          Stock actual {product.stock}
          {product.low ? ` · ${LOW_STOCK_LABEL}` : ''}
        </span>
      </div>

      <div className="filters">
        <div className="field">
          <label htmlFor="product-movements-limit">Últimos movimientos</label>
          <select
            id="product-movements-limit"
            className="select"
            value={limit}
            onChange={(event) => onLimitChange(Number(event.target.value))}
          >
            {LIMIT_OPTIONS.map((option) => (
              <option key={option} value={option}>
                {option} movimientos
              </option>
            ))}
          </select>
        </div>
        <button type="button" className="btn btn--quiet" onClick={onClose}>
          Cerrar movimientos
        </button>
      </div>

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? (
        <p className="muted" role="status">
          Cargando los movimientos…
        </p>
      ) : null}

      {!loading && !error && movements.length === 0 ? (
        <p className="empty-state">
          Este producto todavía no tiene movimientos: el stock inicial y cada venta o
          ajuste aparecen acá.
        </p>
      ) : null}

      {movements.length > 0 ? (
        <div className="table-wrap">
          <table className="table table--stack">
            <thead>
              <tr>
                <th scope="col">Fecha</th>
                <th scope="col">Motivo</th>
                <th scope="col" className="table__num">
                  Cambio
                </th>
                <th scope="col">Nota</th>
                <th scope="col">Venta</th>
              </tr>
            </thead>
            <tbody>
              {movements.map((movement) => (
                <tr key={movement.id}>
                  <td data-label="Fecha">
                    {formatHistoryInstant(movement.created_at, timeZone) || (
                      <span className="muted">Sin fecha</span>
                    )}
                  </td>
                  <td data-label="Motivo">{movementReasonLabel(movement.reason)}</td>
                  <td className="table__num" data-label="Cambio">
                    {movement.delta > 0 ? `+${movement.delta}` : movement.delta}
                  </td>
                  <td data-label="Nota">
                    {movement.note === '' ? (
                      <span className="muted">Sin nota</span>
                    ) : (
                      movement.note
                    )}
                  </td>
                  <td data-label="Venta">
                    {movement.sale_id ? (
                      <span className="table__id">{movement.sale_id}</span>
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      <p className="field__hint">
        El stock del producto es la suma de estos movimientos. La interfaz no lo calcula:
        lo muestra tal como lo devuelve el servidor.
      </p>
    </section>
  )
}
