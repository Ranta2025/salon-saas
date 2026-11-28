import { roleLabel, type Role } from '@/lib/permissions'

/**
 * Explanation a role without access to the waiting list sees instead of the
 * table (contract F7, D8: owner, admin and reception read it; a professional
 * gets 403 from the server). Same pattern as `ManagerOnlyNotice`, with the copy
 * of this section: the shared notice talks about payroll and commissions.
 */
export function WaitlistNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>La lista de espera la ve la administración y la recepción</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver los avisos: son datos de contacto de personas que
        pidieron que les avisen si se libera un turno. Si necesitás acceso, pedíselo a la dueña o
        a la administración.
      </p>
    </div>
  )
}
