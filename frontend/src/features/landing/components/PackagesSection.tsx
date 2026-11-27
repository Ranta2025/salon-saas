import { formatCents } from '@/lib/money'
import type { LandingPackage } from '@/lib/publicSalon'

type PackagesSectionProps = {
  packages: readonly LandingPackage[]
}

/** Rendered only when the salon has active packages (catalogue of F8). */
export function PackagesSection({ packages }: PackagesSectionProps) {
  return (
    <section className="section packages" id="paquetes" aria-labelledby="packages-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Paquetes</p>
          <h2 id="packages-title">Paquetes de sesiones</h2>
          <p className="lede">Se adquieren en el salón.</p>
        </div>
        <ul className="packages__grid">
          {packages.map((pkg) => (
            <li key={pkg.id} className="package-card">
              <h3 className="package-card__name">{pkg.name}</h3>
              <p className="package-card__detail">{pkg.detail}</p>
              <p className="package-card__price">
                <span className="visually-hidden">Precio: </span>
                {formatCents(pkg.priceCents)}
              </p>
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
