import { useState } from 'react'
import {
  WAITLIST_REMOVE_NOTE,
  WAITLIST_REMOVE_QUESTION,
  professionalLabel,
  waitlistDayLabel,
  waitlistStatusLabel,
  type WaitlistEntry,
} from '../lib/waitlist'

type WaitlistTableProps = {
  entries: readonly WaitlistEntry[]
  /** Id of the row whose DELETE is in flight. */
  removingId: string | null
  /** Spanish sentence of a failed remove, shown verbatim. */
  error: string | null
  onRemove: (entry: WaitlistEntry) => void
}

/**
 * Table of the waiting list (contract F7, D8): service, professional (or
 * «Cualquiera»), day, name, email, phone and status. «Quitar» asks for a
 * confirmation first, because the person stops receiving the aviso of that day.
 *
 * The day is printed as the salon sent it (`YYYY-MM-DD`, UTC noon) so the table
 * never shifts a day with the zone of whoever reads it. There is no money here.
 */
export function WaitlistTable({ entries, removingId, error, onRemove }: WaitlistTableProps) {
  const [confirmId, setConfirmId] = useState<string | null>(null)

  return (
    <>
      {error !== null ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      <div className="table-wrap">
        <table className="table table--stack">
          <thead>
            <tr>
              <th scope="col">Servicio</th>
              <th scope="col">Profesional</th>
              <th scope="col">Día</th>
              <th scope="col">Nombre</th>
              <th scope="col">Correo</th>
              <th scope="col">Teléfono</th>
              <th scope="col">Estado</th>
              <th scope="col">Acciones</th>
            </tr>
          </thead>
          <tbody>
            {entries.map((entry) => {
              const removing = removingId === entry.id
              return (
                <tr key={entry.id}>
                  <td data-label="Servicio">{entry.service_name}</td>
                  <td data-label="Profesional">{professionalLabel(entry.professional_name)}</td>
                  <td data-label="Día">
                    <time dateTime={entry.date}>{waitlistDayLabel(entry.date)}</time>
                  </td>
                  <td data-label="Nombre">{entry.name}</td>
                  <td data-label="Correo">
                    <a href={`mailto:${entry.email}`}>{entry.email}</a>
                  </td>
                  <td data-label="Teléfono">
                    {entry.phone === null ? (
                      <span className="muted">Sin teléfono</span>
                    ) : (
                      entry.phone
                    )}
                  </td>
                  <td data-label="Estado">
                    <span
                      className={`pill ${entry.status === 'notified' ? 'pill--on' : 'pill--pending'}`}
                    >
                      {waitlistStatusLabel(entry.status)}
                    </span>
                  </td>
                  <td data-label="Acciones" className="table__actions">
                    {confirmId === entry.id ? (
                      <div className="team-confirm">
                        <p className="team-confirm__question">{WAITLIST_REMOVE_QUESTION}</p>
                        <p className="team-confirm__note">{WAITLIST_REMOVE_NOTE}</p>
                        <div className="team-confirm__buttons">
                          <button
                            type="button"
                            className="btn btn--wine"
                            disabled={removing}
                            onClick={() => {
                              setConfirmId(null)
                              onRemove(entry)
                            }}
                          >
                            {removing ? 'Quitando…' : 'Sí, quitar'}
                          </button>
                          <button
                            type="button"
                            className="btn btn--quiet"
                            onClick={() => setConfirmId(null)}
                          >
                            Cancelar
                          </button>
                        </div>
                      </div>
                    ) : (
                      <div className="team-confirm__buttons">
                        <button
                          type="button"
                          className="btn btn--quiet"
                          disabled={removing}
                          onClick={() => setConfirmId(entry.id)}
                        >
                          Quitar
                        </button>
                      </div>
                    )}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>
    </>
  )
}
