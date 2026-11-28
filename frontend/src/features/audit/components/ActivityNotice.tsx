import { roleLabel, type Role } from '@/lib/permissions'

/**
 * Explanation a role without access to the activity log sees instead of the page
 * (contract F10, D3/D13: only owner and admin read it; the server answers 403
 * `sin permiso` to everyone else, before touching the database).
 */
export function ActivityNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>La actividad la ve la administración</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver el registro de actividad del salón: dice quién
        hizo qué en la gestión del negocio. Si necesitás acceso, pedíselo a la dueña o a la
        administración.
      </p>
    </div>
  )
}
