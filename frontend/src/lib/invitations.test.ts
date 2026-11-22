import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError } from './api.ts'
import { clearToken, setToken } from './session.ts'
import {
  EMAIL_HINT_FALLBACK,
  INVITATION_MESSAGES,
  LINK_ONCE_NOTE,
  acceptInvitation,
  acceptInvitationSignedIn,
  applyRevoke,
  canInviteRole,
  copyInvitationLink,
  createInvitation,
  dismissLinkOnce,
  emailHintLabel,
  emailIsMember,
  expiryLabel,
  expirySentence,
  fetchInvitations,
  invitableRoles,
  invitationErrorMessage,
  invitationLandingPath,
  invitationRoleLabel,
  isInvitationToken,
  linkDeliveryNotice,
  linkOnceFrom,
  lookupInvitation,
  normalizeCreatedInvitation,
  normalizeInvitationLookup,
  normalizePendingInvitations,
  readInvitationHash,
  revokeInvitation,
  rollbackRevoke,
  validateSignup,
  type PendingInvitation,
} from './invitations.ts'

// A real token is 32 random bytes in base64url: 43 characters.
const TOKEN = 'AbCdEfGhIjKlMnOpQrStUvWxYz0123456789-_abcd'
const ZONE = 'America/Mexico_City'
const NOW = new Date('2026-06-01T18:00:00.000Z')

function invitation(over: Partial<PendingInvitation> = {}): PendingInvitation {
  return { id: 'i1', email: 'luz@mail.com', role: 'professional', expires_at: '2026-11-29T18:00:00.000Z', ...over }
}

// --- the token lives in the fragment, never in the path or the query ---

test('readInvitationHash takes the token out of the fragment and keeps the rest of the URL', () => {
  assert.deepEqual(readInvitationHash(`https://salon.test/invitacion#${TOKEN}`), {
    token: TOKEN,
    url: 'https://salon.test/invitacion',
  })
  assert.deepEqual(readInvitationHash(`http://localhost:5173/invitacion?x=1#${TOKEN}`), {
    token: TOKEN,
    url: 'http://localhost:5173/invitacion?x=1',
  })
})

test('readInvitationHash leaves a URL without a fragment untouched', () => {
  assert.deepEqual(readInvitationHash('https://salon.test/invitacion'), {
    token: null,
    url: 'https://salon.test/invitacion',
  })
})

test('readInvitationHash consumes malformed fragments and never reads them as a token', () => {
  const bad = ['#', '#corto', '#con espacio', '#abc=', '#a%20b', '#a'.repeat(200)]
  for (const hash of bad) {
    const read = readInvitationHash(`https://salon.test/invitacion${hash}`)
    assert.equal(read.token, null, hash)
    // The link is clean even when the token is unusable: no trace is left behind.
    assert.equal(read.url, 'https://salon.test/invitacion', hash)
  }
})

test('readInvitationHash reads the token from the first fragment and keeps extra anchors', () => {
  assert.deepEqual(readInvitationHash(`https://salon.test/invitacion#${TOKEN}#paso-2`), {
    token: TOKEN,
    url: 'https://salon.test/invitacion#paso-2',
  })
  // A token after another fragment is not the invitation slot: never guessed.
  assert.deepEqual(readInvitationHash(`https://salon.test/invitacion#paso-2#${TOKEN}`), {
    token: null,
    url: 'https://salon.test/invitacion#paso-2',
  })
})

test('isInvitationToken only accepts base64url of a plausible length', () => {
  assert.equal(isInvitationToken(TOKEN), true)
  assert.equal(isInvitationToken('a'.repeat(20)), true)
  assert.equal(isInvitationToken('a'.repeat(19)), false)
  assert.equal(isInvitationToken('a'.repeat(129)), false)
  assert.equal(isInvitationToken('abc='), false)
  assert.equal(isInvitationToken('a+b'), false)
  assert.equal(isInvitationToken(''), false)
})

// --- name and password: the rules of the registration endpoint ---

test('validateSignup requires a name and trims it', () => {
  const empty = validateSignup({ name: '   ', password: 'secreta123' })
  assert.equal(empty.ok, false)
  assert.equal(empty.ok === false && empty.field, 'name')

  const ok = validateSignup({ name: '  Luz Marina  ', password: 'secreta123' })
  assert.equal(ok.ok, true)
  assert.equal(ok.ok === true && ok.value.name, 'Luz Marina')
})

test('validateSignup caps the name at 120 characters like the API', () => {
  assert.equal(validateSignup({ name: 'a'.repeat(120), password: 'secreta123' }).ok, true)
  const long = validateSignup({ name: 'a'.repeat(121), password: 'secreta123' })
  assert.equal(long.ok, false)
  assert.equal(long.ok === false && long.field, 'name')
  assert.equal(long.ok === false && long.message, 'El nombre no puede superar los 120 caracteres')
})

test('validateSignup counts the password in bytes, exactly like the API', () => {
  const short = validateSignup({ name: 'Luz', password: '1234567' })
  assert.equal(short.ok, false)
  assert.equal(short.ok === false && short.field, 'password')
  assert.equal(short.ok === false && short.message, 'La contraseña debe tener al menos 8 caracteres')

  assert.equal(validateSignup({ name: 'Luz', password: '12345678' }).ok, true)
  // Four characters, eight bytes: bcrypt (and the API) measure bytes.
  assert.equal(validateSignup({ name: 'Luz', password: 'ññññ' }).ok, true)
  assert.equal(validateSignup({ name: 'Luz', password: 'a'.repeat(72) }).ok, true)

  const long = validateSignup({ name: 'Luz', password: 'a'.repeat(73) })
  assert.equal(long.ok, false)
  assert.equal(long.ok === false && long.message, 'La contraseña no puede superar los 72 caracteres')
})

test('validateSignup never trims the password', () => {
  const checked = validateSignup({ name: 'Luz', password: ' secreta1 ' })
  assert.equal(checked.ok, true)
  assert.equal(checked.ok === true && checked.value.password, ' secreta1 ')
})

// --- the masked email is shown as the server sent it ---

test('emailHintLabel prints the hint exactly as received, never unmasked', () => {
  assert.equal(emailHintLabel('l***@mail.com'), 'l***@mail.com')
  assert.equal(emailHintLabel('  l***@mail.com  '), 'l***@mail.com')
  // Already masked or fully visible: the UI does not invent a mask.
  assert.equal(emailHintLabel('ana@mail.com'), 'ana@mail.com')
  assert.equal(emailHintLabel(''), EMAIL_HINT_FALLBACK)
  assert.equal(emailHintLabel(undefined), EMAIL_HINT_FALLBACK)
})

// --- roles in Spanish ---

test('invitationRoleLabel prefers the server label and falls back to Spanish', () => {
  assert.equal(invitationRoleLabel({ role: 'professional', role_label: 'Profesional' }), 'Profesional')
  assert.equal(invitationRoleLabel({ role: 'admin', role_label: '  Administración ' }), 'Administración')
  assert.equal(invitationRoleLabel({ role: 'professional' }), 'Profesional')
  assert.equal(invitationRoleLabel({ role: 'owner' }), 'Dueña o dueño')
  assert.equal(invitationRoleLabel({ role: 'reception' }), 'Recepción')
  assert.equal(invitationRoleLabel({ role: 'jefe' }), 'Profesional')
})

// --- expiry: "vence el 29 de noviembre" ---

test('expiry text says the local day of the salon, in Spanish', () => {
  const options = { timeZone: ZONE, now: NOW }
  assert.equal(expiryLabel('2026-11-29T18:00:00.000Z', options), 'Vence el 29 de noviembre')
  assert.equal(expirySentence('2026-11-29T18:00:00.000Z', options), 'vence el 29 de noviembre')
  // 02:00 UTC of the 30th is still the 29th in Mexico City.
  assert.equal(expiryLabel('2026-11-30T02:00:00.000Z', options), 'Vence el 29 de noviembre')
})

test('expiry text adds the year when it is not the current one', () => {
  assert.equal(
    expiryLabel('2027-01-02T18:00:00.000Z', { timeZone: ZONE, now: NOW }),
    'Vence el 2 de enero de 2027',
  )
})

test('expiry text is null when the date is unusable', () => {
  assert.equal(expiryLabel('no-es-fecha', { timeZone: ZONE, now: NOW }), null)
  assert.equal(expirySentence('', { timeZone: ZONE, now: NOW }), null)
})

// --- who can invite which role (mirror of domain.AddableBy) ---

test('canInviteRole mirrors AddableBy: owner never shares ownership', () => {
  const cases: [Parameters<typeof canInviteRole>[0], Parameters<typeof canInviteRole>[1], boolean][] = [
    ['owner', 'admin', true],
    ['owner', 'professional', true],
    ['owner', 'reception', true],
    ['owner', 'owner', false],
    ['admin', 'admin', false],
    ['admin', 'professional', true],
    ['admin', 'reception', true],
    ['admin', 'owner', false],
    ['professional', 'professional', false],
    ['reception', 'reception', false],
    [null, 'professional', false],
  ]
  for (const [actor, target, allowed] of cases) {
    assert.equal(canInviteRole(actor, target), allowed, `${String(actor)} -> ${target}`)
  }
})

test('invitableRoles lists only the roles the actor may hand out', () => {
  assert.deepEqual(invitableRoles('owner'), ['professional', 'admin', 'reception'])
  assert.deepEqual(invitableRoles('admin'), ['professional', 'reception'])
  assert.deepEqual(invitableRoles('professional'), [])
  assert.deepEqual(invitableRoles(null), [])
})

test('emailIsMember compares trimmed lowercase emails of the team', () => {
  const members = [{ email: 'Luz@Mail.com' }, { email: '' }]
  assert.equal(emailIsMember(members, '  luz@mail.com '), true)
  assert.equal(emailIsMember(members, 'otra@mail.com'), false)
  assert.equal(emailIsMember(members, ''), false)
  assert.equal(emailIsMember([], 'luz@mail.com'), false)
})

// --- optimistic revoke and its rollback ---

test('applyRevoke removes one invitation without mutating the list', () => {
  const list = [invitation({ id: 'i1' }), invitation({ id: 'i2', expires_at: '2026-12-01T18:00:00.000Z' })]
  const after = applyRevoke(list, 'i1')
  assert.deepEqual(after.map((row) => row.id), ['i2'])
  assert.deepEqual(list.map((row) => row.id), ['i1', 'i2'])
  assert.deepEqual(applyRevoke(after, 'i1').map((row) => row.id), ['i2'])
})

test('rollbackRevoke puts the invitation back in its place, once', () => {
  const list = [
    invitation({ id: 'i2', expires_at: '2026-11-28T18:00:00.000Z' }),
    invitation({ id: 'i1', expires_at: '2026-11-29T18:00:00.000Z' }),
    invitation({ id: 'i3', expires_at: '2026-12-01T18:00:00.000Z' }),
  ]
  const removed = applyRevoke(list, 'i1')
  assert.deepEqual(removed.map((row) => row.id), ['i2', 'i3'])
  const restored = rollbackRevoke(removed, list[1]!)
  assert.deepEqual(restored.map((row) => row.id), ['i2', 'i1', 'i3'])
  // A second rollback never duplicates the row.
  assert.deepEqual(rollbackRevoke(restored, list[1]!).map((row) => row.id), ['i2', 'i1', 'i3'])
  assert.deepEqual(removed.map((row) => row.id), ['i2', 'i3'])
})

// --- the link is shown once and never recovered ---

test('linkOnceFrom keeps the link of the creation answer only while it is shown', () => {
  const created = { invitation: invitation(), link: ' https://salon.test/invitacion#tok ', email_sent: true }
  assert.deepEqual(linkOnceFrom(created), { link: 'https://salon.test/invitacion#tok', emailSent: true })
  // No link in the answer: nothing to show, never an empty box.
  assert.equal(linkOnceFrom({ ...created, link: '   ' }), null)
  assert.equal(dismissLinkOnce(), null)
  assert.equal(LINK_ONCE_NOTE, 'Este enlace se muestra una sola vez')
})

test('linkDeliveryNotice says whether the email left the server', () => {
  assert.equal(linkDeliveryNotice(true), 'Te enviamos el enlace por correo.')
  assert.equal(
    linkDeliveryNotice(false),
    'No pudimos enviar el correo: compartí el enlace por WhatsApp o a mano.',
  )
})

test('copyInvitationLink reports success, failure and a missing clipboard', async () => {
  let written = ''
  const ok = await copyInvitationLink('https://salon.test/invitacion#tok', {
    writeText: async (text: string) => {
      written = text
    },
  })
  assert.equal(ok, true)
  assert.equal(written, 'https://salon.test/invitacion#tok')

  const rejected = await copyInvitationLink('https://salon.test/invitacion#tok', {
    writeText: async () => {
      throw new Error('permiso denegado')
    },
  })
  assert.equal(rejected, false)

  assert.equal(await copyInvitationLink('https://salon.test/invitacion#tok', null), false)
  // Nothing to copy: the clipboard is never touched.
  let touched = false
  assert.equal(
    await copyInvitationLink('   ', {
      writeText: async () => {
        touched = true
      },
    }),
    false,
  )
  assert.equal(touched, false)
})

test('invitationLandingPath sends a professional to Mi agenda', () => {
  assert.equal(invitationLandingPath('professional'), '/panel/mi-agenda')
  assert.equal(invitationLandingPath('owner'), '/panel')
  assert.equal(invitationLandingPath(null), '/panel')
})

test('invitationErrorMessage keeps the server sentence verbatim', () => {
  assert.equal(
    invitationErrorMessage(new ApiError(400, 'No podés invitar con ese rol'), 'Error'),
    'No podés invitar con ese rol',
  )
  assert.equal(invitationErrorMessage(new Error('sin conexión'), 'Error'), 'sin conexión')
  assert.equal(invitationErrorMessage(null, 'No pudimos invitar.'), 'No pudimos invitar.')
  assert.equal(invitationErrorMessage(new Error('  '), 'No pudimos invitar.'), 'No pudimos invitar.')
})

// --- payloads of the contract ---

test('normalizePendingInvitations reads the list and never keeps a link or a token', () => {
  const rows = normalizePendingInvitations({
    invitations: [
      {
        id: 'i1',
        email: 'luz@mail.com',
        role: 'professional',
        expires_at: '2026-11-29T18:00:00.000Z',
        // A server that leaked these fields would never reach the screen.
        link: 'https://salon.test/invitacion#tok',
        token: 'tok',
        token_hash: 'abc',
      },
      { id: '', email: 'sin-id@mail.com' },
      { email: 'sin-id@mail.com' },
    ],
  })
  assert.deepEqual(rows, [
    { id: 'i1', email: 'luz@mail.com', role: 'professional', expires_at: '2026-11-29T18:00:00.000Z' },
  ])
  assert.equal(Object.hasOwn(rows[0]!, 'link'), false)
  assert.equal(Object.hasOwn(rows[0]!, 'token'), false)
})

test('normalizePendingInvitations tolerates a bare array and sorts by expiry', () => {
  const rows = normalizePendingInvitations([
    invitation({ id: 'i2', expires_at: '2026-12-01T18:00:00.000Z' }),
    invitation({ id: 'i1', expires_at: '2026-11-29T18:00:00.000Z' }),
  ])
  assert.deepEqual(rows.map((row) => row.id), ['i1', 'i2'])
  assert.deepEqual(normalizePendingInvitations(null), [])
})

test('normalizeCreatedInvitation reads the invitation, the link and the email flag', () => {
  const created = normalizeCreatedInvitation({
    invitation: {
      id: 'i1',
      email: 'luz@mail.com',
      role: 'professional',
      expires_at: '2026-11-29T18:00:00.000Z',
    },
    link: 'https://salon.test/invitacion#tok',
    email_sent: false,
  })
  assert.equal(created.invitation?.id, 'i1')
  assert.equal(created.link, 'https://salon.test/invitacion#tok')
  assert.equal(created.email_sent, false)

  const noInvitation = normalizeCreatedInvitation({ link: 'https://x/invitacion#t', email_sent: true })
  assert.equal(noInvitation.invitation, null)
  assert.equal(normalizeCreatedInvitation(null).link, '')
})

test('normalizeInvitationLookup reads the public answer and masks nothing', () => {
  const lookup = normalizeInvitationLookup({
    salon_name: 'Aurelia',
    role: 'professional',
    role_label: 'Profesional',
    email_hint: 'l***@mail.com',
    expires_at: '2026-11-29T18:00:00.000Z',
    has_account: true,
  })
  assert.deepEqual(lookup, {
    salon_name: 'Aurelia',
    role: 'professional',
    role_label: 'Profesional',
    email_hint: 'l***@mail.com',
    expires_at: '2026-11-29T18:00:00.000Z',
    has_account: true,
  })
  // An answer without a salon is not a valid invitation.
  assert.equal(normalizeInvitationLookup({ role: 'professional' }), null)
  assert.equal(normalizeInvitationLookup(null), null)
  // `has_account` absent is read as "no account yet": the person is offered the form.
  const fresh = normalizeInvitationLookup({ salon_name: 'Aurelia', role: 'professional' })
  assert.equal(fresh?.has_account, false)
  assert.equal(fresh?.role_label, 'Profesional')
  assert.equal(fresh?.email_hint, EMAIL_HINT_FALLBACK)
})

// --- client of the contract ---

type Call = { url: string; method: string; headers: Record<string, string>; body?: string }

function stubFetch(handler: (call: Call) => Response): {
  calls: Call[]
  restore: () => void
} {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: typeof init?.body === 'string' ? init.body : undefined,
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

test('createInvitation posts the email, the role and only a given attends', async () => {
  const stub = stubFetch(() =>
    json(
      {
        invitation: {
          id: 'i1',
          email: 'luz@mail.com',
          role: 'professional',
          expires_at: '2026-11-29T18:00:00.000Z',
        },
        link: 'https://salon.test/invitacion#tok',
        email_sent: true,
      },
      201,
    ),
  )
  try {
    const created = await createInvitation('s1', { email: '  Luz@Mail.com ', role: 'professional' })
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/invitations$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      email: 'luz@mail.com',
      role: 'professional',
    })
    assert.equal(created.link, 'https://salon.test/invitacion#tok')
    assert.equal(created.email_sent, true)
  } finally {
    stub.restore()
  }

  const withAttends = stubFetch(() => json({ invitation: null, link: 'https://x#t', email_sent: false }, 201))
  try {
    await createInvitation('s1', { email: 'ana@mail.com', role: 'admin', attends: true })
    assert.deepEqual(JSON.parse(withAttends.calls[0]?.body ?? '{}'), {
      email: 'ana@mail.com',
      role: 'admin',
      attends: true,
    })
  } finally {
    withAttends.restore()
  }
})

test('createInvitation keeps the server sentence verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'Esa persona ya es parte del equipo' }, 409))
  try {
    await assert.rejects(
      () => createInvitation('s1', { email: 'luz@mail.com', role: 'professional' }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'Esa persona ya es parte del equipo',
    )
  } finally {
    stub.restore()
  }
})

test('fetchInvitations asks the pending list and revokeInvitation deletes one', async () => {
  setToken('jwt-1')
  const stub = stubFetch((call) =>
    call.method === 'DELETE'
      ? new Response(null, { status: 204 })
      : json({ invitations: [invitation()] }),
  )
  try {
    const rows = await fetchInvitations('s1')
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/salons\/s1\/invitations$/)
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer jwt-1')
    assert.deepEqual(rows.map((row) => row.id), ['i1'])

    await revokeInvitation('s1', 'i1')
    assert.equal(stub.calls[1]?.method, 'DELETE')
    assert.match(stub.calls[1]?.url ?? '', /\/api\/v1\/salons\/s1\/invitations\/i1$/)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('lookupInvitation posts the token in the body without a session', async () => {
  setToken('jwt-1')
  const stub = stubFetch(() =>
    json({
      salon_name: 'Aurelia',
      role: 'professional',
      role_label: 'Profesional',
      email_hint: 'l***@mail.com',
      expires_at: '2026-11-29T18:00:00.000Z',
      has_account: false,
    }),
  )
  try {
    const lookup = await lookupInvitation(TOKEN)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/public\/invitations\/lookup$/)
    // The token travels in the body: never the path, never the query.
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { token: TOKEN })
    assert.equal(stub.calls[0]?.url.includes(TOKEN), false)
    // A public route: the request never carries the session of whoever is logged in.
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
    assert.equal(lookup?.salon_name, 'Aurelia')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('lookupInvitation keeps the single generic message of an invalid token', async () => {
  const stub = stubFetch(() => json({ error: INVITATION_MESSAGES.invalid }, 404))
  try {
    await assert.rejects(
      () => lookupInvitation(TOKEN),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 404 &&
        error.message === 'Invitación inválida o vencida',
    )
  } finally {
    stub.restore()
  }
})

test('acceptInvitation creates the account with the token in the body and returns the session', async () => {
  const stub = stubFetch(() => json({ token: 'jwt-new' }, 201))
  try {
    const jwt = await acceptInvitation(TOKEN, '  Luz Marina ', 'secreta123')
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/public\/invitations\/accept$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      token: TOKEN,
      name: 'Luz Marina',
      password: 'secreta123',
    })
    assert.equal(stub.calls[0]?.headers['Authorization'], undefined)
    assert.equal(jwt, 'jwt-new')
  } finally {
    stub.restore()
  }
})

test('acceptInvitation keeps the 409 sentence of an account that already exists', async () => {
  const stub = stubFetch(() =>
    json({ error: INVITATION_MESSAGES.accountExists }, 409),
  )
  try {
    await assert.rejects(
      () => acceptInvitation(TOKEN, 'Luz', 'secreta123'),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 409 &&
        error.message === 'Ya tenés cuenta: iniciá sesión para aceptar la invitación',
    )
  } finally {
    stub.restore()
  }
})

test('acceptInvitationSignedIn uses the session and keeps the 403 verbatim', async () => {
  setToken('jwt-1')
  const stub = stubFetch(() => json({ error: INVITATION_MESSAGES.otherAccount }, 403))
  try {
    await assert.rejects(
      () => acceptInvitationSignedIn(TOKEN),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 403 &&
        error.message === 'Esta invitación es para otra cuenta',
    )
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.match(stub.calls[0]?.url ?? '', /\/api\/v1\/invitations\/accept$/)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { token: TOKEN })
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer jwt-1')
  } finally {
    stub.restore()
    clearToken()
  }
})
