import {
  candidateOptionLabel,
  ineligibleSummary,
  type CoverChoice,
} from '../lib/coverAbsence'

type CoverSelectProps = {
  id: string
  label: string
  choices: readonly CoverChoice[]
  value: string
  /** Copy of the empty option; the select always allows "nobody chosen". */
  placeholder?: string
  disabled?: boolean
  onChange: (value: string) => void
}

/**
 * The selector of S-F2d3-5 shared by `/panel/equipo` and the calendar: every
 * professional of the salon with the ones that cannot take the turn disabled and
 * the reason printed under the control (a disabled option cannot be focused, so
 * the reason also lives in text).
 */
export function CoverSelect({
  id,
  label,
  choices,
  value,
  placeholder = 'Elegí una profesional',
  disabled = false,
  onChange,
}: CoverSelectProps) {
  const why = ineligibleSummary(choices)
  return (
    <div className="field cover-field">
      <label htmlFor={id}>{label}</label>
      <select
        id={id}
        className="select"
        value={value}
        disabled={disabled}
        aria-describedby={why === '' ? undefined : `${id}-why`}
        onChange={(event) => onChange(event.target.value)}
      >
        <option value="">{placeholder}</option>
        {choices.map((choice) => (
          <option key={choice.id} value={choice.id} disabled={!choice.eligible}>
            {candidateOptionLabel(choice)}
          </option>
        ))}
      </select>
      {why === '' ? null : (
        <p className="field__hint cover-field__why" id={`${id}-why`}>
          No puede tomarlo: {why}.
        </p>
      )}
    </div>
  )
}
