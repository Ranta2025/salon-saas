import { useEffect, useRef, useState, type FormEvent } from 'react'
import { isBrowserOnline, subscribeOnlineStatus } from '@/lib/connectivity'
import { RecoveryCard } from '@/features/auth/components/RecoveryCard'
import { useFragmentToken } from '@/app/components/RecoveryToken'
import { StarRating } from '../components/StarRating'
import {
  commentCounterText,
  createReviewGate,
  describeReviewLinkError,
  fetchReviewLink,
  MAX_REVIEW_COMMENT_LENGTH,
  REVIEW_MESSAGES,
  reviewDayLabel,
  sanitizeReviewComment,
  submitReview,
  validateReviewDraft,
  type ReviewField,
  type ReviewLink,
} from '../lib/reviews'

type LinkState =
  | { kind: 'loading' }
  | { kind: 'ready'; link: ReviewLink }
  /** Unknown, expired or already used link (the server answers 404; nothing to fill). */
  | { kind: 'invalid'; message: string }
  | { kind: 'error'; message: string }

/** `true` while the browser reports a connection (the page says so and blocks the send). */
function useOnline(): boolean {
  const [online, setOnline] = useState(isBrowserOnline)
  useEffect(() => subscribeOnlineStatus(setOnline), [])
  return online
}

/**
 * Public `/resena` page (contract F13, D4/D5/D8). The token comes in the FRAGMENT of
 * the emailed link: `useFragmentToken` reads it and wipes it from the address bar
 * before any request, and it lives only in memory (never storage, console, message,
 * query or path). The page asks the server for the salon, the service and the turno
 * and then offers an accessible star rating and a comment with a live counter; the
 * token is consumed by the server on the POST, so a second send can never store a
 * second review. A missing token requests nothing, and every server sentence is
 * printed verbatim.
 */
export function ReviewPage() {
  const token = useFragmentToken()
  const online = useOnline()
  const [state, setState] = useState<LinkState>({ kind: 'loading' })
  const [reloadKey, setReloadKey] = useState(0)

  const [rating, setRating] = useState(0)
  const [comment, setComment] = useState('')
  const [fieldError, setFieldError] = useState<{ field: ReviewField; message: string } | null>(null)
  const [serverError, setServerError] = useState<string | null>(null)
  const [sending, setSending] = useState(false)
  const [sent, setSent] = useState(false)

  // The gate lives outside React state: two presses of the same render would both
  // see `sending === false` and send two requests with the same token.
  const gate = useRef(createReviewGate())

  useEffect(() => {
    if (token === null) {
      setState({ kind: 'invalid', message: REVIEW_MESSAGES.noToken })
      return
    }  let active = true
    setState({ kind: 'loading' })
    fetchReviewLink(token)
      .then((link) => {
        if (!active) {
          return
        }
        if (link === null) {
          setState({ kind: 'invalid', message: REVIEW_MESSAGES.noToken })
          return
        }
        setState({ kind: 'ready', link })
      })
      .catch((error: unknown) => {
        if (active) {
          setState(describeReviewLinkError(error, REVIEW_MESSAGES.linkFallback))
        }
      })
    return () => {
      active = false
    }
  }, [token, reloadKey])

  // A fragment without a usable token: nothing is requested at all.
  if (token === null) {
    return (
      <RecoveryCard eyebrow="Reseña" title="Enlace no válido">
        <p className="notice notice--error" role="alert">
          {REVIEW_MESSAGES.noToken}
        </p>
        <p className="review-public__fineprint">
          Las reseñas se piden solas después de cada turno atendido, con un enlace de un solo uso.
        </p>
      </RecoveryCard>
    )
  }

  if (state.kind !== 'ready') {
    if (state.kind === 'loading') {
      return (
        <RecoveryCard eyebrow="Reseña" title="Tu reseña">
          <p className="loading" role="status">
            <span className="loading__dot" aria-hidden="true" />
            Cargando el enlace…
          </p>
        </RecoveryCard>
      )
    }
    return (
      <RecoveryCard eyebrow="Reseña" title={state.kind === 'invalid' ? 'Enlace no válido' : 'No pudimos cargar el enlace'}>
        <p className="notice notice--error" role="alert">
          {state.message}
        </p>
        {state.kind === 'error' ? (
          <p className="recovery__actions">
            <button
              type="button"
              className="btn btn--quiet"
              onClick={() => setReloadKey((key) => key + 1)}
            >
              Reintentar
            </button>
          </p>
        ) : null}
        <p className="review-public__fineprint">
          Las reseñas se piden solas después de cada turno atendido, con un enlace de un solo uso.
        </p>
      </RecoveryCard>
    )
  }

  if (sent) {
    return (
      <RecoveryCard eyebrow="Reseña" title="¡Gracias!">
        <p className="notice notice--success" role="status">
          {REVIEW_MESSAGES.thanks} Tu opinión ya quedó guardada y el enlace no vuelve a usarse.
        </p>
        <p className="review-public__fineprint">
          {state.link.salonName} la va a leer en su panel; después puede mostrarla en su página
          pública, sin tu correo ni tu teléfono.
        </p>
      </RecoveryCard>
    )
  }

  const commentLength = Array.from(comment).length

  const handleSubmit = (event: FormEvent<HTMLFormElement>): void => {
    event.preventDefault()
    if (sending) {
      return
    }
    const checked = validateReviewDraft({ rating, comment })
    if (!checked.ok) {
      setFieldError({ field: checked.field, message: checked.message })
      setServerError(null)
      // The first radio of the group is the real control: focus it, not a wrapper.
      const target =
        checked.field === 'rating'
          ? document.querySelector<HTMLInputElement>('input[name="review-rating"]')
          : document.getElementById('review-comment')
      target?.focus()
      return
    }
    setFieldError(null)
    setServerError(null)
    setSending(true)
    void (async () => {
      try {
        const outcome = await submitReview(gate.current, token, { rating, comment })
        if (outcome.status === 'sent') {
          setSent(true)
          return
        }
        if (outcome.status === 'invalid') {
          setFieldError({ field: outcome.field, message: outcome.message })
          return
        }
        if (outcome.status === 'used') {
          setState({ kind: 'invalid', message: outcome.message })
          return
        }
        if (outcome.status === 'busy') {
          return
        }
        setServerError(outcome.message)
      } finally {
        setSending(false)
      }
    })()
  }

  const { link } = state
  const day = reviewDayLabel(link.startsAt)

  return (
    <RecoveryCard eyebrow="Reseña" title="¿Cómo fue tu visita?">
      <div className="review-public__salon">
        <p className="review-public__name">{link.salonName}</p>
        {link.serviceName !== '' ? <p className="review-public__service">{link.serviceName}</p> : null}
        {day !== '' ? (
          <p className="review-public__when">
            Turno del <time dateTime={link.startsAt}>{day}</time>
          </p>
        ) : null}
      </div>

      {!online ? (
        <p className="notice notice--info" role="status">
          {REVIEW_MESSAGES.offline}
        </p>
      ) : null}

      <form className="review-form" onSubmit={handleSubmit} noValidate>
        <div>
          <StarRating
            value={rating}
            onChange={(next) => {
              setRating(next)
              if (fieldError?.field === 'rating') {
                setFieldError(null)
              }
            }}
            disabled={sending}
            invalid={fieldError?.field === 'rating'}
            errorId={fieldError?.field === 'rating' ? 'review-rating-error' : undefined}
          />
        </div>
        {fieldError?.field === 'rating' ? (
          <p className="field__error" id="review-rating-error" role="alert">
            {fieldError.message}
          </p>
        ) : null}

        <div className="field">
          <label htmlFor="review-comment">Comentario (opcional)</label>
          <textarea
            id="review-comment"
            className="textarea review-form__comment"
            name="comment"
            rows={4}
            value={comment}
            disabled={sending}
            onChange={(event) => {
              // A NUL or a line break is a 400 on the server: it never enters the box.
              setComment(sanitizeReviewComment(event.target.value))
              if (fieldError?.field === 'comment') {
                setFieldError(null)
              }
            }}
            aria-invalid={fieldError?.field === 'comment' ? true : undefined}
            aria-describedby="review-counter review-comment-hint"
          />
          <div className="review-form__foot">
            <p className="field__hint" id="review-comment-hint">
              Hasta {MAX_REVIEW_COMMENT_LENGTH} caracteres, en una sola línea.
            </p>
            <p
              className="review-counter"
              id="review-counter"
              data-over={commentLength > MAX_REVIEW_COMMENT_LENGTH ? 'true' : undefined}
            >
              <span className="visually-hidden">Caracteres: </span>
              {commentCounterText(commentLength)}
            </p>
          </div>
        </div>
        {fieldError?.field === 'comment' ? (
          <p className="field__error" role="alert">
            {fieldError.message}
          </p>
        ) : null}
        {serverError ? (
          <p className="notice notice--error" role="alert">
            {serverError}
          </p>
        ) : null}

        <button type="submit" className="btn btn--wine" disabled={sending}>
          {sending ? 'Enviando…' : 'Enviar reseña'}
        </button>
      </form>

      <p className="review-public__fineprint">
        Tu reseña se publica con tu nombre de pila y la inicial de tu apellido. Nunca mostramos tu
        correo, tu teléfono ni tu turno.
      </p>
    </RecoveryCard>
  )
}
