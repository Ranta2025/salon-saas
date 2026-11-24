import { useState, type FormEvent } from 'react'
import { Link } from 'react-router-dom'
import { formatCents } from '../../lib/money'
import {
  packageSessionsLabel,
  packageValidityLabel,
  validateSellDraft,
  type SellDraft,
  type SellField,
  type SellPayload,
  type ServicePackage,
} from '../../lib/packages'
import type { StaffMember } from '../../lib/staff'
import { SALE_METHODS } from '../../types'

type PackageSellFormProps = {
  /** Only the packages that can be sold (active) come in. */
  packages: readonly ServicePackage[]
  staff: readonly StaffMember[]
  /** Owner and admin get a link to create the first package. */
  canCreatePackages: boolean
  saving: boolean
  /** Sentence of the server (400/404) or the offline/network notice, verbatim. */
  serverError: string | null
  onCancel: () => void
  onSubmit: (payload: SellPayload) => void
}

const EMPTY_DRAFT: SellDraft = { package_id: '', professional_id: '', method: 'cash' }

/**
 * «Vender paquete» (contract F8, D5): package, professional and payment method
 * (the labels of the POS). The sale is a normal sale of money: the amount comes
 * from the catalog price the server holds, never from this form.
 */
export function PackageSellForm({
  packages,
  staff,
  canCreatePackages,
  saving,
  serverError,
  onCancel,
  onSubmit,
}: PackageSellFormProps) {
  const [draft, setDraft] = useState<SellDraft>(EMPTY_DRAFT)
  const [error, setError] = useState<{ field: SellField; message: string } | null>(null)
  const fieldError = (field: SellField): string | null =>
    error?.field === field ? error.message : null
  // A message about a field goes away as soon as the person changes the form.
  const change = (patch: Partial<SellDraft>): void => {
    setDraft({ ...draft, ...patch })
    setError(null)
  }
  const chosen = packages.find((pkg) => pkg.id === draft.package_id) ?? null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    const check = validateSellDraft(draft)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSubmit(check.payload)
  }

  if (packages.length === 0) {
    return (
      <div className="package-sell">
        <p className="empty-state">
          No hay paquetes activos para vender.{' '}
          {canCreatePackages ? (
            <Link to="/panel/paquetes">Crea uno en «Paquetes»</Link>
          ) : (
            'Pídele a la administración que cree uno.'
          )}
        </p>
        <div className="page-head__actions">
          <button type="button" className="btn btn--quiet" onClick={onCancel}>
            Cerrar
          </button>
        </div>
      </div>
    )
  }

  return (
    <form className="service-form" onSubmit={handleSubmit} noValidate>
      <div className="service-form__grid">
        <div className="field">
          <label htmlFor="package-sell-package">Paquete</label>
          <select
            id="package-sell-package"
            className="select"
            value={draft.package_id}
            onChange={(event) => change({ package_id: event.target.value })}
            aria-invalid={fieldError('package') ? true : undefined}
            aria-describedby={fieldError('package') ? 'package-sell-package-error' : undefined}
            required
          >
            <option value="">Elegí un paquete</option>
            {packages.map((pkg) => (
              <option key={pkg.id} value={pkg.id}>
                {pkg.name} · {formatCents(pkg.price_cents)}
              </option>
            ))}
          </select>
          {fieldError('package') ? (
            <p className="field__error" id="package-sell-package-error" role="alert">
              {fieldError('package')}
            </p>
          ) : null}
        </div>

        <div className="field">
          <label htmlFor="package-sell-professional">Profesional que lo vende</label>
          <select
            id="package-sell-professional"
            className="select"
            value={draft.professional_id}
            onChange={(event) => change({ professional_id: event.target.value })}
            aria-invalid={fieldError('professional') ? true : undefined}
            aria-describedby={
              fieldError('professional') ? 'package-sell-professional-error' : undefined
            }
            required
          >
            <option value="">Elegí quién lo vende</option>
            {staff.map((member) => (
              <option key={member.id} value={member.id}>
                {member.name}
              </option>
            ))}
          </select>
          {fieldError('professional') ? (
            <p className="field__error" id="package-sell-professional-error" role="alert">
              {fieldError('professional')}
            </p>
          ) : null}
        </div>

        <div className="field">
          <label htmlFor="package-sell-method">Método de pago</label>
          <select
            id="package-sell-method"
            className="select"
            value={draft.method}
            onChange={(event) => change({ method: event.target.value })}
            aria-invalid={fieldError('method') ? true : undefined}
          >
            {Object.entries(SALE_METHODS).map(([value, label]) => (
              <option key={value} value={value}>
                {label}
              </option>
            ))}
          </select>
          {fieldError('method') ? (
            <p className="field__error" role="alert">
              {fieldError('method')}
            </p>
          ) : null}
        </div>
      </div>

      {chosen ? (
        <p className="field__hint">
          {packageSessionsLabel(chosen.sessions)} de {chosen.service_name || 'un servicio'} ·{' '}
          {packageValidityLabel(chosen.validity_days)}. Se registra una venta de{' '}
          <strong>{formatCents(chosen.price_cents)}</strong> y la clienta recibe el saldo.
        </p>
      ) : null}

      {serverError ? (
        <p className="notice notice--error" role="alert">
          {serverError}
        </p>
      ) : null}

      <div className="page-head__actions">
        <button type="submit" className="btn btn--wine" disabled={saving}>
          {saving
            ? 'Vendiendo…'
            : chosen
              ? `Vender por ${formatCents(chosen.price_cents)}`
              : 'Vender paquete'}
        </button>
        <button type="button" className="btn btn--quiet" onClick={onCancel} disabled={saving}>
          Cancelar
        </button>
      </div>
    </form>
  )
}
