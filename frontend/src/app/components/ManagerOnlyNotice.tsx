import { roleLabel, type Role } from '../../lib/permissions'
import { LoadingState } from './Feedback'

type ManagerOnlyNoticeProps = {
  role: Role | null
  /**
   * What the section shows, for the sentence of the wall: «los gastos del salón»,
   * «el inventario ni el stock de los productos». Every page names its own thing
   * instead of repeating the payroll sentence (S-F3.2-5 / S-F3.3-5).
   */
  what?: string
  /**
   * `GET /me` has not answered yet: the role is UNKNOWN, not forbidden, so the page
   * shows the loading state instead of a wall that says «Sin rol».
   */
  loading?: boolean
  /** Retry of the failed `/me` (the role never arrived). */
  onRetry?: () => void
}

/**
 * Explanation a professional or reception sees instead of a management page
 * (S-F3.2-5 / S-F3.3-5). Nómina and the detail of a run share the same copy so the
 * forbidden page of both routes is identical.
 */
export function ManagerOnlyNotice({
  role,
  what = 'la nómina ni las comisiones del equipo',
  loading = false,
  onRetry,
}: ManagerOnlyNoticeProps) {
  if (loading) {
    return <LoadingState label="Comprobando tu rol…" />
  }

  // With the identity call answered, `null` means it failed: saying «Sin rol» would
  // be a lie about the person, so the page says what it does not know.
  if (role === null) {
    return (
      <div className="empty-state">
        <h2>No pudimos comprobar tu rol</h2>
        <p>
          La sesión no nos dijo qué rol tenés en este salón, así que no podemos abrir esta
          sección. Vuelve a intentarlo; si sigue igual, cierra sesión y entra de nuevo.
        </p>
        {onRetry ? (
          <button type="button" className="btn btn--quiet" onClick={onRetry}>
            Reintentar
          </button>
        ) : null}
      </div>
    )
  }

  return (
    <div className="empty-state">
      <h2>Esta sección es de la administración</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver {what}. Si necesitas acceso, pídeselo a la dueña
        o a la administración.
      </p>
    </div>
  )
}
