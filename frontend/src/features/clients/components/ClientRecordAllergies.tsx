import { ALLERGIES_NOTICE, ALLERGIES_TITLE } from '../lib/clientRecord'

/**
 * Allergies of the client (contract F5, D4). Health data: the notice is shown to
 * EVERY active member — a professional needs it while attending — and it is the
 * first thing under the title, not a row of the data table. When the field is
 * empty the component renders nothing at all.
 */
export function ClientRecordAllergies({ allergies }: { allergies: string }) {
  const value = allergies.trim()
  if (value === '') {
    return null
  }
  return (
    <section
      className="panel-card client-allergies"
      aria-labelledby="client-allergies-title"
      data-allergies="true"
    >
      <div className="panel-card__head">
        <h2 id="client-allergies-title">{ALLERGIES_TITLE}</h2>
        <span className="tag">Dato de salud</span>
      </div>
      <p className="notice notice--error" role="note">
        <strong>{value}</strong>
      </p>
      <p className="muted">{ALLERGIES_NOTICE}</p>
    </section>
  )
}
