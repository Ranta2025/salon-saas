import { useState, type FormEvent } from 'react'
import { describeApiError } from '@/lib/api'
import { isEmailErrorMessage } from '@/lib/email'
import {
  PROFILE_HELP,
  PROFILE_LIMITS,
  validateProfileDraft,
  type ProfileDraft,
  type ProfileField,
  type ProfilePayload,
} from '../lib/salonProfile'

type SalonProfileFormProps = {
  draft: ProfileDraft
  onChange: (draft: ProfileDraft) => void
  /** Sends the validated payload; a rejection is shown as the server sent it. */
  onSave: (payload: ProfilePayload) => Promise<void>
  /** Reception reads the profile; only owner/admin edit (D1). */
  readOnly: boolean
}

type FieldProps = {
  id: ProfileField
  label: string
  value: string
  hint?: string
  error?: string
  readOnly: boolean
  multiline?: boolean
  inputMode?: 'text' | 'email' | 'tel'
  autoComplete?: string
  placeholder?: string
  onChange: (value: string) => void
}

function Field({
  id,
  label,
  value,
  hint,
  error,
  readOnly,
  multiline = false,
  inputMode,
  autoComplete,
  placeholder,
  onChange,
}: FieldProps) {
  const inputId = `profile-${id}`
  const describedBy = [error ? `${inputId}-error` : null, hint ? `${inputId}-hint` : null]
    .filter(Boolean)
    .join(' ')
  const common = {
    id: inputId,
    value,
    readOnly,
    placeholder,
    'aria-invalid': error ? true : undefined,
    'aria-describedby': describedBy === '' ? undefined : describedBy,
  }
  return (
    <div className={`field${multiline ? ' settings-form__wide' : ''}`}>
      <label htmlFor={inputId}>{label}</label>
      {multiline ? (
        <textarea
          {...common}
          className="textarea"
          rows={4}
          onChange={(event) => onChange(event.target.value)}
        />
      ) : (
        <input
          {...common}
          className="input"
          inputMode={inputMode}
          autoComplete={autoComplete}
          onChange={(event) => onChange(event.target.value)}
        />
      )}
      {hint ? (
        <p className="field__hint" id={`${inputId}-hint`}>
          {hint}
        </p>
      ) : null}
      {error ? (
        <p className="field__error" id={`${inputId}-error`} role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}

/**
 * Profile form of «Mi salón» (D1/D7). The six fields are validated with the same
 * limits as the server before sending; whatever the server still rejects is shown
 * as it sent it (the email text next to the email field).
 */
export function SalonProfileForm({ draft, onChange, onSave, readOnly }: SalonProfileFormProps) {
  const [fieldError, setFieldError] = useState<{ field: ProfileField; message: string } | null>(
    null,
  )
  const [formError, setFormError] = useState<string | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)

  const set = (field: ProfileField, value: string): void => {
    setNotice(null)
    if (fieldError?.field === field) {
      setFieldError(null)
    }
    onChange({ ...draft, [field]: value })
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (readOnly || saving) {
      return
    }
    setNotice(null)
    setFormError(null)
    setFieldError(null)
    const result = validateProfileDraft(draft)
    if (!result.ok) {
      setFieldError({ field: result.field, message: result.message })
      document.getElementById(`profile-${result.field}`)?.focus()
      return
    }
    setSaving(true)
    try {
      await onSave(result.payload)
      setNotice('Datos guardados. La página pública ya los muestra.')
    } catch (error: unknown) {
      const { message } = describeApiError(error, 'No pudimos guardar los datos del salón.')
      if (isEmailErrorMessage(message)) {
        setFieldError({ field: 'email', message })
      } else {
        setFormError(message)
      }
    } finally {
      setSaving(false)
    }
  }

  const errorFor = (field: ProfileField): string | undefined =>
    fieldError?.field === field ? fieldError.message : undefined

  return (
    <form
      className="panel-card settings-form"
      onSubmit={(event) => void handleSubmit(event)}
      noValidate
      aria-labelledby="profile-form-title"
    >
      <div className="panel-card__head">
        <h2 id="profile-form-title">Datos del salón</h2>
      </div>
      {readOnly ? (
        <p className="notice notice--info" role="status">
          Tu rol puede ver estos datos, pero los cambia la dueña o la administración.
        </p>
      ) : (
        <p className="field__hint">{PROFILE_HELP.contact}</p>
      )}
      <div className="settings-form__grid">
        <Field
          id="name"
          label="Nombre del salón"
          value={draft.name}
          error={errorFor('name')}
          readOnly={readOnly}
          autoComplete="organization"
          hint={`Hasta ${PROFILE_LIMITS.name} caracteres.`}
          onChange={(value) => set('name', value)}
        />
        <Field
          id="phone"
          label="Teléfono / WhatsApp"
          value={draft.phone}
          error={errorFor('phone')}
          readOnly={readOnly}
          inputMode="tel"
          autoComplete="tel"
          placeholder="+53 5555 1234"
          hint="Con código de país: arma el botón de WhatsApp."
          onChange={(value) => set('phone', value)}
        />
        <Field
          id="address"
          label="Dirección"
          value={draft.address}
          error={errorFor('address')}
          readOnly={readOnly}
          autoComplete="street-address"
          onChange={(value) => set('address', value)}
        />
        <Field
          id="email"
          label="Email"
          value={draft.email}
          error={errorFor('email')}
          readOnly={readOnly}
          inputMode="email"
          autoComplete="email"
          onChange={(value) => set('email', value)}
        />
        <Field
          id="instagram"
          label="Instagram"
          value={draft.instagram}
          error={errorFor('instagram')}
          readOnly={readOnly}
          placeholder="usuario"
          hint="Solo el usuario, sin @."
          onChange={(value) => set('instagram', value)}
        />
        <Field
          id="description"
          label="Descripción"
          value={draft.description}
          error={errorFor('description')}
          readOnly={readOnly}
          multiline
          hint={`${PROFILE_HELP.description} Hasta ${PROFILE_LIMITS.description} caracteres (${[...draft.description].length}).`}
          onChange={(value) => set('description', value)}
        />
      </div>

      {formError ? (
        <p className="notice notice--error" role="alert">
          {formError}
        </p>
      ) : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      {readOnly ? null : (
        <div className="settings-form__actions">
          <button type="submit" className="btn btn--wine" disabled={saving}>
            {saving ? 'Guardando…' : 'Guardar datos'}
          </button>
        </div>
      )}
    </form>
  )
}
