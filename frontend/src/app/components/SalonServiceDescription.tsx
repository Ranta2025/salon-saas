import { MAX_SERVICE_DESCRIPTION_LENGTH } from '../../lib/salonProfile'

type SalonServiceDescriptionProps = {
  value: string
  onChange: (value: string) => void
  error?: string | null
}

/** Description field of the service form (F16, D2): the text the public page shows. */
export function SalonServiceDescription({ value, onChange, error }: SalonServiceDescriptionProps) {
  return (
    <div className="field service-form__wide">
      <label htmlFor="service-description">Descripción (opcional)</label>
      <textarea
        id="service-description"
        className="textarea"
        rows={3}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        aria-invalid={error ? true : undefined}
        aria-describedby={error ? 'service-description-error' : 'service-description-hint'}
      />
      <p className="field__hint" id="service-description-hint">
        Esto es lo que ve el cliente en tu página pública. Hasta {MAX_SERVICE_DESCRIPTION_LENGTH}{' '}
        caracteres ({[...value].length}); si la dejás vacía, el servicio se muestra sin
        descripción.
      </p>
      {error ? (
        <p className="field__error" id="service-description-error" role="alert">
          {error}
        </p>
      ) : null}
    </div>
  )
}
