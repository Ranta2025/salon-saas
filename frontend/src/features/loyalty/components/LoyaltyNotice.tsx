import { roleLabel, type Role } from '@/lib/permissions'

/**
 * Explanation a role without access to loyalty sees instead of the page or of the
 * section (contract F9, D5-D7: owner, admin and reception read the status of a
 * client and deliver and use rewards; a professional gets 403 from the server).
 * Same pattern as `PackagesNotice`, with this section's copy: the shared notice
 * talks about payroll and commissions.
 */
export function LoyaltyNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>La fidelidad la ven la administración y la recepción</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver las visitas ni los premios de las clientas:
        llevan datos de consumo. Si necesitás acceso, pedíselo a la dueña o a la
        administración.
      </p>
    </div>
  )
}
