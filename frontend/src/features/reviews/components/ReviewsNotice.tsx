import { roleLabel, type Role } from '@/lib/permissions'

/**
 * Explanation a role without access to the reviews sees instead of the page
 * (contract F13, D6/D8: owner, admin and reception read the reviews; a professional
 * gets 403 from the server, so the menu entry stays hidden too).
 */
export function ReviewsNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>Las reseñas las ven la administración y la recepción</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver las reseñas del salón: las escriben las clientas
        después de un turno atendido. Si necesitás acceso, pídeselo a la dueña o a la
        administración.
      </p>
    </div>
  )
}
