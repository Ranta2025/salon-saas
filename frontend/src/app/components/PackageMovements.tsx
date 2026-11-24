import { formatHistoryInstant } from '../../lib/clientHistory'
import {
  movementReasonLabel,
  sessionsLabel,
  type ClientPackage,
  type PackageMovement,
} from '../../lib/packages'

type PackageMovementsProps = {
  pkg: ClientPackage
  movements: readonly PackageMovement[]
  loading: boolean
  /** Sentence of the failed request, shown verbatim. */
  error: string | null
  /** Zone of the salon, used to print each instant in local time. */
  timeZone: string
  /** Name of a professional by id; unknown ids print a dash. */
  professionalName: (id: string) => string
  onClose: () => void
}

/**
 * Ledger of one balance (contract F8, D8/D9, owner and admin): every movement,
 * newest first, with its signed sessions, reason, professional and note. It is a
 * reading of what the server stored: the balance is never computed from it here.
 */
export function PackageMovements({
  pkg,
  movements,
  loading,
  error,
  timeZone,
  professionalName,
  onClose,
}: PackageMovementsProps) {
  return (
    <div className="package-movements" role="region" aria-label={`Movimientos de ${pkg.name}`}>
      <div className="panel-card__head">
        <h3>Movimientos de {pkg.name}</h3>
        <span className="panel-card__meta">Saldo actual {sessionsLabel(pkg)}</span>
      </div>

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? (
        <p className="muted" role="status">
          Cargando los movimientos…
        </p>
      ) : null}
      {!loading && !error && movements.length === 0 ? (
        <p className="empty-state">Este paquete todavía no tiene movimientos.</p>
      ) : null}

      {movements.length > 0 ? (
        <div className="table-wrap">
          <table className="table table--stack">
            <thead>
              <tr>
                <th scope="col">Fecha</th>
                <th scope="col">Motivo</th>
                <th scope="col" className="table__num">
                  Sesiones
                </th>
                <th scope="col">Profesional</th>
                <th scope="col">Nota</th>
              </tr>
            </thead>
            <tbody>
              {movements.map((movement) => (
                <tr key={movement.id}>
                  <td data-label="Fecha">
                    {formatHistoryInstant(movement.created_at, timeZone) || (
                      <span className="muted">Sin fecha</span>
                    )}
                  </td>
                  <td data-label="Motivo">{movementReasonLabel(movement.reason)}</td>
                  <td className="table__num" data-label="Sesiones">
                    {movement.delta > 0 ? `+${movement.delta}` : movement.delta}
                  </td>
                  <td data-label="Profesional">
                    {movement.professional_id ? (
                      professionalName(movement.professional_id)
                    ) : (
                      <span className="muted">—</span>
                    )}
                  </td>
                  <td data-label="Nota">
                    {movement.note === '' ? <span className="muted">Sin nota</span> : movement.note}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      <div className="page-head__actions">
        <button type="button" className="btn btn--quiet" onClick={onClose}>
          Cerrar movimientos
        </button>
      </div>
    </div>
  )
}
