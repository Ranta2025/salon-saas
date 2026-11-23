import { roleLabel, type Role } from '../../lib/permissions'

/**
 * Explanation a professional or reception sees instead of a management page
 * (S-F3.2-5 / S-F3.3-5). Nómina and the detail of a run share the same copy so the
 * forbidden page of both routes is identical.
 */
export function ManagerOnlyNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>Esta sección es de la administración</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver la nómina ni las comisiones del equipo. Si
        necesitas acceso, pídeselo a la dueña o a la administración.
      </p>
    </div>
  )
}
