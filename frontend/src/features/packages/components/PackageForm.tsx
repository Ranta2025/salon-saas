import { useEffect, useState, type FormEvent } from 'react'
import {
  MAX_PACKAGE_NAME_LENGTH,
  MAX_PACKAGE_SESSIONS,
  MAX_VALIDITY_DAYS,
  packagePriceInputFromCents,
  packageSessionsLabel,
  validatePackageDraft,
  type PackageCreatePayload,
  type PackageDraft,
  type PackageField,
  type PackageUpdatePayload,
  type ServicePackage,
} from '../lib/packages'

/** A service the new package can be made of (only the fields the form needs). */
export type PackageServiceOption = { id: string; name: string }

type PackageFormProps = {
  /** The package being replaced, or absent to create a new one. */
  pkg?: ServicePackage
  /** Services of the salon for the picker of a new package. */
  services: readonly PackageServiceOption[]
  /** Name of the service of the package being edited (it is read-only there). */
  serviceName?: string
  saving: boolean
  /** Spanish sentence of the server (400/409) or the offline notice, shown verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: PackageCreatePayload | PackageUpdatePayload) => void
}

function draftOf(pkg?: ServicePackage): PackageDraft {
  if (!pkg) {
    return {
      name: '',
      service_id: '',
      sessions: '',
      price: '',
      no_expiry: false,
      validity_days: '',
      active: true,
    }
  }
  return {
    name: pkg.name,
    service_id: pkg.service_id,
    sessions: String(pkg.sessions),
    price: packagePriceInputFromCents(pkg.price_cents),
    no_expiry: pkg.validity_days === null,
    validity_days: pkg.validity_days === null ? '' : String(pkg.validity_days),
    active: pkg.active,
  }
}

/**
 * Form of a package of the catalog (contract F8, D10): name, service and sessions
 * (only when creating: the PUT does not edit them), price typed in dollars and
 * turned into integer cents, validity in days or «Sin vencimiento», and the state
 * when editing. The draft is checked with the sentences of the API before sending.
 */
export function PackageForm({
  pkg,
  services,
  serviceName,
  saving,
  serverError,
  onCancel,
  onSubmit,
}: PackageFormProps) {
  const editing = pkg !== undefined
  const [draft, setDraft] = useState<PackageDraft>(() => draftOf(pkg))
  const [error, setError] = useState<{ field: PackageField; message: string } | null>(null)

  useEffect(() => {
    setDraft(draftOf(pkg))
    setError(null)
  }, [pkg])

  const idBase = editing ? `package-edit-${pkg.id}` : 'package-new'
  const fieldError = (field: PackageField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validatePackageDraft(draft, editing ? 'update' : 'create')
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="service-form__grid">
        <div className="field">
          <label htmlFor={`${idBase}-name`}>Nombre del paquete</label>
          <input
            id={`${idBase}-name`}
            className="input"
            type="text"
            autoComplete="off"
            placeholder="5 cortes"
            value={draft.name}
            onChange={(event) => setDraft({ ...draft, name: event.target.value })}
            aria-invalid={fieldError('name') ? true : undefined}
            aria-describedby={fieldError('name') ? `${idBase}-name-error` : undefined}
            required
          />
          {fieldError('name') ? (
            <p className="field__error" id={`${idBase}-name-error`} role="alert">
              {fieldError('name')}
            </p>
          ) : null}
        </div>

        {editing ? (
          <div className="field">
            <p className="muted">Servicio y sesiones</p>
            <p>
              <strong>{serviceName && serviceName !== '' ? serviceName : 'Servicio del paquete'}</strong>
              {' · '}
              {packageSessionsLabel(pkg.sessions)}
            </p>
            <p className="field__hint">No se editan: para cambiarlos crea otro paquete.</p>
          </div>
        ) : (
          <>
            <div className="field">
              <label htmlFor={`${idBase}-service`}>Servicio</label>
              <select
                id={`${idBase}-service`}
                className="select"
                value={draft.service_id}
                onChange={(event) => setDraft({ ...draft, service_id: event.target.value })}
                aria-invalid={fieldError('service') ? true : undefined}
                aria-describedby={fieldError('service') ? `${idBase}-service-error` : undefined}
                required
              >
                <option value="">Elegí un servicio</option>
                {services.map((service) => (
                  <option key={service.id} value={service.id}>
                    {service.name}
                  </option>
                ))}
              </select>
              {fieldError('service') ? (
                <p className="field__error" id={`${idBase}-service-error`} role="alert">
                  {fieldError('service')}
                </p>
              ) : null}
            </div>

            <div className="field">
              <label htmlFor={`${idBase}-sessions`}>Sesiones</label>
              <input
                id={`${idBase}-sessions`}
                className="input"
                type="text"
                inputMode="numeric"
                autoComplete="off"
                placeholder="5"
                value={draft.sessions}
                onChange={(event) => setDraft({ ...draft, sessions: event.target.value })}
                aria-invalid={fieldError('sessions') ? true : undefined}
                aria-describedby={
                  fieldError('sessions') ? `${idBase}-sessions-error` : `${idBase}-sessions-hint`
                }
                required
              />
              {fieldError('sessions') ? (
                <p className="field__error" id={`${idBase}-sessions-error`} role="alert">
                  {fieldError('sessions')}
                </p>
              ) : (
                <p className="field__hint" id={`${idBase}-sessions-hint`}>
                  De 1 a {MAX_PACKAGE_SESSIONS}, sin decimales.
                </p>
              )}
            </div>
          </>
        )}

        <div className="field">
          <label htmlFor={`${idBase}-price`}>Precio del paquete en dólares</label>
          <input
            id={`${idBase}-price`}
            className="input"
            type="text"
            inputMode="decimal"
            autoComplete="off"
            placeholder="250.00"
            value={draft.price}
            onChange={(event) => setDraft({ ...draft, price: event.target.value })}
            aria-invalid={fieldError('price') ? true : undefined}
            aria-describedby={
              fieldError('price') ? `${idBase}-price-error` : `${idBase}-price-hint`
            }
            required
          />
          {fieldError('price') ? (
            <p className="field__error" id={`${idBase}-price-error`} role="alert">
              {fieldError('price')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-price-hint`}>
              Precio total, hasta dos decimales: 250, 250.50 o 250,50. Una venta ya hecha
              conserva el precio con el que se vendió.
            </p>
          )}
        </div>

        <div className="field">
          <label htmlFor={`${idBase}-validity`}>Vigencia en días</label>
          <input
            id={`${idBase}-validity`}
            className="input"
            type="text"
            inputMode="numeric"
            autoComplete="off"
            placeholder="90"
            value={draft.validity_days}
            disabled={draft.no_expiry}
            onChange={(event) => setDraft({ ...draft, validity_days: event.target.value })}
            aria-invalid={fieldError('validity') ? true : undefined}
            aria-describedby={
              fieldError('validity') ? `${idBase}-validity-error` : `${idBase}-validity-hint`
            }
          />
          <label className="checkbox" htmlFor={`${idBase}-no-expiry`}>
            <input
              id={`${idBase}-no-expiry`}
              type="checkbox"
              checked={draft.no_expiry}
              onChange={(event) => setDraft({ ...draft, no_expiry: event.target.checked })}
            />
            <span>Sin vencimiento</span>
          </label>
          {fieldError('validity') ? (
            <p className="field__error" id={`${idBase}-validity-error`} role="alert">
              {fieldError('validity')}
            </p>
          ) : (
            <p className="field__hint" id={`${idBase}-validity-hint`}>
              De 1 a {MAX_VALIDITY_DAYS} días desde la compra. Vence al final de ese día.
            </p>
          )}
        </div>

        {editing ? (
          <div className="field">
            <label className="checkbox" htmlFor={`${idBase}-active`}>
              <input
                id={`${idBase}-active`}
                type="checkbox"
                checked={draft.active}
                onChange={(event) => setDraft({ ...draft, active: event.target.checked })}
              />
              <span>Activo: se puede vender</span>
            </label>
            <p className="field__hint">
              Un paquete no se borra: se desactiva para que deje de venderse. Las clientas
              conservan su saldo.
            </p>
          </div>
        ) : null}
      </div>

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving ? 'Guardando…' : editing ? 'Guardar cambios' : 'Crear paquete'}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={saving}>
          Cancelar
        </button>
      </div>

      <p className="field__hint">El nombre admite hasta {MAX_PACKAGE_NAME_LENGTH} caracteres.</p>
    </form>
  )
}
