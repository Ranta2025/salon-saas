import { useCallback, useEffect, useState } from 'react'
import {
  createService,
  describeApiError,
  fetchServices,
  updateService,
  type ApiErrorNotice,
} from '../lib/api'
import { formatCents, formatMinutes } from '../lib/money'
import type { Service, ServiceInput, ServiceUpdateInput } from '../types'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { ServiceForm } from './components/ServiceForm'

type FormMode = { kind: 'closed' } | { kind: 'create' } | { kind: 'edit'; service: Service }

export function ServicesPage() {
  const { salonId } = useSalon()
  const [services, setServices] = useState<Service[]>([])
  const [includeInactive, setIncludeInactive] = useState(true)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [mode, setMode] = useState<FormMode>({ kind: 'closed' })
  const [reloadKey, setReloadKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    fetchServices(salonId, includeInactive)
      .then((list) => {
        if (active) {
          setServices(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar los servicios.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, includeInactive, reloadKey])

  const handleCreate = async (input: ServiceInput | ServiceUpdateInput): Promise<void> => {
    if (!salonId) {
      return
    }
    await createService(salonId, input)
    setMode({ kind: 'closed' })
    setNotice('Servicio creado. Ya aparece en el catálogo del POS.')
    reload()
  }

  const handleUpdate = async (
    serviceId: string,
    input: ServiceInput | ServiceUpdateInput,
  ): Promise<void> => {
    if (!salonId) {
      return
    }
    await updateService(salonId, serviceId, { ...input, active: 'active' in input ? input.active : true })
    setMode({ kind: 'closed' })
    setNotice('Servicio actualizado.')
    reload()
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Catálogo" title="Servicios" />
        <SalonRequired />
      </>
    )
  }

  return (
    <>
      <PageHeader
        eyebrow="Catálogo"
        title="Servicios y precios"
        description="Precios finales en pesos mexicanos, con duración y comisión de cada servicio."
        actions={
          <button
            type="button"
            className="btn btn--wine btn--sm"
            onClick={() => setMode({ kind: 'create' })}
          >
            Nuevo servicio
          </button>
        }
      />

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {mode.kind !== 'closed' ? (
        <ServiceForm
          service={mode.kind === 'edit' ? mode.service : undefined}
          onSubmit={
            mode.kind === 'edit'
              ? (input) => handleUpdate(mode.service.id, input)
              : handleCreate
          }
          onCancel={() => setMode({ kind: 'closed' })}
        />
      ) : null}

      <div className="filters">
        <label className="checkbox">
          <input
            type="checkbox"
            checked={includeInactive}
            onChange={(event) => setIncludeInactive(event.target.checked)}
          />
          <span>Mostrar servicios inactivos</span>
        </label>
        <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
          Actualizar
        </button>
      </div>

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando servicios…' : 'Cargando servicios…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="services-table-title">
          <div className="panel-card__head">
            <h2 id="services-table-title">Catálogo del salón</h2>
            <span className="panel-card__meta">{services.length} servicios</span>
          </div>
          {services.length === 0 ? (
            <p className="muted">
              El salón todavía no tiene servicios. Crea el primero con “Nuevo servicio”.
            </p>
          ) : (
            <div className="table-wrap">
              <table className="table table--stack">
                <thead>
                  <tr>
                    <th scope="col">Servicio</th>
                    <th scope="col">Duración</th>
                    <th scope="col" className="table__num">
                      Precio
                    </th>
                    <th scope="col" className="table__num">
                      Comisión
                    </th>
                    <th scope="col">Estado</th>
                    <th scope="col">
                      <span className="visually-hidden">Acciones</span>
                    </th>
                  </tr>
                </thead>
                <tbody>
                  {services.map((service) => (
                    <tr key={service.id} data-inactive={!service.active}>
                      <td data-label="Servicio">
                        <span>
                          <strong>{service.name}</strong>
                          <span className="table__id">{service.id}</span>
                        </span>
                      </td>
                      <td data-label="Duración">{formatMinutes(service.duration_min)}</td>
                      <td className="table__num" data-label="Precio">
                        {formatCents(service.price_cents)}
                      </td>
                      <td className="table__num" data-label="Comisión">
                        {service.commission_pct}%
                      </td>
                      <td data-label="Estado">
                        <span className={`pill ${service.active ? 'pill--on' : 'pill--off'}`}>
                          {service.active ? 'Activo' : 'Inactivo'}
                        </span>
                      </td>
                      <td className="table__actions" data-label="Acciones">
                        <button
                          type="button"
                          className="btn btn--quiet btn--sm"
                          onClick={() => setMode({ kind: 'edit', service })}
                        >
                          Editar
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </section>
      ) : null}
    </>
  )
}
