import {
  formatProfitNet,
  profitDayBarLabel,
  shortDateLabel,
  type ProfitDayBar,
} from '../lib/profit'
import { formatCents } from '@/lib/money'

/** Same red as `.field__error` / `.notice--error`: no stylesheet change here. */
const NEGATIVE_COLOR = '#8c1d33'

/** One bar per local day, drawn with plain CSS; no chart dependency (contract F3b). */
export function ProfitDayBars({ bars }: { bars: readonly ProfitDayBar[] }) {
  if (bars.length === 0) {
    return <p className="muted">No hay días en este período.</p>
  }
  // A month has up to 31 days: above ten bars only the edges are labelled, so the
  // chart never overflows at 390 px. Every bar keeps its full accessible name.
  const showEveryLabel = bars.length <= 10
  const first = bars[0]
  const last = bars[bars.length - 1]

  return (
    <div style={{ display: 'grid', gap: '0.4rem' }}>
      <ul
        aria-label="Ingresos por día"
        style={{
          display: 'flex',
          alignItems: 'flex-end',
          gap: '0.15rem',
          listStyle: 'none',
          margin: 0,
          padding: 0,
        }}
      >
        {bars.map((bar) => {
          const label = profitDayBarLabel(bar)
          const negative = bar.net_cents < 0
          return (
            <li
              key={bar.date}
              tabIndex={0}
              aria-label={label}
              title={label}
              style={{ flex: '1 1 0', minWidth: 0, display: 'grid', gap: '0.2rem' }}
            >
              <span
                aria-hidden="true"
                style={{
                  display: 'flex',
                  alignItems: 'flex-end',
                  height: '6.5rem',
                  background: 'var(--rose-tint)',
                  borderRadius: 'var(--radius-xs)',
                  overflow: 'hidden',
                }}
              >
                <span
                  style={{
                    display: 'block',
                    width: '100%',
                    height: `${bar.height}%`,
                    background: negative
                      ? NEGATIVE_COLOR
                      : 'linear-gradient(0deg, var(--wine), var(--gold))',
                    borderRadius: 'var(--radius-xs) var(--radius-xs) 0 0',
                  }}
                />
              </span>
              {showEveryLabel ? (
                <span
                  aria-hidden="true"
                  style={{
                    fontSize: '0.6rem',
                    color: 'var(--ink-muted)',
                    textAlign: 'center',
                    whiteSpace: 'nowrap',
                    overflow: 'hidden',
                  }}
                >
                  {bar.label}
                </span>
              ) : null}
            </li>
          )
        })}
      </ul>
      <div
        aria-hidden="true"
        style={{
          display: 'flex',
          justifyContent: 'space-between',
          fontSize: '0.6rem',
          color: 'var(--ink-muted)',
        }}
      >
        <span>{showEveryLabel ? '' : first ? shortDateLabel(first.date) : ''}</span>
        <span>{showEveryLabel ? '' : last ? shortDateLabel(last.date) : ''}</span>
      </div>
      <p className="field__hint">
        La barra es el ingreso de cada día; el neto del día está en el detalle de cada barra.
        {bars.some((bar) => bar.net_cents < 0)
          ? ' Los días con ganancia negativa se pintan en rojo.'
          : ''}
      </p>
      <ul
        aria-label="Detalle por día"
        style={{ listStyle: 'none', margin: 0, padding: 0, display: 'grid', gap: '0.15rem', fontSize: '0.8125rem' }}
      >
        {bars.map((bar) => (
          <li
            key={`${bar.date}-detail`}
            style={{ display: 'flex', justifyContent: 'space-between', gap: '0.75rem' }}
          >
            <span className="muted">{bar.label || bar.date}</span>
            <span>
              {`Ingresos ${formatCents(bar.revenue_cents)} · Neto `}
              <span
                style={
                  bar.net_cents < 0 ? { color: NEGATIVE_COLOR, fontWeight: 700 } : undefined
                }
              >
                {formatProfitNet(bar.net_cents)}
              </span>
            </span>
          </li>
        ))}
      </ul>
    </div>
  )
}
