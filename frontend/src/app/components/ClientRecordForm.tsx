import { useEffect, useState, type FormEvent } from 'react'
import {
  EDIT_CLIENT_HINT,
  EDIT_CLIENT_TITLE,
  MAX_ALLERGIES_LENGTH,
  MAX_NOTES_LENGTH,
  validateClientRecordDraft,
  type ClientRecord,
  type ClientRecordField,
  type ClientRecordInput,
} from '../../lib/clientRecord'
import { PhoneInput } from '../../components/PhoneInput'

type ClientRecordFormProps = {
  /** The file as the server returned it: the form always opens from that. */
  record: ClientRecord
  /** A save is running: the button is disabled and a second press does nothing. */
  saving: boolean
  /** Sentence of the server (400/403/404) or of the busy gate, shown verbatim. */
  serverError: string | null
  /** Receives the five keys already checked with the sentences of the API. */
  onSave: (input: ClientRecordInput) => void
}

function draftOf(record: ClientRecord): ClientRecordInput {
  return {
    name: record.name,
    phone: record.phone,
    email: record.email,
    notes: record.notes,
    allergies: record.allergies,
  }
}

/**
 * Edit form of a client file (contract F5, D5). Owner, admin and reception only:
 * the page never renders it for a professional, whose API call would be a 403.
 *
 * The PUT REPLACES the file, so the five keys always travel — an empty `notes` or
 * `allergies` is sent as `""` instead of being left out. Nothing is painted
 * optimistically: the page asks the server for the file again after a save.
 */
export function ClientRecordForm({ record, saving, serverError, onSave }: ClientRecordFormProps) {
  const [draft, setDraft] = useState<ClientRecordInput>(() => draftOf(record))
  const [error, setError] = useState<{ field: ClientRecordField; message: string } | null>(null)

  // A new file (or the file reloaded after a save) always starts from its values.
  useEffect(() => {
    setDraft(draftOf(record))
    setError(null)
  }, [record])

  const fieldError = (field: ClientRecordField): string | null =>
    error?.field === field ? error.message : null

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (saving) {
      return
    }
    const check = validateClientRecordDraft(draft)
    if (!check.ok) {
      setError({ field: check.field, message: check.message })
      return
    }
    setError(null)
    onSave(check.payload)
  }

  return (
    <section className="panel-card" aria-labelledby="client-edit-title">
      <div className="panel-card__head">
        <h2 id="client-edit-title">{EDIT_CLIENT_TITLE}</h2>
        <span className="panel-card__meta">Las alergias son un dato de salud</span>
      </div>
      <p className="field__hint">{EDIT_CLIENT_HINT}</p>

      <form className="service-form" onSubmit={handleSubmit} noValidate>
        <div className="service-form__grid">
          <div className="field">
            <label htmlFor="client-edit-name">Nombre</label>
            <input
              id="client-edit-name"
              className="input"
              value={draft.name}
              onChange={(event) => setDraft({ ...draft, name: event.target.value })}
              autoComplete="name"
              disabled={saving}
              aria-invalid={fieldError('name') ? true : undefined}
              aria-describedby={fieldError('name') ? 'client-edit-name-error' : undefined}
              required
            />
            {fieldError('name') ? (
              <p className="field__error" id="client-edit-name-error" role="alert">
                {fieldError('name')}
              </p>
            ) : null}
          </div>

          <PhoneInput
            id="client-edit-phone"
            label="Teléfono"
            value={draft.phone}
            onChange={(phone) => setDraft({ ...draft, phone })}
            error={fieldError('phone')}
            hint="Opcional. Con el teléfono se vinculan los turnos que coinciden con una sola ficha."
          />

          <div className="field">
            <label htmlFor="client-edit-email">Correo</label>
            <input
              id="client-edit-email"
              className="input"
              type="email"
              value={draft.email}
              onChange={(event) => setDraft({ ...draft, email: event.target.value })}
              autoComplete="email"
              placeholder="clienta@correo.com"
              disabled={saving}
              aria-invalid={fieldError('email') ? true : undefined}
              aria-describedby={fieldError('email') ? 'client-edit-email-error' : undefined}
            />
            {fieldError('email') ? (
              <p className="field__error" id="client-edit-email-error" role="alert">
                {fieldError('email')}
              </p>
            ) : (
              <p className="field__hint">Opcional.</p>
            )}
          </div>

          <div className="field">
            <label htmlFor="client-edit-allergies">Alergias</label>
            <textarea
              id="client-edit-allergies"
              className="textarea"
              rows={2}
              maxLength={MAX_ALLERGIES_LENGTH}
              value={draft.allergies}
              onChange={(event) => setDraft({ ...draft, allergies: event.target.value })}
              disabled={saving}
              aria-invalid={fieldError('allergies') ? true : undefined}
              aria-describedby={
                fieldError('allergies') ? 'client-edit-allergies-error' : 'client-edit-allergies-hint'
              }
              placeholder="Níquel, tintes con amoníaco, látex…"
            />
            {fieldError('allergies') ? (
              <p className="field__error" id="client-edit-allergies-error" role="alert">
                {fieldError('allergies')}
              </p>
            ) : (
              <p className="field__hint" id="client-edit-allergies-hint">
                Hasta {MAX_ALLERGIES_LENGTH} caracteres. Lo ve todo el equipo antes de atender.
              </p>
            )}
          </div>

          <div className="field service-form__wide">
            <label htmlFor="client-edit-notes">Notas</label>
            <textarea
              id="client-edit-notes"
              className="textarea"
              rows={3}
              maxLength={MAX_NOTES_LENGTH}
              value={draft.notes}
              onChange={(event) => setDraft({ ...draft, notes: event.target.value })}
              disabled={saving}
              aria-invalid={fieldError('notes') ? true : undefined}
              aria-describedby={fieldError('notes') ? 'client-edit-notes-error' : 'client-edit-notes-hint'}
            />
            {fieldError('notes') ? (
              <p className="field__error" id="client-edit-notes-error" role="alert">
                {fieldError('notes')}
              </p>
            ) : (
              <p className="field__hint" id="client-edit-notes-hint">
                Preferencias de horario, coloración usada… Hasta {MAX_NOTES_LENGTH} caracteres.
              </p>
            )}
          </div>
        </div>

        {serverError ? (
          <p className="notice notice--error" role="alert">
            {serverError}
          </p>
        ) : null}

        <div className="page-head__actions">
          <button type="submit" className="btn btn--wine" disabled={saving}>
            {saving ? 'Guardando…' : 'Guardar cambios'}
          </button>
        </div>
      </form>
    </section>
  )
}
