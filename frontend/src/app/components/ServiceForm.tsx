import { useState, type FormEvent } from 'react'
import {
  activeUnitCount,
  NO_ACTIVE_EQUIPMENT_NOTICE,
  serviceResourceTypeId,
  type ResourceType,
} from '../../lib/equipment'
import { formatCents, parseAmountToCents } from '../../lib/money'
import { normalizeServiceIds } from '../../lib/tradeServices'
import type { SalonMember } from '../../lib/members'
import type { Service, ServiceInput, ServiceUpdateInput } from '../../types'

type ServiceFormProps = {
  /** Absent = create mode. */
  service?: Service
  /** Bookable members offered to mark in create mode (S-F2c-7). */
  professionals?: readonly SalonMember[]
  /** Types of equipment the service may use (S-F2e-5); empty is valid. */
  equipment?: readonly ResourceType[]
  onSubmit: (input: ServiceInput | ServiceUpdateInput) => Promise<void>
  onCancel: () => void
}

export function ServiceForm({
  service,
  professionals = [],
  equipment = [],
  onSubmit,
  onCancel,
}: ServiceFormProps) {
  const editing = Boolean(service)
  const [name, setName] = useState(service?.name ?? '')
  const [duration, setDuration] = useState(String(service?.duration_min ?? 45))
  const [price, setPrice] = useState(
    service ? (service.price_cents / 100).toFixed(2) : '',
  )
  const [commission, setCommission] = useState(String(service?.commission_pct ?? 0))
  const [active, setActive] = useState(service?.active ?? true)
  const [professionalIds, setProfessionalIds] = useState<string[]>([])
  const [resourceTypeId, setResourceTypeId] = useState(serviceResourceTypeId(service ?? {}) ?? '')
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
      // S-F2e-3: a type id, or `null` when the service uses no equipment.
      resource_type_id: resourceTypeId === '' ? null : resourceTypeId,
    }

    setPending(true)
    try {
      if (editing) {
        await onSubmit({ ...input, active })
      } else {
        const ids = normalizeServiceIds(professionalIds)
        await onSubmit(ids.length > 0 ? { ...input, professional_ids: ids } : input)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'No pudimos guardar el servicio.')
    } finally {
      setPending(false)
    }
  }

  const priceCentsInForm = parseAmountToCents(price)
  // A type absent from the list (the equipment call failed, or the type was
  // deleted) stays selectable so saving the service never clears it silently.
  const knownType = equipment.find((type) => type.id === resourceTypeId) ?? null
  const unknownType = resourceTypeId !== '' && knownType === null

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
          <label htmlFor="service-price">Precio (USD)</label>
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
              Se guarda como {formatCents(priceCentsInForm)}.
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
        <div className="field service-form__wide">
          <label htmlFor="service-equipment">Equipo que usa</label>
          <select
            id="service-equipment"
            className="select"
            value={resourceTypeId}
            onChange={(event) => setResourceTypeId(event.target.value)}
          >
            <option value="">Ninguno (sin equipo)</option>
            {equipment.map((type) => (
              <option key={type.id} value={type.id}>
                {type.name}
              </option>
            ))}
            {unknownType ? <option value={resourceTypeId}>Equipo actual (sin nombre)</option> : null}
          </select>
          <p className="field__hint">
            Un turno de este servicio ocupa una unidad activa del equipo además de la profesional.
          </p>
          {knownType && activeUnitCount(knownType) === 0 ? (
            <p className="notice notice--info" role="status">
              {NO_ACTIVE_EQUIPMENT_NOTICE}
            </p>
          ) : null}
          {unknownType ? (
            <p className="field__hint">
              No pudimos leer el nombre de este equipo; se conserva el que ya tiene el servicio.
            </p>
          ) : null}
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
        {!editing ? (
          <fieldset className="check-group service-form__wide">
            <legend>Quiénes lo hacen</legend>
            <p className="field__hint">
              Si no marcás a nadie, el servicio queda sin profesionales y no se ofrecerán turnos
              hasta que lo asignes en Equipo o acá.
            </p>
            {professionals.length === 0 ? (
              <p className="field__hint">
                Todavía no hay profesionales que atiendan clientes en el salón.
              </p>
            ) : (
              <div className="check-group__list">
                {professionals.map((person) => (
                  <label key={person.id} className="check-group__option">
                    <input
                      type="checkbox"
                      checked={professionalIds.includes(person.id)}
                      onChange={() =>
                        setProfessionalIds((ids) =>
                          ids.includes(person.id)
                            ? ids.filter((id) => id !== person.id)
                            : [...ids, person.id],
                        )
                      }
                    />
                    <span>
                      {person.name}
                      {person.trade ? <span className="check-group__meta"> · {person.trade}</span> : null}
                    </span>
                  </label>
                ))}
              </div>
            )}
          </fieldset>
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
