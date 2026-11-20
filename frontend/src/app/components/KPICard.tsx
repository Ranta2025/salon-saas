type KPICardProps = {
  label: string
  value: string
  hint?: string
  trend?: string
  accent?: 'wine' | 'gold' | 'plum'
}

export function KPICard({ label, value, hint, trend, accent = 'wine' }: KPICardProps) {
  return (
    <article className={`kpi kpi--${accent}`}>
      <p className="kpi__label">{label}</p>
      <p className="kpi__value">{value}</p>
      <p className="kpi__foot">
        {trend ? <span className="kpi__trend">{trend}</span> : null}
        {hint ? <span className="kpi__hint">{hint}</span> : null}
      </p>
    </article>
  )
}
