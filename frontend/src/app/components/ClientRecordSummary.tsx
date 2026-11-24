import { NO_ALLERGIES_TEXT, type ClientRecord } from '../../lib/clientRecord'
import { displayPhone } from '../../lib/phone'

/**
 * Data of the client file (contract F5, D5). The allergies are repeated here as a
 * row so a file without them says so; when they exist, `ClientRecordAllergies`
 * shows them first, with the prominent notice.
 */
export function ClientRecordSummary({ record }: { record: ClientRecord }) {
  const allergies = record.allergies.trim()
  return (
    <section className="panel-card" aria-labelledby="client-info-title">
      <div className="panel-card__head">
        <h2 id="client-info-title">Datos del cliente</h2>
        <span className="panel-card__meta">
          {allergies === '' ? NO_ALLERGIES_TEXT : 'Con alergias registradas'}
        </span>
      </div>
      <dl className="client-info">
        <div>
          <dt>Nombre</dt>
          <dd>{record.name}</dd>
        </div>
        <div>
          <dt>Teléfono</dt>
          <dd>{displayPhone(record.phone) || 'Sin teléfono'}</dd>
        </div>
        <div>
          <dt>Correo</dt>
          <dd>{record.email.trim() === '' ? 'Sin correo' : record.email}</dd>
        </div>
        <div>
          <dt>Notas</dt>
          <dd>{record.notes.trim() === '' ? 'Sin notas' : record.notes}</dd>
        </div>
        <div>
          <dt>Alergias</dt>
          <dd>{allergies === '' ? NO_ALLERGIES_TEXT : allergies}</dd>
        </div>
        <div>
          <dt>Identificador</dt>
          <dd>
            <code>{record.id}</code>
          </dd>
        </div>
      </dl>
    </section>
  )
}
