import { useEffect, useRef, useState } from 'react'
import { fetchMe } from '../../lib/api'
import { resendVerification, shouldShowVerifyBanner } from '@/features/auth/lib/recovery'

type Feedback = { tone: 'success' | 'error'; message: string; verified: boolean }

/**
 * «Verificá tu email» (F15, D12). Shown in the shell only while `/me` says
 * `email_verified === false` (a missing field counts as verified, so an older API
 * never shows it). «Reenviar correo» posts to `/me/verify-email/resend`: 202 says
 * «Te enviamos un nuevo correo.», 409 and 429 show the server sentence verbatim.
 * It informs only: it blocks nothing, sits in the content column (the sidebar and
 * its footer do not move) and nothing is queued offline.
 */
export function EmailVerificationBanner() {
  const [verified, setVerified] = useState<boolean | null>(null)
  const [pending, setPending] = useState(false)
  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const busyRef = useRef(false)

  useEffect(() => {
    let active = true
    fetchMe()
      .then((me) => {
        if (active) {
          setVerified(me.email_verified)
        }
      })
      .catch(() => {
        // Unknown means verified: the banner never nags because a read failed.
      })
    return () => {
      active = false
    }
  }, [])

  if (!shouldShowVerifyBanner(verified)) {
    return null
  }

  const handleResend = async (): Promise<void> => {
    if (busyRef.current) {
      return
    }
    busyRef.current = true
    setPending(true)
    const outcome = await resendVerification()
    setFeedback({
      tone: outcome.kind === 'error' ? 'error' : 'success',
      message: outcome.message,
      verified: outcome.kind === 'verified',
    })
    busyRef.current = false
    setPending(false)
  }

  const alreadyVerified = feedback?.verified === true

  return (
    <section className="verify-banner" aria-label="Verificación de email">
      <div className="verify-banner__text">
        <strong>Verificá tu email</strong>
        <span>
          Te enviamos un correo con un enlace de confirmación. Podés seguir usando el panel con
          normalidad.
        </span>
      </div>
      {alreadyVerified ? null : (
        <button
          type="button"
          className="btn btn--quiet verify-banner__action"
          onClick={() => void handleResend()}
          disabled={pending}
        >
          {pending ? 'Enviando…' : 'Reenviar correo'}
        </button>
      )}
      {feedback ? (
        <p
          className={`verify-banner__feedback verify-banner__feedback--${feedback.tone}`}
          role={feedback.tone === 'error' ? 'alert' : 'status'}
        >
          {feedback.message}
        </p>
      ) : null}
    </section>
  )
}
