import { formatCents } from '../../lib/money'
import type { ProfitCategoryRow } from '../../lib/profit'

/** Breakdown of the expenses by category (contract F3b), with the bar of each one. */
export function ProfitCategoryBreakdown({ rows }: { rows: readonly ProfitCategoryRow[] }) {
  if (rows.length === 0) {
    return <p className="muted">No hay gastos cargados en este período.</p>
  }
  return (
    <ul className="rank">
      {rows.map((row) => (
        <li key={row.category} className="rank__row">
          <div className="rank__label">
            <span>{row.label}</span>
            <span className="muted">{Math.round(row.share)}% de los gastos</span>
          </div>
          <div className="rank__bar" aria-hidden="true">
            <span style={{ width: `${row.share}%` }} />
          </div>
          <span className="rank__amount">{formatCents(row.total_cents)}</span>
        </li>
      ))}
    </ul>
  )
}
