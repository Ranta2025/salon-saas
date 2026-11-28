import { roleLabel, type Role } from '@/lib/permissions'

/**
 * Explanation a role without access to deposits sees instead of the page (contract
 * F11, D10/D13-a: owner, admin and reception read the list of deposits; a
 * professional gets 403 from the server). Same pattern as `LoyaltyNotice`.
 */
export function DepositNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>Las señas las ven la administración y la recepción</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver la lista de señas ni su política: lleva montos y
        datos de las clientas. Si necesitás acceso, pedíselo a la dueña o a la administración.
      </p>
    </div>
  )
}
