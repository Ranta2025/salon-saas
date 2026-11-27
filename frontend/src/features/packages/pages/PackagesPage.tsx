import { useCallback, useEffect, useRef, useState } from 'react'
import { describeApiError, fetchServices, type ApiErrorNotice } from '@/lib/api'
import { formatCents } from '@/lib/money'
import {
  createPackagesGate,
  fetchPackages,
  packageSessionsLabel,
  packageValidityLabel,
  writePackage,
  type PackageCreatePayload,
  type PackageUpdatePayload,
  type ServicePackage,
} from '../lib/packages'
import { activeRole, canManagePackages, canViewPackages } from '@/lib/permissions'
import type { Service } from '@/types'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PackageForm } from '../components/PackageForm'
import { PackagesNotice } from '../components/PackagesNotice'
import { PageHeader } from '@/app/components/PageHeader'

type FormMode = { kind: 'closed' } | { kind: 'create' } | { kind: 'edit'; pkg: ServicePackage }

/**
 * `/panel/paquetes` (contract F8, D10/D11-a): the prepaid packages of the salon
 * with their service, sessions, price and validity.
 *
 * Owner and admin create and edit; reception only reads the list (it needs it to
 * sell); a professional gets `PackagesNotice` (the API answers 403 to them, this
 * page only avoids offering a button that would fail).
 *
 * Online only: offline the write is not sent and nothing is queued. After every
 * write the list is read again from the server (no optimistic rows).
 */
export function PackagesPage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const allowed = canViewPackages(role)
  const canManage = canManagePackages(role)

  const [packages, setPackages] = useState<ServicePackage[]>([])
  const [services, setServices] = useState<Service[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [reloadKey, setReloadKey] = useState(0)

  const [mode, setMode] = useState<FormMode>({ kind: 'closed' })
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState<string | null>(null)
  const [formVersion, setFormVersion] = useState(0)

  // The gate lives outside React state: two presses of the same render would both
  // see `saving === false` and send two requests.
  const gate = useRef(createPackagesGate())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || !allowed) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    // The services are only a label and the picker of the form: a failure here
    // never hides the catalog (the rows fall back to what the package carries).
    fetchServices(salonId, true)
      .then((list) => {
        if (active) {
          setServices(list)
        }
      })
      .catch(() => {
        if (active) {
          setServices([])
        }
      })
    fetchPackages(salonId)
      .then((list) => {
        if (active) {
          setPackages(list)
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar los paquetes del salón.'))
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
  }, [salonId, allowed, reloadKey])

  const runWrite = async (
    write:
      | { kind: 'create'; payload: PackageCreatePayload }
      | { kind: 'update'; packageId: string; payload: PackageUpdatePayload },
    successCopy: string,
  ): Promise<boolean> => {
    if (!salonId) {
      return false
    }
    setSaving(true)
    setFormError(null)
    setNotice(null)
    try {
      const outcome = await writePackage(gate.current, salonId, write)
      if (outcome.status === 'saved') {
        setNotice(successCopy)
        return true
      }
      setFormError(outcome.message)
      return false
    } finally {
      setSaving(false)
      // No optimistic rows: even a rejected write re-reads the server.
      reload()
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Paquetes" title="Paquetes prepagos" />
        <SalonRequired />
      </>
    )
  }

  if (!allowed) {
    return (
      <>
        <PageHeader eyebrow="Paquetes" title="Paquetes prepagos" />
        <PackagesNotice role={role} />
      </>
    )
  }

  const serviceNameOf = (pkg: ServicePackage): string =>
    pkg.service_name !== ''
      ? pkg.service_name
      : (services.find((service) => service.id === pkg.service_id)?.name ?? 'Servicio del paquete')
  const activeServices = services.filter((service) => service.active)

  return (
    <>
      <PageHeader
        eyebrow="Paquetes"
        title="Paquetes prepagos"
        description="Sesiones de un servicio a un precio fijo, con vigencia opcional. La clienta paga el paquete al comprarlo y cada sesión se descuenta de su saldo en la ficha."
        actions={
          <>
            {canManage ? (
              <button
                type="button"
                className="btn btn--wine btn--sm"
                onClick={() => {
                  setMode({ kind: 'create' })
                  setFormError(null)
                }}
              >
                Nuevo paquete
              </button>
            ) : null}
            <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
              Actualizar
            </button>
          </>
        }
      />

      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {canManage && mode.kind !== 'closed' ? (
        <section className="panel-card" aria-labelledby="package-form-title">
          <div className="panel-card__head">
            <h2 id="package-form-title">
              {mode.kind === 'edit' ? 'Editar paquete' : 'Nuevo paquete'}
            </h2>
            <span className="panel-card__meta">
              {mode.kind === 'edit'
                ? 'El servicio y las sesiones no se editan'
                : 'Servicio, sesiones, precio y vigencia'}
            </span>
          </div>
          <PackageForm
            key={mode.kind === 'edit' ? `edit-${mode.pkg.id}` : `new-${formVersion}`}
            pkg={mode.kind === 'edit' ? mode.pkg : undefined}
            services={activeServices}
            serviceName={mode.kind === 'edit' ? serviceNameOf(mode.pkg) : undefined}
            saving={saving}
            serverError={formError}
            onCancel={() => {
              setMode({ kind: 'closed' })
              setFormError(null)
            }}
            onSubmit={(payload) => {
              void (async () => {
                if (mode.kind === 'edit') {
                  const saved = await runWrite(
                    {
                      kind: 'update',
                      packageId: mode.pkg.id,
                      payload: payload as PackageUpdatePayload,
                    },
                    'Paquete actualizado.',
                  )
                  if (saved) {
                    setMode({ kind: 'closed' })
                  }
                } else {
                  const saved = await runWrite(
                    { kind: 'create', payload: payload as PackageCreatePayload },
                    'Paquete creado. Ya se puede vender desde la ficha de la clienta.',
                  )
                  if (saved) {
                    setMode({ kind: 'closed' })
                    setFormVersion((version) => version + 1)
                  }
                }
              })()
            }}
          />
        </section>
      ) : null}

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando los paquetes…' : 'Cargando los paquetes…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}

      {loaded ? (
        <section className="panel-card" aria-labelledby="packages-table-title">
          <div className="panel-card__head">
            <h2 id="packages-table-title">Paquetes del salón</h2>
            <span className="panel-card__meta">
              {packages.length === 1 ? '1 paquete' : `${packages.length} paquetes`}
            </span>
          </div>

          {packages.length === 0 ? (
            <p className="empty-state">
              {canManage
                ? 'El salón todavía no tiene paquetes. Crea el primero con «Nuevo paquete».'
                : 'El salón todavía no tiene paquetes cargados.'}
            </p>
          ) : (
            <div className="table-wrap">
              <table className="table table--stack">
                <thead>
                  <tr>
                    <th scope="col">Paquete</th>
                    <th scope="col">Servicio</th>
                    <th scope="col" className="table__num">
                      Sesiones
                    </th>
                    <th scope="col" className="table__num">
                      Precio
                    </th>
                    <th scope="col">Vigencia</th>
                    <th scope="col">Estado</th>
                    {canManage ? (
                      <th scope="col">
                        <span className="visually-hidden">Acciones</span>
                      </th>
                    ) : null}
                  </tr>
                </thead>
                <tbody>
                  {packages.map((pkg) => (
                    <tr key={pkg.id} data-inactive={!pkg.active}>
                      <td data-label="Paquete">
                        <span>
                          <strong>{pkg.name}</strong>
                        </span>
                      </td>
                      <td data-label="Servicio">{serviceNameOf(pkg)}</td>
                      <td className="table__num" data-label="Sesiones">
                        {packageSessionsLabel(pkg.sessions)}
                      </td>
                      <td className="table__num" data-label="Precio">
                        {formatCents(pkg.price_cents)}
                      </td>
                      <td data-label="Vigencia">{packageValidityLabel(pkg.validity_days)}</td>
                      <td data-label="Estado">
                        <span className={`pill ${pkg.active ? 'pill--on' : 'pill--off'}`}>
                          {pkg.active ? 'Activo' : 'Inactivo'}
                        </span>
                      </td>
                      {canManage ? (
                        <td className="table__actions" data-label="Acciones">
                          <div className="team-confirm__buttons">
                            <button
                              type="button"
                              className="btn btn--quiet"
                              onClick={() => {
                                setMode({ kind: 'edit', pkg })
                                setFormError(null)
                              }}
                            >
                              Editar
                            </button>
                          </div>
                        </td>
                      ) : null}
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}

          {!canManage ? (
            <p className="field__hint">
              Tu rol ve el catálogo y vende paquetes desde la ficha de la clienta. Crear y editar
              paquetes queda en manos de la dueña o de la administración.
            </p>
          ) : (
            <p className="field__hint">
              Un paquete no se borra: se desactiva para que deje de venderse. Los paquetes ya
              vendidos conservan su saldo y el precio con el que se compraron.
            </p>
          )}
        </section>
      ) : null}
    </>
  )
}
