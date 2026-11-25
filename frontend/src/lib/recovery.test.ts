import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, fetchMe } from './api.ts'
import { clearToken, getToken, setToken, TOKEN_KEY } from './session.ts'
import {
  RECOVERY_MESSAGES,
  RESET_NOTICE_KEY,
  isRecoveryPath,
  readEmailVerified,
  readFragmentToken,
  rememberResetNotice,
  requestPasswordReset,
  resendVerification,
  resetPassword,
  shouldShowVerifyBanner,
  takeResetNotice,
  validateForgotEmail,
  validateNewPassword,
  verifyEmail,
  type NoticeStorage,
} from './recovery.ts'

// A real token is 32 random bytes in base64url: 43 characters.
const TOKEN = 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd'

type Call = { url: string; method: string; body: string | undefined; headers: Record<string, string> }

function stubFetch(handler: (call: Call) => Response | Promise<Response>) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      body: typeof init?.body === 'string' ? init.body : undefined,
      headers: (init?.headers ?? {}) as Record<string, string>,
    }
    calls.push(call)
    return handler(call)
  }) as typeof fetch
  return {
    calls,
    restore: () => {
      globalThis.fetch = original
    },
  }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

function memoryStorage(): NoticeStorage & { data: Map<string, string> } {
  const data = new Map<string, string>()
  return {
    data,
    getItem: (key) => data.get(key) ?? null,
    setItem: (key, value) => void data.set(key, value),
    removeItem: (key) => void data.delete(key),
  }
}

// --- exact sentences of the contract (D2, D3, D7, D11, D12) ---

test('the sentences of the contract are exact', () => {
  assert.equal(
    RECOVERY_MESSAGES.forgotAccepted,
    'Si el correo esta registrado, te enviamos un enlace. Revisa tu bandeja.',
  )
  assert.equal(RECOVERY_MESSAGES.resetDone, 'Contrasena actualizada. Inicia sesion.')
  assert.equal(RECOVERY_MESSAGES.resendSent, 'Te enviamos un nuevo correo.')
})

// --- public paths are never redirected to /login on a 401 ---

test('isRecoveryPath matches only the three public paths', () => {
  assert.equal(isRecoveryPath('/recuperar'), true)
  assert.equal(isRecoveryPath('/restablecer'), true)
  assert.equal(isRecoveryPath('/verificar-email'), true)
  assert.equal(isRecoveryPath('/verificar-email/'), true)
  assert.equal(isRecoveryPath('/login'), false)
  assert.equal(isRecoveryPath('/panel'), false)
  assert.equal(isRecoveryPath('/recuperar/otra'), false)
  assert.equal(isRecoveryPath('/invitacion'), false)
})

// --- the token lives in the fragment ---

test('readFragmentToken takes the token out of the fragment and returns the URL without it', () => {
  assert.deepEqual(readFragmentToken(`https://salon.test/restablecer#${TOKEN}`), {
    token: TOKEN,
    url: 'https://salon.test/restablecer',
  })
  assert.deepEqual(readFragmentToken(`http://localhost:5173/verificar-email?x=1#${TOKEN}`), {
    token: TOKEN,
    url: 'http://localhost:5173/verificar-email?x=1',
  })
})

test('readFragmentToken without a fragment returns no token and the same URL', () => {
  assert.deepEqual(readFragmentToken('https://salon.test/restablecer'), {
    token: null,
    url: 'https://salon.test/restablecer',
  })
})

test('readFragmentToken never trusts or leaves behind a fragment that is not a token', () => {
  for (const junk of ['', 'abc', 'con espacios y mas texto 123456', `${TOKEN}#${TOKEN}`, `x${TOKEN}%20`]) {
    const read = readFragmentToken(`https://salon.test/restablecer#${junk}`)
    assert.equal(read.token, null, junk)
    assert.equal(read.url, 'https://salon.test/restablecer', junk)
  }
})

test('readFragmentToken ignores a token placed in the query string', () => {
  const read = readFragmentToken(`https://salon.test/restablecer?token=${TOKEN}`)
  assert.equal(read.token, null)
  assert.equal(read.url, `https://salon.test/restablecer?token=${TOKEN}`)
})

// --- /recuperar: email check ---

test('validateForgotEmail normalizes a good email and rejects the rest', () => {
  assert.deepEqual(validateForgotEmail('  Ana@Salon.MX '), { ok: true, email: 'ana@salon.mx' })
  for (const bad of ['', '   ', 'sin-arroba', 'a@', '@a.com']) {
    const check = validateForgotEmail(bad)
    assert.equal(check.ok, false, bad)
    if (!check.ok) {
      assert.ok(check.message.length > 0)
    }
  }
})

// --- /restablecer: password rule (8..72 BYTES, confirmation) ---

test('validateNewPassword accepts 8..72 bytes that match the confirmation', () => {
  assert.deepEqual(validateNewPassword({ password: '12345678', confirm: '12345678' }), {
    ok: true,
    password: '12345678',
  })
  const max = 'a'.repeat(72)
  assert.equal(validateNewPassword({ password: max, confirm: max }).ok, true)
  // The password is never trimmed.
  assert.deepEqual(validateNewPassword({ password: ' abcdefg ', confirm: ' abcdefg ' }), {
    ok: true,
    password: ' abcdefg ',
  })
})

test('validateNewPassword rejects short, long and mismatching passwords with the field', () => {
  const short = validateNewPassword({ password: '1234567', confirm: '1234567' })
  assert.equal(short.ok, false)
  if (!short.ok) {
    assert.equal(short.field, 'password')
    assert.match(short.message, /8/)
  }
  const long = validateNewPassword({ password: 'a'.repeat(73), confirm: 'a'.repeat(73) })
  assert.equal(long.ok, false)
  if (!long.ok) {
    assert.equal(long.field, 'password')
    assert.match(long.message, /72/)
  }
  const mismatch = validateNewPassword({ password: '12345678', confirm: '12345679' })
  assert.equal(mismatch.ok, false)
  if (!mismatch.ok) {
    assert.equal(mismatch.field, 'confirm')
  }
})

test('validateNewPassword counts bytes, not characters (an accented letter is two)', () => {
  // 36 x «é» = 72 bytes (ok); 37 x «é» = 74 bytes (too long) though only 37 characters.
  assert.equal(validateNewPassword({ password: 'é'.repeat(36), confirm: 'é'.repeat(36) }).ok, true)
  assert.equal(validateNewPassword({ password: 'é'.repeat(37), confirm: 'é'.repeat(37) }).ok, false)
  // 4 x «é» = 8 bytes (ok) though only 4 characters.
  assert.equal(validateNewPassword({ password: 'éééé', confirm: 'éééé' }).ok, true)
})

// --- clients ---

test('requestPasswordReset posts the normalized email without a session and resolves on 202', async () => {
  const stub = stubFetch(() => json({ ok: true }, 202))
  try {
    setToken('stale-session')
    await requestPasswordReset(' Ana@Salon.MX ')
    assert.equal(stub.calls.length, 1)
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/auth\/forgot$/)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? ''), { email: 'ana@salon.mx' })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('requestPasswordReset keeps the 400 sentence of the server verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Email inválido' }, 400))
  try {
    await assert.rejects(
      () => requestPasswordReset('a@b.c'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Email inválido')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('resetPassword posts {token,password} to /auth/reset, resolves on 204 and stores no session', async () => {
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    clearToken()
    const result = await resetPassword(TOKEN, 'nueva-clave-1')
    assert.equal(result, undefined)
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/auth\/reset$/)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? ''), {
      token: TOKEN,
      password: 'nueva-clave-1',
    })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
  }
})

test('resetPassword keeps the server 400 verbatim and never stores a token even if one is returned', async () => {
  let answer: Response = json({ error: 'Enlace invalido o vencido' }, 400)
  const stub = stubFetch(() => answer)
  try {
    clearToken()
    await assert.rejects(
      () => resetPassword(TOKEN, 'nueva-clave-1'),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, 'Enlace invalido o vencido')
        return true
      },
    )
    answer = json({ token: 'a-session-that-must-be-ignored' }, 200)
    await resetPassword(TOKEN, 'nueva-clave-1')
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
  }
})

test('verifyEmail posts the token once to /auth/verify-email and resolves on 204', async () => {
  const stub = stubFetch(() => new Response(null, { status: 204 }))
  try {
    clearToken()
    await verifyEmail(TOKEN)
    assert.equal(stub.calls.length, 1)
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/auth\/verify-email$/)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? ''), { token: TOKEN })
    assert.equal(stub.calls[0]?.headers.Authorization, undefined)
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
  }
})

test('verifyEmail keeps the server sentence verbatim and a 401 never touches the session', async () => {
  const stub = stubFetch(() => json({ error: 'Enlace invalido o vencido' }, 400))
  try {
    setToken('keep-me')
    await assert.rejects(
      () => verifyEmail(TOKEN),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.message, 'Enlace invalido o vencido')
        return true
      },
    )
    assert.equal(getToken(), 'keep-me')
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- resend (D7, D12) ---

test('resendVerification posts to /me/verify-email/resend with the session and says it was sent on 202', async () => {
  const stub = stubFetch(() => json({ ok: true }, 202))
  try {
    setToken('jwt-1')
    const outcome = await resendVerification()
    assert.deepEqual(outcome, { kind: 'sent', message: 'Te enviamos un nuevo correo.' })
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/me\/verify-email\/resend$/)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-1')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('resendVerification shows 409 and 429 verbatim and tells them apart', async () => {
  let answer: Response = json({ error: 'El email ya esta verificado' }, 409)
  const stub = stubFetch(() => answer)
  try {
    setToken('jwt-1')
    assert.deepEqual(await resendVerification(), {
      kind: 'verified',
      message: 'El email ya esta verificado',
    })
    answer = json({ error: 'Espera un minuto antes de pedir otro correo' }, 429)
    assert.deepEqual(await resendVerification(), {
      kind: 'error',
      message: 'Espera un minuto antes de pedir otro correo',
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('resendVerification keeps a 401 as an expired session (the stored token goes away)', async () => {
  const stub = stubFetch(() => json({ error: 'token inválido' }, 401))
  try {
    setToken('jwt-1')
    const outcome = await resendVerification()
    assert.equal(outcome.kind, 'error')
    assert.equal(outcome.message, 'token inválido')
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('resendVerification online only: offline sends nothing and nothing is queued', async () => {
  const stub = stubFetch(() => json({ ok: true }, 202))
  try {
    setToken('jwt-1')
    const outcome = await resendVerification({ online: () => false })
    assert.equal(outcome.kind, 'error')
    assert.equal(outcome.message, RECOVERY_MESSAGES.offline)
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('resendVerification reports a network failure as an error, not as sent', async () => {
  const stub = stubFetch(() => {
    throw new TypeError('fetch failed')
  })
  try {
    setToken('jwt-1')
    const outcome = await resendVerification()
    assert.equal(outcome.kind, 'error')
    assert.match(outcome.message, /conectar/)
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- banner visibility (D12) and the additive /me field ---

test('readEmailVerified treats a missing or odd field as verified (never shows the banner)', () => {
  assert.equal(readEmailVerified({ email_verified: true }), true)
  assert.equal(readEmailVerified({ email_verified: false }), false)
  assert.equal(readEmailVerified({}), true)
  assert.equal(readEmailVerified(null), true)
  assert.equal(readEmailVerified({ email_verified: 'no' }), true)
  assert.equal(readEmailVerified({ email_verified: 0 }), true)
  assert.equal(readEmailVerified({ email_verified: null }), true)
})

test('shouldShowVerifyBanner is true only for an explicit false', () => {
  assert.equal(shouldShowVerifyBanner(false), true)
  assert.equal(shouldShowVerifyBanner(true), false)
  assert.equal(shouldShowVerifyBanner(null), false)
  assert.equal(shouldShowVerifyBanner(undefined), false)
})

test('fetchMe exposes email_verified and keeps the previous shape', async () => {
  const salons = [{ id: 's1', slug: 'aurelia', name: 'Aurelia', role: 'owner' }]
  const user = { id: 'u1', name: 'Ana', email: 'ana@salon.mx' }
  let body: unknown = { user, salons, email_verified: false }
  const stub = stubFetch(() => json(body))
  try {
    setToken('jwt-me')
    const unverified = await fetchMe()
    assert.equal(unverified.email_verified, false)
    assert.deepEqual(unverified.user, user)
    assert.equal(shouldShowVerifyBanner(unverified.email_verified), true)

    body = { user, salons, email_verified: true }
    assert.equal((await fetchMe()).email_verified, true)

    // An older API without the field: verified, so the banner never shows.
    body = { user, salons }
    const old = await fetchMe()
    assert.equal(old.email_verified, true)
    assert.equal(shouldShowVerifyBanner(old.email_verified), false)
    assert.equal(old.salons[0]?.slug, 'aurelia')
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- the notice of the login after a reset: one shot ---

test('the reset notice is shown once: reading it clears it', () => {
  const storage = memoryStorage()
  assert.equal(takeResetNotice(storage), null)
  rememberResetNotice(storage)
  assert.equal(storage.data.has(RESET_NOTICE_KEY), true)
  assert.equal(takeResetNotice(storage), 'Contrasena actualizada. Inicia sesion.')
  assert.equal(storage.data.has(RESET_NOTICE_KEY), false)
  assert.equal(takeResetNotice(storage), null)
})

test('the reset notice never stores or reads a session token and survives a broken storage', () => {
  const storage = memoryStorage()
  rememberResetNotice(storage)
  assert.equal(storage.data.has(TOKEN_KEY), false)
  assert.equal(storage.data.size, 1)

  const broken: NoticeStorage = {
    getItem: () => {
      throw new Error('denied')
    },
    setItem: () => {
      throw new Error('denied')
    },
    removeItem: () => {
      throw new Error('denied')
    },
  }
  assert.doesNotThrow(() => rememberResetNotice(broken))
  assert.equal(takeResetNotice(broken), null)
})

test('the reset notice ignores a value that is not the exact sentence', () => {
  const storage = memoryStorage()
  storage.setItem(RESET_NOTICE_KEY, '<script>alert(1)</script>')
  assert.equal(takeResetNotice(storage), null)
  assert.equal(storage.data.has(RESET_NOTICE_KEY), false)
})
