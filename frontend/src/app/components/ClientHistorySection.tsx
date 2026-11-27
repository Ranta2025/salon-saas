import type { ApiErrorNotice } from '../../lib/api'
import { statusLabel } from '@/features/bookings/lib/bookingActions'
import {
  BOOKINGS_TITLE,
  EMPTY_HISTORY_TEXT,
  HISTORY_TITLE,
  NO_MONEY_HINT,
  SALES_TITLE,
  SPEND_CARD_LABEL,
  SPEND_TITLE,
  clientHistoryMoney,
  formatHistoryInstant,
  hasLinkedBookings,
  spendText,
  summaryCards,
  type ClientHistory,
} from '../../lib/clientHistory'
import { formatCents } from '../../lib/money'
import { SALE_METHODS, type SaleMethod } from '../../types'
import { ErrorNotice, LoadingState } from './Feedback'
import { KPICard } from './KPICard'

type ClientHistorySectionProps = {
  history: ClientHistory | null
  error: ApiErrorNotice | null
  loading: boolean
  onRetry: () => void
  /** False for the roles the server does not send money to (D6). */
  canSeeMoney: boolean
}

function methodLabel(method: string): string {
  return SALE_METHODS[method as SaleMethod] ?? method
}

/**
 * History of the client (contract F5, D6): the summary cards, the recent bookings
 * and — ONLY when the server sends `spend` — the spend card and the linked sales.
 * Every amount goes through `formatCents`; there is no other arithmetic here, so
 * a figure the API did not send can never appear.
 */
export function ClientHistorySection({
  history,
  error,
  loading,
  onRetry,
  canSeeMoney,
}: ClientHistorySectionProps) {
  const money = history ? clientHistoryMoney(history) : null
  const timeZone = history?.summary.timezone ?? ''

  return (
    <section className="panel-card" aria-labelledby="client-history-title">
      <div className="panel-card__head">
        <h2 id="client-history-title">{HISTORY_TITLE}</h2>
        <span className="panel-card__meta">
          {timeZone === '' ? 'Últimos turnos y ventas' : `Días en la zona del salón (${timeZone})`}
        </span>
      </div>

      {loading ? <LoadingState label={history ? 'Actualizando historial…' : 'Cargando historial…'} /> : null}
      {error ? <ErrorNotice error={error} onRetry={onRetry} /> : null}

      {history ? (
        <>
          <div className="kpi-grid">
            {summaryCards(history).map((card) => (
              // The `kpi__value` size is built for short counts: a date at 2.4rem
              // does not fit the four columns of a full-width grid, so the value
              // keeps the card but takes the normal text size and wraps.
              <article className="kpi" key={card.label}>
                <p className="kpi__label">{card.label}</p>
                <p>
                  <strong>{card.value}</strong>
                </p>
                <p className="kpi__foot">{card.hint}</p>
              </article>
            ))}
          </div>

          <h3>{BOOKINGS_TITLE}</h3>
          {hasLinkedBookings(history) ? (
            <div className="table-wrap">
              {/* `table--stack` turns each row into a card at 390 px, so the four
                  columns never need a sideways scroll on a phone. */}
              <table className="table table--stack">
                <thead>
                  <tr>
                    <th scope="col">Turno</th>
                    <th scope="col">Estado</th>
                    <th scope="col">Servicio</th>
                    <th scope="col">Profesional</th>
                  </tr>
                </thead>
                <tbody>
                  {history.bookings.map((booking) => (
                    <tr key={booking.id}>
                      <td data-label="Turno">{formatHistoryInstant(booking.starts_at, timeZone)}</td>
                      <td data-label="Estado">
                        <span className="pill" data-status={booking.status}>
                          {statusLabel(booking.status)}
                        </span>
                      </td>
                      <td data-label="Servicio">
                        {booking.service_name === '' ? '—' : booking.service_name}
                      </td>
                      <td data-label="Profesional">
                        {booking.professional_name === '' ? '—' : booking.professional_name}
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          ) : (
            <div className="empty-state">
              <h2>Sin turnos vinculados</h2>
              <p>{EMPTY_HISTORY_TEXT}</p>
            </div>
          )}

          {money ? (
            <>
              <h3>{SPEND_TITLE}</h3>
              <div className="kpi-grid">
                <KPICard
                  label={SPEND_CARD_LABEL}
                  value={formatCents(money.spend.total_cents)}
                  hint={spendText(money.spend)}
                  accent="gold"
                />
              </div>
              <h3>{SALES_TITLE}</h3>
              {money.sales.length === 0 ? (
                <p className="muted">
                  Todavía no hay ventas vinculadas a esta clienta. Las ventas del POS se vinculan
                  cuando elegís la ficha al cobrar.
                </p>
              ) : (
                <div className="table-wrap">
                  <table className="table table--stack">
                    <thead>
                      <tr>
                        <th scope="col">Fecha</th>
                        <th scope="col">Método</th>
                        <th scope="col" className="table__num">
                          Monto
                        </th>
                      </tr>
                    </thead>
                    <tbody>
                      {money.sales.map((sale) => (
                        <tr key={sale.id}>
                          <td data-label="Fecha">{formatHistoryInstant(sale.created_at, timeZone)}</td>
                          <td data-label="Método">{methodLabel(sale.method)}</td>
                          <td data-label="Monto" className="table__num">
                            {formatCents(sale.total_cents)}
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>
              )}
            </>
          ) : canSeeMoney ? null : (
            <p className="muted">{NO_MONEY_HINT}</p>
          )}
        </>
      ) : null}
    </section>
  )
}
