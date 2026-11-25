import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, NetworkError } from './api.ts'
import {
  canEditLoyaltyProgram,
  canViewLoyalty,
  canManagePackages,
  canViewPackages,
  isManager,
} from './permissions.ts'
import { clearToken, setToken } from './session.ts'
import {
  createLoyaltyGate,
  createOpAttempt,
  describeLoyaltyError,
  draftFromProgram,
  fetchClientLoyalty,
  fetchLoyaltyProgram,
  grantFingerprint,
  grantLoyaltyReward,
  grantBody,
  LOYALTY_BUSY_MESSAGE,
  LOYALTY_HELP,
  LOYALTY_MESSAGES,
  loyaltyProgress,
  loyaltyServiceOptions,
  nextInLabel,
  normalizeClientLoyalty,
  normalizeLoyaltyProgram,
  normalizeLoyaltyReward,
  previewSentence,
  redeemFingerprint,
  redeemLoyaltyReward,
  redeemRewardBody,
  rewardDateText,
  rewardGrantedDay,
  rewardsAvailableLabel,
  rewardStateLabel,
  saveLoyaltyProgram,
  validateLoyaltyDraft,
  validateRedeemDraft,
  writeLoyalty,
  type ClientLoyalty,
  type LoyaltyDraft,
  type LoyaltyReward,
  type LoyaltyWrite,
} from './loyalty.ts'

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

const RAW_REWARD = {
  id: 'r1',
  reward_number: 1,
  service_id: 'sv1',
  service_name: 'Corte',
  granted_at: '2026-11-03T18:00:00Z',
  expires_at: '2027-02-02T05:59:59Z',
  redeemed_at: null,
  state: 'available',
}

const RAW_LOYALTY = {
  enabled: true,
  visits: 7,
  visits_required: 5,
  rewards_earned: 1,
  rewards_granted: 0,
  rewards_available: 1,
  next_in: 3,
  rewards: [RAW_REWARD],
}

function loyalty(over: Partial<ClientLoyalty> = {}): ClientLoyalty {
  return {
    enabled: true,
    visits: 7,
    visits_required: 5,
    rewards_earned: 1,
    rewards_granted: 0,
    rewards_available: 1,
    next_in: 3,
    rewards: [],
    ...over,
  }
}

function reward(over: Partial<LoyaltyReward> & Pick<LoyaltyReward, 'id'>): LoyaltyReward {
  return {
    reward_number: 1,
    service_id: 'sv1',
    service_name: 'Corte',
    granted_at: '2026-11-03T18:00:00Z',
    expires_at: null,
    redeemed_at: null,
    state: 'available',
    ...over,
  }
}

const DRAFT: LoyaltyDraft = {
  enabled: true,
  visits_required: '10',
  reward_service_id: 'sv1',
  no_expiry: false,
  validity_days: '90',
}

// --- permisos ---

test('loyalty permissions: owner, admin and reception use it; only owner and admin edit the program', () => {
  for (const role of ['owner', 'admin', 'reception'] as const) {
    assert.equal(canViewLoyalty(role), true, role)
  }
  for (const role of ['owner', 'admin'] as const) {
    assert.equal(canEditLoyaltyProgram(role), true, role)
  }
  assert.equal(canEditLoyaltyProgram('reception'), false)
  for (const role of ['professional', null, undefined] as const) {
    assert.equal(canViewLoyalty(role), false, String(role))
    assert.equal(canEditLoyaltyProgram(role), false, String(role))
  }
})

test('the existing permission helpers keep answering what they did', () => {
  assert.equal(isManager('owner'), true)
  assert.equal(isManager('reception'), false)
  assert.equal(canViewPackages('reception'), true)
  assert.equal(canViewPackages('professional'), false)
  assert.equal(canManagePackages('admin'), true)
  assert.equal(canManagePackages('reception'), false)
})

// --- textos verbatim ---

test('the sentences of the contract are verbatim', () => {
  assert.equal(
    LOYALTY_MESSAGES.missingProgram,
    'Faltan datos del programa: activado, visitas, servicio y vigencia',
  )
  assert.equal(LOYALTY_MESSAGES.visitsRange, 'Las visitas necesarias deben ser entre 2 y 50')
  assert.equal(LOYALTY_MESSAGES.invalidService, 'Servicio inválido')
  assert.equal(LOYALTY_MESSAGES.validityRange, 'La vigencia debe ser entre 1 y 3650 días')
  assert.equal(LOYALTY_MESSAGES.noRewards, 'No hay premios disponibles')
  assert.equal(LOYALTY_MESSAGES.notEnabled, 'La fidelidad no está activada')
  assert.equal(LOYALTY_MESSAGES.rewardNotFound, 'Premio no encontrado')
  assert.equal(LOYALTY_MESSAGES.alreadyRedeemed, 'Ese premio ya se usó')
  assert.equal(LOYALTY_MESSAGES.rewardExpired, 'El premio venció')
  assert.equal(LOYALTY_HELP, 'Esto es lo que ve el cliente en tu página pública')
})

// --- programa: normalizador y borrador ---

test('normalizeLoyaltyProgram reads the D4 shape and a never-saved program (enabled false, rest null)', () => {
  assert.deepEqual(
    normalizeLoyaltyProgram({
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_service_name: 'Corte',
      reward_validity_days: 90,
      counting_from: '2026-11-03T18:00:00Z',
    }),
    {
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_service_name: 'Corte',
      reward_validity_days: 90,
      counting_from: '2026-11-03T18:00:00Z',
    },
  )
  assert.deepEqual(
    normalizeLoyaltyProgram({
      enabled: false,
      visits_required: null,
      reward_service_id: null,
      reward_service_name: null,
      reward_validity_days: null,
      counting_from: null,
    }),
    {
      enabled: false,
      visits_required: null,
      reward_service_id: null,
      reward_service_name: null,
      reward_validity_days: null,
      counting_from: null,
    },
  )
})

test('normalizeLoyaltyProgram is null for a non-object and never turns junk numbers into values', () => {
  assert.equal(normalizeLoyaltyProgram(null), null)
  assert.equal(normalizeLoyaltyProgram('x'), null)
  assert.equal(normalizeLoyaltyProgram([]), null)
  const odd = normalizeLoyaltyProgram({ enabled: true, visits_required: 2.5, reward_validity_days: '90' })
  assert.equal(odd?.visits_required, null)
  assert.equal(odd?.reward_validity_days, null)
  // Only a literal `true` enables.
  assert.equal(normalizeLoyaltyProgram({ enabled: 'true' })?.enabled, false)
})

test('draftFromProgram fills the form from the saved program and from none', () => {
  assert.deepEqual(draftFromProgram(null), {
    enabled: false,
    visits_required: '',
    reward_service_id: '',
    no_expiry: false,
    validity_days: '',
  })
  assert.deepEqual(
    draftFromProgram({
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_service_name: 'Corte',
      reward_validity_days: 90,
      counting_from: null,
    }),
    DRAFT,
  )
  assert.deepEqual(
    draftFromProgram({
      enabled: true,
      visits_required: 6,
      reward_service_id: 'sv1',
      reward_service_name: 'Corte',
      reward_validity_days: null,
      counting_from: null,
    }),
    { enabled: true, visits_required: '6', reward_service_id: 'sv1', no_expiry: true, validity_days: '' },
  )
})

test('validateLoyaltyDraft sends the four keys, with validity_days null explicit for no expiry', () => {
  assert.deepEqual(validateLoyaltyDraft(DRAFT), {
    ok: true,
    payload: { enabled: true, visits_required: 10, reward_service_id: 'sv1', reward_validity_days: 90 },
  })
  const noExpiry = validateLoyaltyDraft({ ...DRAFT, no_expiry: true, validity_days: '999' })
  assert.deepEqual(noExpiry, {
    ok: true,
    payload: { enabled: true, visits_required: 10, reward_service_id: 'sv1', reward_validity_days: null },
  })
  assert.equal(
    'reward_validity_days' in (noExpiry.ok ? noExpiry.payload : {}),
    true,
    'the key travels even when it is null',
  )
  // Switched off still carries the four keys.
  const off = validateLoyaltyDraft({ ...DRAFT, enabled: false })
  assert.deepEqual(off.ok ? off.payload : null, {
    enabled: false,
    visits_required: 10,
    reward_service_id: 'sv1',
    reward_validity_days: 90,
  })
})

test('validateLoyaltyDraft says the missing-data sentence on each empty required field', () => {
  const missing = LOYALTY_MESSAGES.missingProgram
  assert.deepEqual(validateLoyaltyDraft({ ...DRAFT, visits_required: ' ' }), {
    ok: false,
    field: 'visits',
    message: missing,
  })
  assert.deepEqual(validateLoyaltyDraft({ ...DRAFT, reward_service_id: '' }), {
    ok: false,
    field: 'service',
    message: missing,
  })
  assert.deepEqual(validateLoyaltyDraft({ ...DRAFT, validity_days: '' }), {
    ok: false,
    field: 'validity',
    message: missing,
  })
})

test('validateLoyaltyDraft checks visits 2..50 as whole numbers at the borders', () => {
  const bad = LOYALTY_MESSAGES.visitsRange
  for (const value of ['1', '51', '0', '-3', '2.5', '1e1', 'diez', '5 visitas']) {
    assert.deepEqual(
      validateLoyaltyDraft({ ...DRAFT, visits_required: value }),
      { ok: false, field: 'visits', message: bad },
      value,
    )
  }
  for (const [value, n] of [
    ['2', 2],
    ['50', 50],
    [' 7 ', 7],
  ] as const) {
    const check = validateLoyaltyDraft({ ...DRAFT, visits_required: value })
    assert.equal(check.ok && check.payload.visits_required, n, value)
  }
})

test('validateLoyaltyDraft checks the validity 1..3650 days unless there is no expiry', () => {
  const bad = LOYALTY_MESSAGES.validityRange
  for (const value of ['0', '3651', '-1', '1.5', 'x']) {
    assert.deepEqual(
      validateLoyaltyDraft({ ...DRAFT, validity_days: value }),
      { ok: false, field: 'validity', message: bad },
      value,
    )
  }
  for (const [value, n] of [
    ['1', 1],
    ['3650', 3650],
  ] as const) {
    const check = validateLoyaltyDraft({ ...DRAFT, validity_days: value })
    assert.equal(check.ok && check.payload.reward_validity_days, n, value)
  }
})

test('loyaltyServiceOptions lists the active services by name and keeps the saved one even if inactive', () => {
  const services = [
    { id: 'b', name: 'Tinte', active: true },
    { id: 'a', name: 'Corte', active: true },
    { id: 'c', name: 'Viejo', active: false },
  ]
  assert.deepEqual(loyaltyServiceOptions(services, null), [
    { id: 'a', label: 'Corte', inactive: false },
    { id: 'b', label: 'Tinte', inactive: false },
  ])
  assert.deepEqual(loyaltyServiceOptions(services, { id: 'c', name: 'Viejo' }), [
    { id: 'a', label: 'Corte', inactive: false },
    { id: 'b', label: 'Tinte', inactive: false },
    { id: 'c', label: 'Viejo (inactivo)', inactive: true },
  ])
  // A saved service that is active does not appear twice.
  assert.equal(loyaltyServiceOptions(services, { id: 'a', name: 'Corte' }).length, 2)
  // A saved service the list does not carry at all is still offered, with its name.
  assert.deepEqual(loyaltyServiceOptions([], { id: 'z', name: 'Zeta' }), [
    { id: 'z', label: 'Zeta (inactivo)', inactive: true },
  ])
})

test('previewSentence is the public sentence of the draft, or null while it is incomplete or off', () => {
  const options = [{ id: 'sv1', label: 'Corte', inactive: false }]
  assert.equal(previewSentence(DRAFT, options), 'Cada 10 visitas, Corte de regalo')
  assert.equal(previewSentence({ ...DRAFT, visits_required: '1' }, options), null)
  assert.equal(previewSentence({ ...DRAFT, visits_required: 'x' }, options), null)
  assert.equal(previewSentence({ ...DRAFT, reward_service_id: '' }, options), null)
  assert.equal(previewSentence({ ...DRAFT, reward_service_id: 'otro' }, options), null)
  // The sentence does not depend on `enabled`: the page says separately that it is off.
  assert.equal(previewSentence({ ...DRAFT, enabled: false }, options), 'Cada 10 visitas, Corte de regalo')
})

// --- clienta: normalizadores ---

test('normalizeClientLoyalty reads the D5 shape', () => {
  assert.deepEqual(normalizeClientLoyalty(RAW_LOYALTY), {
    enabled: true,
    visits: 7,
    visits_required: 5,
    rewards_earned: 1,
    rewards_granted: 0,
    rewards_available: 1,
    next_in: 3,
    rewards: [
      {
        id: 'r1',
        reward_number: 1,
        service_id: 'sv1',
        service_name: 'Corte',
        granted_at: '2026-11-03T18:00:00Z',
        expires_at: '2027-02-02T05:59:59Z',
        redeemed_at: null,
        state: 'available',
      },
    ],
  })
})

test('normalizeClientLoyalty of a salon without program keeps the counters it can read and no rewards', () => {
  const off = normalizeClientLoyalty({
    enabled: false,
    visits: 0,
    visits_required: null,
    rewards_earned: 0,
    rewards_granted: 0,
    rewards_available: 0,
    next_in: null,
    rewards: [],
  })
  assert.deepEqual(off, {
    enabled: false,
    visits: 0,
    visits_required: null,
    rewards_earned: 0,
    rewards_granted: 0,
    rewards_available: 0,
    next_in: null,
    rewards: [],
  })
})

test('normalizeClientLoyalty is null when the counters are not integers (never shown as zero)', () => {
  assert.equal(normalizeClientLoyalty(null), null)
  assert.equal(normalizeClientLoyalty({ ...RAW_LOYALTY, visits: '7' }), null)
  assert.equal(normalizeClientLoyalty({ ...RAW_LOYALTY, visits: 1.5 }), null)
  assert.equal(normalizeClientLoyalty({ ...RAW_LOYALTY, visits: -1 }), null)
  assert.equal(normalizeClientLoyalty({ ...RAW_LOYALTY, rewards_available: undefined }), null)
  assert.equal(normalizeClientLoyalty({ ...RAW_LOYALTY, rewards_available: -2 }), null)
})

test('normalizeLoyaltyReward drops rows without id, number or a known state', () => {
  assert.equal(normalizeLoyaltyReward(RAW_REWARD)?.state, 'available')
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, id: '' }), null)
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, reward_number: 0 }), null)
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, reward_number: 1.5 }), null)
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, state: 'weird' }), null)
  assert.equal(normalizeLoyaltyReward(null), null)
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, state: 'redeemed', redeemed_at: '2026-12-01T10:00:00Z' })?.state, 'redeemed')
  assert.equal(normalizeLoyaltyReward({ ...RAW_REWARD, service_name: null })?.service_name, '')
})

test('normalizeClientLoyalty keeps the order of the server (newest first) and drops only the bad rows', () => {
  const result = normalizeClientLoyalty({
    ...RAW_LOYALTY,
    rewards: [
      { ...RAW_REWARD, id: 'r3', reward_number: 3 },
      { nope: true },
      { ...RAW_REWARD, id: 'r1', reward_number: 1, state: 'redeemed', redeemed_at: '2026-12-01T10:00:00Z' },
    ],
  })
  assert.deepEqual(
    result?.rewards.map((row) => row.id),
    ['r3', 'r1'],
  )
})

// --- presentación ---

test('loyaltyProgress counts the visits of the current cycle: visits modulo N, as integers', () => {
  assert.deepEqual(loyaltyProgress(loyalty({ visits: 7, visits_required: 5, next_in: 3 })), {
    inCycle: 2,
    required: 5,
    percent: 40,
    label: '2 de 5 visitas',
  })
  // An exact multiple starts a new cycle at 0 (the earned reward is counted apart).
  assert.deepEqual(loyaltyProgress(loyalty({ visits: 10, visits_required: 5, next_in: 5 })), {
    inCycle: 0,
    required: 5,
    percent: 0,
    label: '0 de 5 visitas',
  })
  assert.equal(loyaltyProgress(loyalty({ visits: 2, visits_required: 3 }))?.percent, 66)
  assert.equal(loyaltyProgress(loyalty({ visits: 49, visits_required: 50 }))?.percent, 98)
  assert.equal(loyaltyProgress(loyalty({ visits: 0, visits_required: 10 }))?.label, '0 de 10 visitas')
})

test('loyaltyProgress is null without a program (nothing to measure against)', () => {
  assert.equal(loyaltyProgress(loyalty({ enabled: false, visits_required: null })), null)
  assert.equal(loyaltyProgress(loyalty({ visits_required: 0 })), null)
})

test('labels of rewards available and of the visits that are missing', () => {
  assert.equal(rewardsAvailableLabel(0), 'Sin premios disponibles')
  assert.equal(rewardsAvailableLabel(1), '1 premio disponible')
  assert.equal(rewardsAvailableLabel(3), '3 premios disponibles')
  assert.equal(nextInLabel(1), 'Falta 1 visita para el próximo premio')
  assert.equal(nextInLabel(4), 'Faltan 4 visitas para el próximo premio')
  assert.equal(nextInLabel(null), null)
  assert.equal(nextInLabel(0), null)
})

test('rewardStateLabel and rewardDateText print the state and the LOCAL day of the salon', () => {
  assert.deepEqual(rewardStateLabel('available'), { label: 'Disponible', on: true })
  assert.deepEqual(rewardStateLabel('redeemed'), { label: 'Usado', on: false })
  assert.deepEqual(rewardStateLabel('expired'), { label: 'Vencido', on: false })
  // The expiry is the END of the local day: 05:59:59Z is still the 1st at night in Mexico City… and
  // the 2nd in UTC.
  const available = reward({ id: 'r', expires_at: '2027-02-02T05:59:59Z' })
  assert.equal(rewardDateText(available, 'America/Mexico_City'), 'Vence el 2027-02-01')
  assert.equal(rewardDateText(available, 'UTC'), 'Vence el 2027-02-02')
  assert.equal(rewardDateText(reward({ id: 'r' }), 'America/Mexico_City'), 'Sin vencimiento')
  assert.equal(
    rewardDateText(reward({ id: 'r', state: 'expired', expires_at: '2026-11-20T05:59:59Z' }), 'UTC'),
    'Venció el 2026-11-20',
  )
  assert.equal(
    rewardDateText(
      reward({ id: 'r', state: 'redeemed', redeemed_at: '2026-12-01T10:00:00Z', expires_at: '2027-01-01T05:59:59Z' }),
      'UTC',
    ),
    'Usado el 2026-12-01',
  )
  // A bad zone falls back to the default one instead of throwing.
  assert.match(rewardDateText(available, 'Mars/Olympus'), /^Vence el 2027-02-0[12]$/)
})

test('rewardGrantedDay is the LOCAL day it was granted, or a dash when the server sent none', () => {
  const granted = reward({ id: 'r', granted_at: '2026-11-04T03:30:00Z' })
  assert.equal(rewardGrantedDay(granted, 'UTC'), '2026-11-04')
  assert.equal(rewardGrantedDay(granted, 'America/Mexico_City'), '2026-11-03')
  assert.equal(rewardGrantedDay(reward({ id: 'r', granted_at: null }), 'UTC'), '—')
  assert.equal(rewardGrantedDay(reward({ id: 'r', granted_at: 'nope' }), 'UTC'), '—')
})

// --- usar: formulario y cuerpos ---

test('validateRedeemDraft: both fields optional, trimmed and only what has a value travels', () => {
  assert.deepEqual(validateRedeemDraft({ professional_id: '', note: '  ' }), { ok: true, payload: {} })
  assert.deepEqual(validateRedeemDraft({ professional_id: ' u1 ', note: ' ya vino ' }), {
    ok: true,
    payload: { professional_id: 'u1', note: 'ya vino' },
  })
})

test('validateRedeemDraft refuses a note with a line break, a NUL or more than 200 characters', () => {
  for (const note of ['a\nb', 'a\u0000b', 'a\rb']) {
    const check = validateRedeemDraft({ professional_id: '', note })
    assert.equal(check.ok, false)
    assert.equal(check.ok ? '' : check.field, 'note')
  }
  assert.equal(validateRedeemDraft({ professional_id: '', note: 'x'.repeat(200) }).ok, true)
  assert.deepEqual(validateRedeemDraft({ professional_id: '', note: 'x'.repeat(201) }), {
    ok: false,
    field: 'note',
    message: LOYALTY_MESSAGES.noteTooLong,
  })
})

test('grantBody and redeemRewardBody carry the op id only when there is one', () => {
  assert.deepEqual(grantBody('op-1'), { client_op_id: 'op-1' })
  assert.deepEqual(grantBody(undefined), {})
  assert.deepEqual(grantBody('  '), {})
  assert.deepEqual(redeemRewardBody({}, 'op-2'), { client_op_id: 'op-2' })
  assert.deepEqual(redeemRewardBody({ professional_id: 'u1', note: ' hola ' }, 'op-3'), {
    professional_id: 'u1',
    note: 'hola',
    client_op_id: 'op-3',
  })
  assert.deepEqual(redeemRewardBody({ professional_id: '', note: '' }), {})
})

// --- errores ---

test('describeLoyaltyError prints the server sentence verbatim and a network failure as its own kind', () => {
  assert.deepEqual(describeLoyaltyError(new ApiError(409, 'Ese premio ya se usó'), 'x'), {
    kind: 'server',
    message: 'Ese premio ya se usó',
  })
  assert.deepEqual(describeLoyaltyError(new ApiError(403, 'sin permiso'), 'x'), {
    kind: 'server',
    message: 'sin permiso',
  })
  const net = describeLoyaltyError(new NetworkError('boom'), 'x')
  assert.equal(net.kind, 'network')
  assert.match(net.message, /no sabemos si la operación llegó/)
  assert.match(net.message, /No quedó en cola/)
  assert.deepEqual(describeLoyaltyError('???', 'Falló'), { kind: 'server', message: 'Falló' })
})

// --- un id por intento ---

test('createOpAttempt reuses the id for the same attempt and issues a new one after settle', () => {
  let n = 0
  const attempt = createOpAttempt(() => `op-${++n}`)
  const fp = grantFingerprint('c1', 0)
  assert.equal(attempt.idFor(fp), 'op-1')
  assert.equal(attempt.idFor(fp), 'op-1')
  attempt.settle()
  assert.equal(attempt.idFor(fp), 'op-2')
})

test('the grant fingerprint changes when the amount of vouchers granted changed (a new attempt)', () => {
  assert.equal(grantFingerprint('c1', 0), grantFingerprint('c1', 0))
  assert.notEqual(grantFingerprint('c1', 0), grantFingerprint('c1', 1))
  assert.notEqual(grantFingerprint('c1', 0), grantFingerprint('c2', 0))
  assert.equal(redeemFingerprint('c1', 'r1', { note: ' a ' }), redeemFingerprint('c1', 'r1', { note: 'a' }))
  assert.notEqual(redeemFingerprint('c1', 'r1', {}), redeemFingerprint('c1', 'r2', {}))
  assert.notEqual(redeemFingerprint('c1', 'r1', {}), redeemFingerprint('c1', 'r1', { professional_id: 'u1' }))
})

// --- cliente HTTP ---

test('fetchLoyaltyProgram, saveLoyaltyProgram, fetchClientLoyalty, grant and redeem hit the frozen routes', async () => {
  const stub = stubFetch((call) => {
    if (call.url.endsWith('/loyalty/grant')) {
      return json({ reward: RAW_REWARD }, 201)
    }
    if (call.url.endsWith('/redeem')) {
      return json({ reward: { ...RAW_REWARD, state: 'redeemed', redeemed_at: '2026-12-01T10:00:00Z' } })
    }
    if (call.url.endsWith('/loyalty')) {
      return json(RAW_LOYALTY)
    }
    return json({
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_service_name: 'Corte',
      reward_validity_days: null,
      counting_from: '2026-11-03T18:00:00Z',
    })
  })
  try {
    setToken('jwt-loyalty')
    const program = await fetchLoyaltyProgram('s 1')
    assert.equal(program?.reward_validity_days, null)
    const saved = await saveLoyaltyProgram('s 1', {
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_validity_days: null,
    })
    assert.equal(saved?.visits_required, 10)
    const status = await fetchClientLoyalty('s1', 'c/1')
    assert.equal(status?.rewards.length, 1)
    const granted = await grantLoyaltyReward('s1', 'c1', 'op-g')
    assert.equal(granted?.id, 'r1')
    const redeemed = await redeemLoyaltyReward('s1', 'c1', 'r1', { professional_id: 'u1', note: 'x' }, 'op-r')
    assert.equal(redeemed?.state, 'redeemed')

    assert.deepEqual(
      stub.calls.map((call) => `${call.method} ${call.url.replace(/^.*\/api\/v1/, '')}`),
      [
        'GET /salons/s%201/loyalty-program',
        'PUT /salons/s%201/loyalty-program',
        'GET /salons/s1/clients/c%2F1/loyalty',
        'POST /salons/s1/clients/c1/loyalty/grant',
        'POST /salons/s1/clients/c1/loyalty/rewards/r1/redeem',
      ],
    )
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), {
      enabled: true,
      visits_required: 10,
      reward_service_id: 'sv1',
      reward_validity_days: null,
    })
    assert.deepEqual(JSON.parse(stub.calls[3]?.body ?? '{}'), { client_op_id: 'op-g' })
    assert.deepEqual(JSON.parse(stub.calls[4]?.body ?? '{}'), {
      professional_id: 'u1',
      note: 'x',
      client_op_id: 'op-r',
    })
    assert.equal(stub.calls[0]?.body, undefined)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a rejected write keeps the sentence of the server on the thrown error', async () => {
  const stub = stubFetch(() => json({ error: 'Las visitas necesarias deben ser entre 2 y 50' }, 400))
  try {
    setToken('jwt-loyalty')
    await assert.rejects(
      () =>
        saveLoyaltyProgram('s1', {
          enabled: true,
          visits_required: 99,
          reward_service_id: 'sv1',
          reward_validity_days: null,
        }),
      (error: unknown) =>
        error instanceof ApiError &&
        error.status === 400 &&
        error.message === 'Las visitas necesarias deben ser entre 2 y 50',
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- una escritura a la vez, solo en línea ---

const GRANT_WRITE: LoyaltyWrite = { kind: 'grant', clientId: 'c1', clientOpId: 'op-1' }
const PROGRAM_WRITE: LoyaltyWrite = {
  kind: 'program',
  payload: { enabled: true, visits_required: 10, reward_service_id: 'sv1', reward_validity_days: null },
}

test('writeLoyalty offline sends nothing, queues nothing and says so (every kind of write)', async () => {
  const stub = stubFetch(() => json({}))
  try {
    setToken('jwt-loyalty')
    const gate = createLoyaltyGate()
    for (const write of [
      GRANT_WRITE,
      PROGRAM_WRITE,
      { kind: 'redeem', clientId: 'c1', rewardId: 'r1', payload: {}, clientOpId: 'op-2' } as LoyaltyWrite,
    ]) {
      const outcome = await writeLoyalty(gate, 's1', write, () => false)
      assert.equal(outcome.status, 'offline')
      assert.match(outcome.status === 'offline' ? outcome.message : '', /sin conexión/i)
      assert.match(outcome.status === 'offline' ? outcome.message : '', /ni quedó en cola/)
    }
    assert.equal(stub.calls.length, 0)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writeLoyalty on a network error reports `network`, frees the gate and does not retry by itself', async () => {
  const stub = stubFetch(() => {
    throw new TypeError('fetch failed')
  })
  try {
    setToken('jwt-loyalty')
    const gate = createLoyaltyGate()
    const outcome = await writeLoyalty(gate, 's1', GRANT_WRITE, () => true)
    assert.equal(outcome.status, 'network')
    assert.match(outcome.status === 'network' ? outcome.message : '', /Podés reintentar/)
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writeLoyalty keeps the sentence of the server on a 4xx (definitive) and a 5xx is not definitive', async () => {
  let status = 409
  const stub = stubFetch(() =>
    status === 409 ? json({ error: 'No hay premios disponibles' }, 409) : json({ error: 'El servidor respondió 502. Intenta nuevamente.' }, 502),
  )
  try {
    setToken('jwt-loyalty')
    assert.deepEqual(await writeLoyalty(createLoyaltyGate(), 's1', GRANT_WRITE, () => true), {
      status: 'failed',
      message: 'No hay premios disponibles',
      definitive: true,
    })
    status = 502
    assert.deepEqual(await writeLoyalty(createLoyaltyGate(), 's1', GRANT_WRITE, () => true), {
      status: 'failed',
      message: 'El servidor respondió 502. Intenta nuevamente.',
      definitive: false,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writeLoyalty resends the same client_op_id on a retry of the same attempt', async () => {
  let fail = true
  const stub = stubFetch(() => {
    if (fail) {
      fail = false
      throw new TypeError('fetch failed')
    }
    return json({ reward: RAW_REWARD }, 201)
  })
  try {
    setToken('jwt-loyalty')
    const gate = createLoyaltyGate()
    const attempt = createOpAttempt(() => 'op-fixed')
    const fp = grantFingerprint('c1', 0)
    const first = await writeLoyalty(gate, 's1', { kind: 'grant', clientId: 'c1', clientOpId: attempt.idFor(fp) }, () => true)
    assert.equal(first.status, 'network')
    const second = await writeLoyalty(gate, 's1', { kind: 'grant', clientId: 'c1', clientOpId: attempt.idFor(fp) }, () => true)
    assert.equal(second.status, 'saved')
    assert.equal(second.status === 'saved' ? second.reward?.id : null, 'r1')
    const ids = stub.calls.map((call) => (JSON.parse(call.body ?? '{}') as { client_op_id?: string }).client_op_id)
    assert.deepEqual(ids, ['op-fixed', 'op-fixed'])
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a double press sends one request: the gate refuses the second', async () => {
  let release: () => void = () => undefined
  const stub = stubFetch(
    () =>
      new Promise<Response>((resolve) => {
        release = () => resolve(json({ reward: RAW_REWARD }, 201))
      }),
  )
  try {
    setToken('jwt-loyalty')
    const gate = createLoyaltyGate()
    const first = writeLoyalty(gate, 's1', GRANT_WRITE, () => true)
    const second = writeLoyalty(gate, 's1', GRANT_WRITE, () => true)
    assert.deepEqual(await second, { status: 'busy', message: LOYALTY_BUSY_MESSAGE })
    release()
    assert.equal((await first).status, 'saved')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('the loyalty module never touches the offline queue storage', async () => {
  const stub = stubFetch(() => json({ reward: RAW_REWARD }, 201))
  const store = new Map<string, string>()
  const original = (globalThis as { localStorage?: unknown }).localStorage
  ;(globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
  }
  try {
    setToken('jwt-loyalty')
    await writeLoyalty(createLoyaltyGate(), 's1', GRANT_WRITE, () => false)
    await writeLoyalty(createLoyaltyGate(), 's1', GRANT_WRITE, () => true)
    assert.equal([...store.keys()].some((key) => key.includes('offline_queue')), false)
  } finally {
    ;(globalThis as { localStorage?: unknown }).localStorage = original
    stub.restore()
    clearToken()
  }
})
