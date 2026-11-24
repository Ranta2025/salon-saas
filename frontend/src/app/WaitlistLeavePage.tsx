import { useEffect, useRef, useState } from 'react'
import {
  WAITLIST_LEAVE_ACTION,
  WAITLIST_LEAVE_DONE,
  WAITLIST_LEAVE_FALLBACK,
  WAITLIST_LEAVE_HELP,
  WAITLIST_NO_TOKEN_HELP,
  WAITLIST_NO_TOKEN_TITLE,
  describeWaitlistError,
  leaveWaitlist,
  takeWaitlistToken,
} from '../lib/waitlist'

/**
 * The token of the link, read ONCE per document.
 *
 * React 18 renders a component twice in development (StrictMode remounts it) and
 * the second pass finds the address bar already cleaned, so a token kept only in
 * a `useRef` of the mount is lost in `npm run dev` — measured: the page showed
 * «El enlace no trae el código del aviso» with a valid token in the URL. Module
 * state lives as long as the document and never in storage: a reload re-imports
 * the module and starts over.
 */
let documentToken: { token: string | null } | null = null

function readDocumentToken(): string | null {
  if (documentToken === null) {
    documentToken = { token: takeWaitlistToken(window.location.href, window.history) }
  }
  return documentToken.token
}

/**
 * Public `/lista-de-espera` page (contract F7, D5 and D9b): the baja of the
 * avisos from the link of the notification email.
 *
 * The token comes in the FRAGMENT of the link (`/lista-de-espera#<token>`) so it
 * never reaches a server or proxy log:
 *
 * 1. it is read from `location.hash` in the first render and wiped from the
 *    address bar with `history.replaceState` in the same tick, BEFORE any call;
 * 2. once read it lives only in memory (a module value of this page): never
 *    localStorage, never sessionStorage, never a cookie, never the console,
 *    never an error message, never the query string and never the route path;
 * 3. it travels only in the request body (`{token}`);
 * 4. the baja is idempotent: the server answers 200 for a token that was already
 *    left, and `404 {"error":"Aviso no encontrado"}` for an unknown one, which
 *    the page shows verbatim.
 */
export function WaitlistLeavePage() {
  // Read + clean before anything else runs, once per document: the token is
  // never requested twice and never reaches storage or the address bar.
  const token = readDocumentToken()

  const pendingRef = useRef(false)
  const resultRef = useRef<HTMLHeadingElement>(null)
  const [pending, setPending] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    if (done) {
      resultRef.current?.focus()
    }
  }, [done])

  /** Only the server confirms the baja: the page never says it optimistically. */
  const handleLeave = async (): Promise<void> => {
    if (token === null || pendingRef.current) {
      return
    }
    pendingRef.current = true
    setPending(true)
    setError(null)
    try {
      await leaveWaitlist(token)
      setDone(true)
    } catch (err: unknown) {
      setError(describeWaitlistError(err, WAITLIST_LEAVE_FALLBACK).message)
    } finally {
      pendingRef.current = false
      setPending(false)
    }
  }

  return (
    <main className="invite">
      <section className="invite__card" aria-labelledby="waitlist-leave-title">
        <p className="eyebrow">Lista de espera</p>
        <h1 id="waitlist-leave-title">Avisos de la lista de espera</h1>

        {token === null ? (
          <>
            <h2>{WAITLIST_NO_TOKEN_TITLE}</h2>
            <p>{WAITLIST_NO_TOKEN_HELP}</p>
            <p className="invite__actions">
              <a className="btn btn--quiet" href="/">
                Ir al sitio del salón
              </a>
            </p>
          </>
        ) : null}

        {token !== null && done ? (
          <>
            <h2 tabIndex={-1} ref={resultRef}>
              {WAITLIST_LEAVE_DONE}
            </h2>
            <p className="notice notice--success" role="status">
              Ya no vas a recibir más avisos de esta entrada. Si más adelante querés que te
              avisemos de nuevo, podés anotarte otra vez desde la página del salón.
            </p>
            <p className="invite__actions">
              <a className="btn btn--quiet" href="/">
                Ir al sitio del salón
              </a>
            </p>
          </>
        ) : null}

        {token !== null && !done ? (
          <>
            <p>{WAITLIST_LEAVE_HELP}</p>
            {error !== null ? (
              <p className="notice notice--error" role="alert">
                {error}
              </p>
            ) : null}
            <p className="invite__actions">
              <button
                type="button"
                className="btn btn--wine"
                disabled={pending}
                onClick={() => void handleLeave()}
              >
                {pending ? 'Dando de baja…' : WAITLIST_LEAVE_ACTION}
              </button>
            </p>
          </>
        ) : null}
      </section>
    </main>
  )
}
