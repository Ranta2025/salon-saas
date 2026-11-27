import type { LandingModel } from '@/lib/publicSalon'

type TeamSectionProps = {
  team: LandingModel['team']
}

/** Rendered only when the salon publishes at least one professional (name and trade only). */
export function TeamSection({ team }: TeamSectionProps) {
  return (
    <section className="section team" id="equipo" aria-labelledby="team-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Equipo</p>
          <h2 id="team-title">Quiénes te atienden</h2>
        </div>
        <ul className="team__grid">
          {team.map((member, index) => (
            <li key={`${member.name}-${index}`} className="team-card">
              <span className="team-card__name">{member.name}</span>
              {member.trade !== null ? (
                <span className="team-card__trade">{member.trade}</span>
              ) : null}
            </li>
          ))}
        </ul>
      </div>
    </section>
  )
}
