import {
  actorLabel,
  auditActionLabel,
  formatAuditWhen,
  type AuditEvent,
} from '../lib/audit'

type ActivityListProps = {
  events: readonly AuditEvent[]
  /** IANA zone of the salon: the date and time are shown in it. */
  timeZone: string
}

/**
 * Rows of the activity log, newest first (the order of the server). Each row says
 * what was done in Spanish, who did it (the name saved at that moment), when (zone
 * of the salon) and, small, the id of the affected record. An action this build does
 * not know is shown with its raw name.
 */
export function ActivityList({ events, timeZone }: ActivityListProps) {
  return (
    <ol className="activity-list" aria-label="Actividad del salón">
      {events.map((event) => (
        <li key={event.id} className="activity-row">
          <div className="activity-row__main">
            <p className="activity-row__action">{auditActionLabel(event.action)}</p>
            <p className="activity-row__meta">
              <span>{actorLabel(event)}</span>
              <span aria-hidden="true"> · </span>
              <time dateTime={event.created_at}>{formatAuditWhen(event.created_at, timeZone)}</time>
            </p>
          </div>
          {event.entity_id ? (
            <p className="activity-row__entity">
              <span className="activity-row__entity-label">Registro </span>
              <code>{event.entity_id}</code>
            </p>
          ) : null}
        </li>
      ))}
    </ol>
  )
}
