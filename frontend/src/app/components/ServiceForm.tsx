import { useState, type FormEvent } from 'react'
import { formatCents, parseAmountToCents } from '../../lib/money'
import type { Service, ServiceInput, ServiceUpdateInput } from '../../types'

type ServiceFormProps = {
  /** Absent = create mode. */
  service?: Service
  onSubmit: (input: ServiceInput | ServiceUpdateInput) => Promise<void>
  onCancel: () => void
}

export function ServiceForm({ service, onSubmit, onCancel }: ServiceFormProps) {
  const editing = Boolean(service)
  const [name, setName] = useState(service?.name ?? '')
  const [duration, setDuration] = useState(String(service?.duration_min ?? 45))
  const [price, setPrice] = useState(
    service ? (service.price_cents / 100).toFixed(2) : '',
  )
  const [commission, setCommission] = useState(String(service?.commission_pct ?? 0))
  const [active, setActive] = useState(service?.active ?? true)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)

  const handleSubmit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    setError(null)

    const priceCents = parseAmountToCents(price)
    const durationMin = Number(duration)
    const commissionPct = Number(commission)

    if (name.trim().length < 2) {
      setError('El nombre del servicio es obligatorio.')
      return
    }
    if (!Number.isInteger(durationMin) || durationMin < 5 || durationMin > 600) {
      setError('La duración debe ser un número entero entre 5 y 600 minutos.')
      return
    }
    if (priceCents === null) {
      setError('El precio debe ser un monto válido, por ejemplo 350.00.')
      return
    }
    if (!Number.isInteger(commissionPct) || commissionPct < 0 || commissionPct > 100) {
      setError('La comisión debe ser un porcentaje entero entre 0 y 100.')
      return
    }

    const input: ServiceInput = {
      name: name.trim(),
      duration_min: durationMin,
      price_cents: priceCents,
      commission_pct: commissionPct,
    }

    setPending(true)
    try {
      if (editing) {
        await onSubmit({ ...input, active })
      } else {
        await onSubmit(input)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'No pudimos guardar el servicio.')
    } finally {
      setPending(false)
    }
  }

  const priceCentsInForm = parseAmountToCents(price)

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="panel-card__head">
        <h2>{editing ? 'Editar servicio' : 'Nuevo servicio'}</h2>
        <button type="button" className="btn btn--quiet btn--sm" onClick={onCancel}>
          Cancelar
        </button>
      </div>

      <div className="service-form__grid">
        <div className="field service-form__wide">
          <label htmlFor="service-name">Nombre</label>
          <input
            id="service-name"
            className="input"
            value={name}
            onChange={(event) => setName(event.target.value)}
            maxLength={80}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="service-duration">Duración (min)</label>
          <input
            id="service-duration"
            className="input"
            type="number"
            min={5}
            max={600}
            step={5}
            value={duration}
            onChange={(event) => setDuration(event.target.value)}
            required
          />
        </div>
        <div className="field">
          <label htmlFor="service-price">Precio (MXN)</label>
          <input
            id="service-price"
            className="input"
            inputMode="decimal"
            value={price}
            onChange={(event) => setPrice(event.target.value)}
            placeholder="350.00"
            required
          />
          {priceCentsInForm !== null ? (
            <p className="field__hint">
              Se guarda como {priceCentsInForm} centavos ({formatCents(priceCentsInForm)}).
            </p>
          ) : null}
        </div>
        <div className="field">
          <label htmlFor="service-commission">Comisión (%)</label>
          <input
            id="service-commission"
            className="input"
            type="number"
            min={0}
            max={100}
            step={1}
            value={commission}
            onChange={(event) => setCommission(event.target.value)}
            required
          />
        </div>
        {editing ? (
          <label className="checkbox service-form__wide">
            <input
              type="checkbox"
              checked={active}
              onChange={(event) => setActive(event.target.checked)}
            />
            <span>Servicio activo (visible para vender)</span>
          </label>
        ) : null}
      </div>

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      <button type="submit" className="btn btn--wine" disabled={pending}>
        {pending ? 'Guardando…' : editing ? 'Guardar cambios' : 'Crear servicio'}
      </button>
    </form>
  )
}
