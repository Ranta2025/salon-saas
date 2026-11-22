import { useCallback, useEffect, useState } from 'react'
import {
  createService,
  describeApiError,
  fetchServices,
  updateService,
  type ApiErrorNotice,
} from '../lib/api'
import {
  fetchEquipment,
  NO_ACTIVE_EQUIPMENT_NOTICE,
  resourceTypeName,
  serviceOffersSlots,
  serviceResourceTypeId,
  type ResourceType,
} from '../lib/equipment'
import { fetchMembers, type SalonMember } from '../lib/members'
import {
  bookableMembers,
  NOBODY_DOES_SERVICE_NOTICE,
  nobodyDoesService,
  professionalCount,
  professionalsCountLabel,
} from '../lib/tradeServices'
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
  const [members, setMembers] = useState<SalonMember[] | null>(null)
  // Equipment of the salon (S-F2e-5): null until it loads, and null forever if
  // the call fails. Without it the page claims nothing about equipment.
  const [equipment, setEquipment] = useState<ResourceType[] | null>(null)
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

  // Count of professionals per service (S-F2c-7) comes from GET /members, which
  // only owner/admin can read. Without it no count is claimed at all: an empty
  // list would be a lie about the salon.
  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    fetchMembers(salonId)
      .then((list) => {
        if (active) {
          setMembers(list)
        }
      })
      .catch(() => {
        if (active) {
          setMembers(null)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, reloadKey])

  useEffect(() => {
    if (!salonId) {
      return
    }
    let active = true
    fetchEquipment(salonId)
      .then((list) => {
        if (active) {
          setEquipment(list)
        }
      })
      .catch(() => {
        if (active) {
          setEquipment(null)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, reloadKey])

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
        description="Precios finales en dólares estadounidenses, con duración y comisión de cada servicio."
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
          professionals={bookableMembers(members ?? [])}
          equipment={equipment ?? []}
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
                    {members !== null ? <th scope="col">Profesionales</th> : null}
                    {equipment !== null ? <th scope="col">Equipo</th> : null}
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
                      {members !== null ? (
                        <td data-label="Profesionales" className="service-cell">
                          {/* Grid inside the cell: a `display:grid` on the <td>
                              itself would leave the table and merge with the next
                              cell (two `.service-cell`s are now adjacent). */}
                          <span className="service-count">
                            {(() => {
                              const count = professionalCount(members, service.id)
                              const nobody = nobodyDoesService(members, service.id)
                              return (
                                <>
                                  <span className="service-count__label">
                                    {professionalsCountLabel(count)}
                                  </span>
                                  {nobody ? (
                                    <span className="service-count__alert" role="status">
                                      {NOBODY_DOES_SERVICE_NOTICE}
                                    </span>
                                  ) : null}
                                </>
                              )
                            })()}
                          </span>
                        </td>
                      ) : null}
                      {equipment !== null ? (
                        <td data-label="Equipo" className="service-cell">
                          <span className="service-count">
                            {(() => {
                              const typeId = serviceResourceTypeId(service)
                              const name = resourceTypeName(equipment, typeId)
                              if (typeId === null) {
                                return <span className="muted">Sin equipo</span>
                              }
                              return (
                                <>
                                  <span className="service-count__label">
                                    {name ?? 'Equipo borrado'}
                                  </span>
                                  {!serviceOffersSlots(equipment, typeId) ? (
                                    <span className="service-count__alert" role="status">
                                      {NO_ACTIVE_EQUIPMENT_NOTICE}
                                    </span>
                                  ) : null}
                                </>
                              )
                            })()}
                          </span>
                        </td>
                      ) : null}
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
