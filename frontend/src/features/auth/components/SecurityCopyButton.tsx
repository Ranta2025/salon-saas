import { useEffect, useRef, useState } from 'react'

type SecurityCopyButtonProps = {
  /** Text put on the clipboard. */
  text: string
  /** Visible label, e.g. «Copiar secreto». */
  label: string
  /** Accessible name when several buttons share the same visible label. */
  ariaLabel?: string
}

/**
 * Copies a secret to the clipboard. `navigator.clipboard` needs a secure context
 * (https or localhost); the textarea fallback covers the rest. If both fail the
 * person is told to select the text by hand, which is always possible because the
 * secret is shown as plain text next to the button.
 */
async function copyToClipboard(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the textarea fallback
  }
  try {
    const area = document.createElement('textarea')
    area.value = text
    area.setAttribute('readonly', '')
    area.style.position = 'fixed'
    area.style.opacity = '0'
    document.body.appendChild(area)
    area.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(area)
    return ok
  } catch {
    return false
  }
}

export function SecurityCopyButton({ text, label, ariaLabel }: SecurityCopyButtonProps) {
  const [state, setState] = useState<'idle' | 'copied' | 'failed'>('idle')
  const timer = useRef<number | null>(null)

  useEffect(
    () => () => {
      if (timer.current !== null) {
        window.clearTimeout(timer.current)
      }
    },
    [],
  )

  const handleClick = (): void => {
    void copyToClipboard(text).then((ok) => {
      setState(ok ? 'copied' : 'failed')
      if (timer.current !== null) {
        window.clearTimeout(timer.current)
      }
      timer.current = window.setTimeout(() => setState('idle'), 4000)
    })
  }

  return (
    <span className="security-copy">
      <button
        type="button"
        className="btn btn--quiet btn--sm"
        onClick={handleClick}
        aria-label={ariaLabel}
      >
        {label}
      </button>
      <span className="security-copy__status" role="status">
        {state === 'copied' ? 'Copiado' : null}
        {state === 'failed' ? 'No pudimos copiar: seleccioná el texto y copialo a mano.' : null}
      </span>
    </span>
  )
}
