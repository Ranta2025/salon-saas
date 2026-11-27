import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, login, NetworkError } from '../../../lib/api.ts'
import { clearToken, getToken, setToken } from '../../../lib/session.ts'
import {
  challengeFailure,
  completeLogin2fa,
  createSecurityGate,
  disableTwoFactor,
  enableTwoFactor,
  fetchTwoFactorStatus,
  narrowLoginResponse,
  normalizeRecoveryCodes,
  normalizeTwoFactorSetup,
  normalizeTwoFactorStatus,
  normalizeVerificationCode,
  runSecurityAction,
  securityReducer,
  shouldWarnBeforeLeaving,
  startTwoFactorSetup,
  TWO_FACTOR_MESSAGES,
  validateDisableDraft,
  validateSetupCode,
  validateVerificationCode,
  type SecurityView,
} from './twofa.ts'

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
  }) as typeof globalThis.fetch
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
    headers: { 'content-type': 'application/json' },
  })
}

const CODES = ['AAAAA-BBBBB', 'CCCCC-DDDDD', 'EEEEE-FFFFF', 'GGGGG-HHHHH', 'IIIII-JJJJJ', 'KKKKK-LLLLL', 'MMMMM-NNNNN', 'OOOOO-PPPPP']

// --- login response narrowing (D8, D11) ---

test('narrowLoginResponse: a plain token is a session, exactly as before F10', () => {
  assert.deepEqual(narrowLoginResponse({ token: 'jwt-1' }), { kind: 'token', token: 'jwt-1' })
})

test('narrowLoginResponse: two_factor_required + challenge is the second step, never a session', () => {
  assert.deepEqual(narrowLoginResponse({ two_factor_required: true, challenge: 'chal-1' }), {
    kind: 'challenge',
    challenge: 'chal-1',
  })
  // Even if a token rode along, a pending challenge must not become a session.
  assert.deepEqual(
    narrowLoginResponse({ two_factor_required: true, challenge: 'chal-1', token: 'jwt-x' }),
    { kind: 'challenge', challenge: 'chal-1' },
  )
})

test('narrowLoginResponse: malformed answers are rejected, never a silent session', () => {
  for (const raw of [
    null,
    undefined,
    'jwt',
    [],
    {},
    { token: '' },
    { token: 42 },
    { two_factor_required: true },
    { two_factor_required: true, challenge: '' },
    { two_factor_required: true, challenge: 7 },
    { two_factor_required: 'true', challenge: 'chal-1' },
  ]) {
    assert.equal(narrowLoginResponse(raw), null, JSON.stringify(raw))
  }
  // two_factor_required:false falls back to the token path.
  assert.deepEqual(narrowLoginResponse({ two_factor_required: false, token: 'jwt-2' }), {
    kind: 'token',
    token: 'jwt-2',
  })
})

test('login(): the new return type carries the token or the challenge and sends the same body', async () => {
  clearToken()
  const stub = stubFetch((call) =>
    call.body?.includes('dos@salon.mx')
      ? json({ two_factor_required: true, challenge: 'chal-9' })
      : json({ token: 'jwt-9' }),
  )
  try {
    assert.deepEqual(await login('uno@salon.mx', 'secreta'), { kind: 'token', token: 'jwt-9' })
    assert.deepEqual(await login('dos@salon.mx', 'secreta'), {
      kind: 'challenge',
      challenge: 'chal-9',
    })
    assert.equal(stub.calls[0]?.url.endsWith('/api/v1/auth/login'), true)
    assert.equal(stub.calls[0]?.body, '{"email":"uno@salon.mx","password":"secreta"}')
    // Nothing was stored by either call: storing is the screen's decision.
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
  }
})

test('login(): a 200 without token or challenge is an error, a 401 keeps the server sentence', async () => {
  const stub = stubFetch(() => json({}))
  try {
    await assert.rejects(login('a@b.mx', 'x'), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.message, 'La respuesta de inicio de sesión no incluyó un token.')
      return true
    })
  } finally {
    stub.restore()
  }
  const failing = stubFetch(() => json({ error: 'Credenciales invalidas' }, 401))
  try {
    await assert.rejects(login('a@b.mx', 'x'), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, 401)
      assert.equal(error.message, 'Credenciales invalidas')
      return true
    })
  } finally {
    failing.restore()
  }
})

// --- second step of the login ---

test('normalizeVerificationCode: trims and drops spaces, keeps recovery codes in upper case', () => {
  assert.equal(normalizeVerificationCode('  123 456 '), '123456')
  assert.equal(normalizeVerificationCode('\t123456\n'), '123456')
  assert.equal(normalizeVerificationCode(' abcde-fghij '), 'ABCDE-FGHIJ')
  assert.equal(normalizeVerificationCode('   '), '')
})

test('validateVerificationCode: needs something, bounds the length, returns the clean code', () => {
  assert.deepEqual(validateVerificationCode(' 123 456 '), { ok: true, code: '123456' })
  assert.deepEqual(validateVerificationCode('abcde-fghij'), { ok: true, code: 'ABCDE-FGHIJ' })
  assert.deepEqual(validateVerificationCode('   '), {
    ok: false,
    message: TWO_FACTOR_MESSAGES.codeRequired,
  })
  assert.deepEqual(validateVerificationCode('x'.repeat(40)), {
    ok: false,
    message: TWO_FACTOR_MESSAGES.codeTooLong,
  })
})

test('completeLogin2fa: POST /auth/login/2fa {challenge, code} and returns the session token only', async () => {
  clearToken()
  const stub = stubFetch(() => json({ token: 'jwt-final' }))
  try {
    const token = await completeLogin2fa('chal-1', '123456')
    assert.equal(token, 'jwt-final')
    assert.equal(stub.calls.length, 1)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.url.endsWith('/api/v1/auth/login/2fa'), true)
    assert.equal(stub.calls[0]?.body, '{"challenge":"chal-1","code":"123456"}')
    // Storing the token is the screen's job: the client never persists it.
    assert.equal(getToken(), null)
  } finally {
    stub.restore()
  }
})

test('completeLogin2fa: a 200 without token is an error; the 401 sentence is verbatim', async () => {
  const empty = stubFetch(() => json({ two_factor_required: true, challenge: 'again' }))
  try {
    await assert.rejects(completeLogin2fa('chal-1', '123456'), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.message, 'La respuesta de inicio de sesión no incluyó un token.')
      return true
    })
  } finally {
    empty.restore()
  }
  const failing = stubFetch(() => json({ error: 'Codigo incorrecto o desafio vencido' }, 401))
  try {
    await assert.rejects(completeLogin2fa('chal-1', '000000'), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, 401)
      assert.equal(error.message, 'Codigo incorrecto o desafio vencido')
      return true
    })
  } finally {
    failing.restore()
  }
})

test('challengeFailure: 401 returns to the first step with the server message; the rest stays on the step', () => {
  assert.deepEqual(
    challengeFailure(new ApiError(401, 'Codigo incorrecto o desafio vencido')),
    { next: 'credentials', message: 'Codigo incorrecto o desafio vencido' },
  )
  assert.deepEqual(
    challengeFailure(new ApiError(429, 'Demasiados intentos. Proba de nuevo en unos minutos')),
    { next: 'code', message: 'Demasiados intentos. Proba de nuevo en unos minutos' },
  )
  assert.deepEqual(challengeFailure(new ApiError(400, 'body invalido')), {
    next: 'code',
    message: 'body invalido',
  })
  assert.deepEqual(challengeFailure(new NetworkError('Sin red')), { next: 'code', message: 'Sin red' })
  assert.deepEqual(challengeFailure(undefined), {
    next: 'code',
    message: TWO_FACTOR_MESSAGES.loginFallback,
  })
})

// --- /me/2fa clients (D7) ---

test('status, setup, enable and disable: exact routes, methods and bodies', async () => {
  setToken('jwt-me')
  const stub = stubFetch((call) => {
    if (call.url.endsWith('/me/2fa') && call.method === 'GET') {
      return json({ enabled: false })
    }
    if (call.url.endsWith('/me/2fa/setup')) {
      return json({ secret: 'JBSWY3DPEHPK3PXP', otpauth_url: 'otpauth://totp/Salon:a@b.mx?secret=JBSWY3DPEHPK3PXP&issuer=Salon' })
    }
    if (call.url.endsWith('/me/2fa/enable')) {
      return json({ recovery_codes: CODES })
    }
    return new Response(null, { status: 204 })
  })
  try {
    assert.deepEqual(await fetchTwoFactorStatus(), { enabled: false })
    assert.deepEqual(await startTwoFactorSetup(), {
      secret: 'JBSWY3DPEHPK3PXP',
      otpauth_url: 'otpauth://totp/Salon:a@b.mx?secret=JBSWY3DPEHPK3PXP&issuer=Salon',
    })
    assert.deepEqual(await enableTwoFactor('123456'), CODES)
    assert.equal(await disableTwoFactor({ password: 'Clave-1234', code: 'AAAAA-BBBBB' }), undefined)
    assert.deepEqual(
      stub.calls.map((call) => `${call.method} ${call.url.replace(/^.*\/api\/v1/, '')}`),
      ['GET /me/2fa', 'POST /me/2fa/setup', 'POST /me/2fa/enable', 'POST /me/2fa/disable'],
    )
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-me')
    assert.equal(stub.calls[2]?.body, '{"code":"123456"}')
    assert.equal(stub.calls[3]?.body, '{"password":"Clave-1234","code":"AAAAA-BBBBB"}')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('the /me/2fa clients reject malformed answers instead of showing them as data', async () => {
  const bad = stubFetch(() => json({ nope: true }))
  try {
    await assert.rejects(fetchTwoFactorStatus(), ApiError)
    await assert.rejects(startTwoFactorSetup(), ApiError)
    await assert.rejects(enableTwoFactor('123456'), ApiError)
  } finally {
    bad.restore()
  }
})

test('the server sentences of /me/2fa reach the caller verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'La verificacion en dos pasos ya esta activada' }, 409))
  try {
    await assert.rejects(startTwoFactorSetup(), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, 409)
      assert.equal(error.message, 'La verificacion en dos pasos ya esta activada')
      return true
    })
  } finally {
    stub.restore()
  }
})

test('normalizers: status needs a boolean, setup needs both strings, codes are all non-empty strings', () => {
  assert.deepEqual(normalizeTwoFactorStatus({ enabled: true }), { enabled: true })
  assert.equal(normalizeTwoFactorStatus({ enabled: 'yes' }), null)
  assert.equal(normalizeTwoFactorStatus(null), null)
  assert.deepEqual(normalizeTwoFactorSetup({ secret: ' ABC ', otpauth_url: 'otpauth://x' }), {
    secret: 'ABC',
    otpauth_url: 'otpauth://x',
  })
  assert.equal(normalizeTwoFactorSetup({ secret: 'ABC' }), null)
  assert.equal(normalizeTwoFactorSetup({ secret: '', otpauth_url: 'otpauth://x' }), null)
  assert.deepEqual(normalizeRecoveryCodes({ recovery_codes: CODES }), CODES)
  assert.equal(normalizeRecoveryCodes({ recovery_codes: [] }), null)
  assert.equal(normalizeRecoveryCodes({ recovery_codes: ['A', 3] }), null)
  assert.equal(normalizeRecoveryCodes({ recovery_codes: ['A', ' '] }), null)
  assert.equal(normalizeRecoveryCodes({}), null)
})

test('validateSetupCode: exactly six digits (spaces allowed); disable needs password and a code', () => {
  assert.deepEqual(validateSetupCode(' 123 456 '), { ok: true, code: '123456' })
  assert.deepEqual(validateSetupCode('12345'), { ok: false, message: TWO_FACTOR_MESSAGES.setupCodeDigits })
  assert.deepEqual(validateSetupCode('12345a'), { ok: false, message: TWO_FACTOR_MESSAGES.setupCodeDigits })
  assert.deepEqual(validateSetupCode(''), { ok: false, message: TWO_FACTOR_MESSAGES.codeRequired })

  assert.deepEqual(validateDisableDraft({ password: 'Clave-1234', code: ' abcde-fghij ' }), {
    ok: true,
    value: { password: 'Clave-1234', code: 'ABCDE-FGHIJ' },
  })
  assert.deepEqual(validateDisableDraft({ password: '', code: '123456' }), {
    ok: false,
    message: TWO_FACTOR_MESSAGES.passwordRequired,
  })
  assert.deepEqual(validateDisableDraft({ password: 'x', code: '  ' }), {
    ok: false,
    message: TWO_FACTOR_MESSAGES.codeRequired,
  })
  // The password is sent as typed: no trimming of a secret.
  const kept = validateDisableDraft({ password: ' x ', code: '123456' })
  assert.deepEqual(kept, { ok: true, value: { password: ' x ', code: '123456' } })
})

// --- online only, one write at a time (D12) ---

test('runSecurityAction: offline sends nothing and queues nothing', async () => {
  const stub = stubFetch(() => json({}))
  try {
    let ran = false
    const outcome = await runSecurityAction(
      createSecurityGate(),
      async () => {
        ran = true
        return 1
      },
      () => false,
    )
    assert.deepEqual(outcome, { status: 'offline', message: TWO_FACTOR_MESSAGES.offline })
    assert.equal(ran, false)
    assert.equal(stub.calls.length, 0)
  } finally {
    stub.restore()
  }
})

test('runSecurityAction: a second press while the first runs is refused; the gate opens afterwards', async () => {
  const gate = createSecurityGate()
  let release: () => void = () => undefined
  const first = runSecurityAction(
    gate,
    () => new Promise<string>((resolve) => (release = () => resolve('ok'))),
    () => true,
  )
  const second = await runSecurityAction(gate, async () => 'dup', () => true)
  assert.deepEqual(second, { status: 'busy', message: TWO_FACTOR_MESSAGES.busy })
  release()
  assert.deepEqual(await first, { status: 'ok', value: 'ok' })
  assert.deepEqual(await runSecurityAction(gate, async () => 'again', () => true), {
    status: 'ok',
    value: 'again',
  })
})

test('runSecurityAction: server errors verbatim with their status, network errors say nothing was queued', async () => {
  const gate = createSecurityGate()
  const failed = await runSecurityAction(
    gate,
    async () => {
      throw new ApiError(400, 'Codigo incorrecto')
    },
    () => true,
  )
  assert.deepEqual(failed, { status: 'failed', message: 'Codigo incorrecto', httpStatus: 400 })
  const network = await runSecurityAction(
    gate,
    async () => {
      throw new NetworkError('boom')
    },
    () => true,
  )
  assert.deepEqual(network, { status: 'network', message: TWO_FACTOR_MESSAGES.network })
  // The gate is free again after a failure.
  assert.equal(gate.busy(), false)
})

// --- the security page flow (D12) ---

test('securityReducer: inactive -> setup -> recovery codes -> active, codes stay until acknowledged', () => {
  let view: SecurityView = { kind: 'loading' }
  view = securityReducer(view, { type: 'status', enabled: false })
  assert.deepEqual(view, { kind: 'inactive' })
  view = securityReducer(view, { type: 'setup_started', secret: 'S', otpauthUrl: 'U' })
  assert.deepEqual(view, { kind: 'setup', secret: 'S', otpauthUrl: 'U' })
  view = securityReducer(view, { type: 'enabled', codes: CODES })
  assert.deepEqual(view, { kind: 'codes', codes: CODES })
  // A status refresh (or a stale status answer) must NOT hide the one-time codes.
  assert.deepEqual(securityReducer(view, { type: 'status', enabled: true }), view)
  assert.deepEqual(securityReducer(view, { type: 'status', enabled: false }), view)
  // Neither disabling nor cancelling a setup touch them.
  assert.deepEqual(securityReducer(view, { type: 'disable_opened' }), view)
  assert.deepEqual(securityReducer(view, { type: 'setup_cancelled' }), view)
  view = securityReducer(view, { type: 'codes_acknowledged' })
  assert.deepEqual(view, { kind: 'active' })
})

test('securityReducer: acknowledging outside the codes view does nothing; disabling goes back to inactive', () => {
  assert.deepEqual(securityReducer({ kind: 'active' }, { type: 'codes_acknowledged' }), { kind: 'active' })
  assert.deepEqual(securityReducer({ kind: 'inactive' }, { type: 'codes_acknowledged' }), { kind: 'inactive' })
  let view: SecurityView = securityReducer({ kind: 'active' }, { type: 'disable_opened' })
  assert.deepEqual(view, { kind: 'disabling' })
  view = securityReducer(view, { type: 'disable_cancelled' })
  assert.deepEqual(view, { kind: 'active' })
  view = securityReducer({ kind: 'disabling' }, { type: 'disabled' })
  assert.deepEqual(view, { kind: 'inactive' })
  assert.deepEqual(securityReducer({ kind: 'setup', secret: 'S', otpauthUrl: 'U' }, { type: 'setup_cancelled' }), {
    kind: 'inactive',
  })
  // Cancelling/opening events from the wrong view are ignored.
  assert.deepEqual(securityReducer({ kind: 'inactive' }, { type: 'disable_opened' }), { kind: 'inactive' })
  assert.deepEqual(securityReducer({ kind: 'inactive' }, { type: 'disable_cancelled' }), { kind: 'inactive' })
})

test('securityReducer: an enable that arrives without codes lands on active; a status can move setup/active', () => {
  assert.deepEqual(
    securityReducer({ kind: 'setup', secret: 'S', otpauthUrl: 'U' }, { type: 'enabled', codes: [] }),
    { kind: 'active' },
  )
  assert.deepEqual(securityReducer({ kind: 'active' }, { type: 'status', enabled: false }), { kind: 'inactive' })
  assert.deepEqual(securityReducer({ kind: 'inactive' }, { type: 'status', enabled: true }), { kind: 'active' })
  // A pending setup survives a status that still says disabled.
  const setup: SecurityView = { kind: 'setup', secret: 'S', otpauthUrl: 'U' }
  assert.deepEqual(securityReducer(setup, { type: 'status', enabled: false }), setup)
})

test('shouldWarnBeforeLeaving: only while the one-time codes are on screen', () => {
  assert.equal(shouldWarnBeforeLeaving({ kind: 'codes', codes: CODES }), true)
  for (const view of [
    { kind: 'loading' },
    { kind: 'inactive' },
    { kind: 'active' },
    { kind: 'disabling' },
    { kind: 'setup', secret: 'S', otpauthUrl: 'U' },
  ] as const) {
    assert.equal(shouldWarnBeforeLeaving(view), false, view.kind)
  }
})

// --- a 401 of these calls is a wrong password/code, not an expired session ---

test('a 401 of disable and of the login challenge keeps the token and does not signal the shell', async () => {
  const events: string[] = []
  const realWindow = (globalThis as { window?: unknown }).window
  ;(globalThis as { window?: unknown }).window = {
    dispatchEvent: (event: Event) => {
      events.push(event.type)
      return true
    },
  }
  const stub = stubFetch(() => json({ error: 'Credenciales invalidas' }, 401))
  try {
    setToken('jwt-keep')
    await assert.rejects(disableTwoFactor({ password: 'mala', code: '123456' }), (error: unknown) => {
      assert.ok(error instanceof ApiError)
      assert.equal(error.status, 401)
      assert.equal(error.message, 'Credenciales invalidas')
      return true
    })
    assert.equal(getToken(), 'jwt-keep')
    await assert.rejects(completeLogin2fa('chal', '000000'), ApiError)
    assert.equal(getToken(), 'jwt-keep')
    assert.deepEqual(events, [])
    // PRESERVE: any other call still treats a 401 as an expired session.
    await assert.rejects(fetchTwoFactorStatus(), ApiError)
    assert.equal(getToken(), null)
    assert.deepEqual(events, ['salon:unauthorized'])
  } finally {
    stub.restore()
    clearToken()
    ;(globalThis as { window?: unknown }).window = realWindow
  }
})
