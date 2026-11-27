import { formatAuditWhen } from '@/features/audit/lib/audit'
import {
  depositStatusLabel,
  depositStatusTone,
  type DepositRow,
} from '../lib/deposits'
import { formatCents } from '@/lib/money'

type DepositListProps = {
  deposits: readonly DepositRow[]
  /** IANA zone of the salon: the dates and times are shown in it. */
  timeZone: string
}

/**
 * Rows of `/panel/senas`, newest first (the order of the server). Each row says who
 * the booking is for, when it starts, how much was paid or is due, the state in
 * Spanish and, small, the folios of the deposit and the booking.
 */
export function DepositList({ deposits, timeZone }: DepositListProps) {
  return (
    <ol className="deposit-list" aria-label="Señas del salón">
      {deposits.map((row) => {
        const tone = depositStatusTone(row.status)
        return (
          <li key={row.id} className="deposit-row">
            <div className="deposit-row__main">
              <p className="deposit-row__client">
                {row.client_name.trim() === '' ? 'Clienta sin nombre' : row.client_name}
              </p>
              <p className="deposit-row__meta">
                <span>Turno: </span>
                <time dateTime={row.starts_at}>{formatAuditWhen(row.starts_at, timeZone)}</time>
              </p>
              {row.paid_at !== null ? (
                <p className="deposit-row__meta">
                  <span>Pago: </span>
                  <time dateTime={row.paid_at}>{formatAuditWhen(row.paid_at, timeZone)}</time>
                </p>
              ) : null}
            </div>
            <div className="deposit-row__side">
              <p className="deposit-row__amount">{formatCents(row.amount_cents)}</p>
              <span className={`pill pill--deposit-${tone}`}>{depositStatusLabel(row.status)}</span>
            </div>
            <p className="deposit-row__ids">
              <span>Seña </span>
              <code>{row.id}</code>
              {row.booking_id !== '' ? (
                <>
                  <span> · Turno </span>
                  <code>{row.booking_id}</code>
                </>
              ) : null}
            </p>
          </li>
        )
      })}
    </ol>
  )
}
