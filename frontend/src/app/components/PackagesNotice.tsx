import { roleLabel, type Role } from '../../lib/permissions'

/**
 * Explanation a role without access to the packages sees instead of the page or
 * of the section (contract F8, D8/D10: owner, admin and reception read the
 * catalog and the balances; a professional gets 403 from the server). Same
 * pattern as `ManagerOnlyNotice`, with this section's copy: the shared notice
 * talks about payroll and commissions.
 */
export function PackagesNotice({ role }: { role: Role | null }) {
  return (
    <div className="empty-state">
      <h2>Los paquetes los ve la administración y la recepción</h2>
      <p>
        Tu rol ({roleLabel(role)}) no puede ver los paquetes ni los saldos de las clientas: llevan
        precios y sesiones prepagas. Si necesitás acceso, pídeselo a la dueña o a la
        administración.
      </p>
    </div>
  )
}
