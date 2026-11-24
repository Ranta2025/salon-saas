import test from 'node:test'
import assert from 'node:assert/strict'
import { ApiError, API_BASE, NetworkError } from './api.ts'
import {
  canAdjustPackages,
  canManagePackages,
  canViewPackages,
  canViewInventory,
  canManageInventory,
  canViewWaitlist,
  isManager,
} from './permissions.ts'
import { clearToken, setToken } from './session.ts'
import {
  adjustPackageBalance,
  clientPackageStatus,
  createOpAttempt,
  createPackage,
  createPackagesGate,
  describePackageError,
  expiryText,
  fetchClientPackages,
  fetchPackageMovements,
  fetchPackages,
  MAX_PACKAGE_NAME_LENGTH,
  MAX_PACKAGE_NOTE_LENGTH,
  MAX_PACKAGE_PRICE_CENTS,
  MAX_PACKAGE_SESSIONS,
  MAX_VALIDITY_DAYS,
  movementReasonLabel,
  normalizeClientPackage,
  normalizeClientPackages,
  normalizePackage,
  normalizePackageMovements,
  normalizePackages,
  PACKAGE_MESSAGES,
  PACKAGES_BUSY_MESSAGE,
  packageCreateBody,
  packageUpdateBody,
  packageValidityLabel,
  canRedeemPackage,
  redeemBody,
  redeemFingerprint,
  redeemPackageSession,
  sellBody,
  sellFingerprint,
  sellPackage,
  sessionsLabel,
  updatePackage,
  validatePackageAdjustDraft,
  validatePackageDraft,
  validateRedeemDraft,
  validateSellDraft,
  writePackage,
  type ClientPackage,
  type PackageDraft,
  type PackageWrite,
} from './packages.ts'

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

const RAW_PACKAGE = {
  id: 'pk1',
  name: '5 cortes',
  service_id: 'sv1',
  service_name: 'Corte',
  sessions: 5,
  price_cents: 25000,
  validity_days: 90,
  active: true,
  created_at: '2026-11-03T18:00:00Z',
}

const RAW_CLIENT_PACKAGE = {
  id: 'cp1',
  name: '5 cortes',
  service_id: 'sv1',
  service_name: 'Corte',
  sessions_total: 5,
  sessions_left: 3,
  price_cents: 25000,
  purchased_at: '2026-11-03T18:00:00Z',
  expires_at: '2027-02-02T05:59:59Z',
  expired: false,
}

function clientPackage(over: Partial<ClientPackage> & Pick<ClientPackage, 'id'>): ClientPackage {
  return {
    name: '5 cortes',
    service_id: 'sv1',
    service_name: 'Corte',
    sessions_total: 5,
    sessions_left: 3,
    price_cents: 25000,
    purchased_at: '2026-11-03T18:00:00Z',
    expires_at: null,
    expired: false,
    ...over,
  }
}

const EMPTY_DRAFT: PackageDraft = {
  name: '',
  service_id: '',
  sessions: '',
  price: '',
  no_expiry: false,
  validity_days: '',
  active: true,
}

const FULL_DRAFT: PackageDraft = {
  name: ' 5 cortes ',
  service_id: 'sv1',
  sessions: '5',
  price: '250',
  no_expiry: false,
  validity_days: '90',
  active: true,
}

// --- permisos: nuevas funciones, las existentes no cambian ---

test('packages permissions: view/sell/redeem for owner, admin and reception; manage and adjust for owner and admin', () => {
  for (const role of ['owner', 'admin', 'reception'] as const) {
    assert.equal(canViewPackages(role), true, role)
  }
  for (const role of ['owner', 'admin'] as const) {
    assert.equal(canManagePackages(role), true, role)
    assert.equal(canAdjustPackages(role), true, role)
  }
  assert.equal(canManagePackages('reception'), false)
  assert.equal(canAdjustPackages('reception'), false)
  for (const role of ['professional', null, undefined] as const) {
    assert.equal(canViewPackages(role), false, String(role))
    assert.equal(canManagePackages(role), false, String(role))
    assert.equal(canAdjustPackages(role), false, String(role))
  }
})

test('the existing permission helpers keep answering what they did', () => {
  assert.equal(canViewInventory('reception'), true)
  assert.equal(canViewInventory('professional'), false)
  assert.equal(canManageInventory('admin'), true)
  assert.equal(canManageInventory('reception'), false)
  assert.equal(canViewWaitlist('reception'), true)
  assert.equal(canViewWaitlist('professional'), false)
  assert.equal(isManager('owner'), true)
  assert.equal(isManager('reception'), false)
})

// --- textos verbatim del contrato ---

test('the sentences of the contract are verbatim', () => {
  assert.equal(
    PACKAGE_MESSAGES.missingCreate,
    'Faltan datos del paquete: nombre, servicio, sesiones y precio',
  )
  assert.equal(
    PACKAGE_MESSAGES.missingUpdate,
    'Faltan datos del paquete: nombre, precio, vigencia y estado',
  )
  assert.equal(PACKAGE_MESSAGES.duplicateName, 'Ya hay un paquete activo con ese nombre')
  assert.equal(PACKAGE_MESSAGES.invalidPackage, 'Paquete inválido')
  assert.equal(PACKAGE_MESSAGES.notFound, 'Paquete no encontrado')
  assert.equal(PACKAGE_MESSAGES.noSessions, 'No quedan sesiones en este paquete')
  assert.equal(PACKAGE_MESSAGES.expired, 'El paquete venció')
  assert.equal(PACKAGE_MESSAGES.adjustNoteRequired, 'Escribí el motivo del ajuste')
  assert.equal(PACKAGE_MESSAGES.adjustOutOfRange, 'El ajuste deja el saldo fuera de rango')
})

// --- formulario del catálogo ---

test('validatePackageDraft (create) turns the typed price into integer cents and trims the name', () => {
  const check = validatePackageDraft(FULL_DRAFT, 'create')
  assert.deepEqual(check, {
    ok: true,
    mode: 'create',
    payload: {
      name: '5 cortes',
      service_id: 'sv1',
      sessions: 5,
      price_cents: 25000,
      validity_days: 90,
    },
  })
})

test('validatePackageDraft (create) omits validity_days when the package never expires', () => {
  const check = validatePackageDraft({ ...FULL_DRAFT, no_expiry: true, validity_days: '90' }, 'create')
  assert.equal(check.ok, true)
  if (check.ok && check.mode === 'create') {
    assert.equal('validity_days' in check.payload, false)
  }
})

test('validatePackageDraft (create) says the missing-data sentence on each empty required field', () => {
  for (const [patch, field] of [
    [{ name: '   ' }, 'name'],
    [{ service_id: '' }, 'service'],
    [{ sessions: '' }, 'sessions'],
    [{ price: '' }, 'price'],
  ] as const) {
    const check = validatePackageDraft({ ...FULL_DRAFT, ...patch }, 'create')
    assert.deepEqual(check, { ok: false, field, message: PACKAGE_MESSAGES.missingCreate })
  }
})

test('validatePackageDraft refuses names with a NUL, a line break or more than 120 characters', () => {
  for (const name of ['Cor\u0000te', 'Cor\nte', 'Cor\rte']) {
    const check = validatePackageDraft({ ...FULL_DRAFT, name }, 'create')
    assert.deepEqual(check, { ok: false, field: 'name', message: PACKAGE_MESSAGES.invalidText })
  }
  assert.equal(validatePackageDraft({ ...FULL_DRAFT, name: 'a'.repeat(MAX_PACKAGE_NAME_LENGTH) }, 'create').ok, true)
  assert.deepEqual(
    validatePackageDraft({ ...FULL_DRAFT, name: 'a'.repeat(MAX_PACKAGE_NAME_LENGTH + 1) }, 'create'),
    { ok: false, field: 'name', message: PACKAGE_MESSAGES.nameTooLong },
  )
})

test('validatePackageDraft checks sessions 1..100 as whole numbers', () => {
  for (const sessions of ['0', '101', '-1', '2.5', 'abc', '1e2']) {
    const check = validatePackageDraft({ ...FULL_DRAFT, sessions }, 'create')
    assert.equal(check.ok, false, sessions)
    if (!check.ok) {
      assert.equal(check.field, 'sessions')
    }
  }
  assert.equal(validatePackageDraft({ ...FULL_DRAFT, sessions: '1' }, 'create').ok, true)
  assert.equal(validatePackageDraft({ ...FULL_DRAFT, sessions: String(MAX_PACKAGE_SESSIONS) }, 'create').ok, true)
  assert.deepEqual(validatePackageDraft({ ...FULL_DRAFT, sessions: '101' }, 'create'), {
    ok: false,
    field: 'sessions',
    message: PACKAGE_MESSAGES.sessionsRange,
  })
})

test('validatePackageDraft checks the price: integer cents, 0..10000000, no float', () => {
  const free = validatePackageDraft({ ...FULL_DRAFT, price: '0' }, 'create')
  assert.equal(free.ok && free.mode === 'create' ? free.payload.price_cents : null, 0)
  const exact = validatePackageDraft({ ...FULL_DRAFT, price: '100000' }, 'create')
  assert.equal(exact.ok && exact.mode === 'create' ? exact.payload.price_cents : null, MAX_PACKAGE_PRICE_CENTS)
  const cents = validatePackageDraft({ ...FULL_DRAFT, price: '10.10' }, 'create')
  assert.equal(cents.ok && cents.mode === 'create' ? cents.payload.price_cents : null, 1010)
  for (const price of ['abc', '-5', '12.345', '1,2,3']) {
    assert.deepEqual(validatePackageDraft({ ...FULL_DRAFT, price }, 'create'), {
      ok: false,
      field: 'price',
      message: PACKAGE_MESSAGES.priceUnreadable,
    })
  }
  assert.deepEqual(validatePackageDraft({ ...FULL_DRAFT, price: '100000.01' }, 'create'), {
    ok: false,
    field: 'price',
    message: PACKAGE_MESSAGES.priceRange,
  })
})

test('validatePackageDraft checks the validity 1..3650 days unless there is no expiry', () => {
  for (const validity_days of ['', '0', '3651', '-3', '1.5', 'x']) {
    const check = validatePackageDraft({ ...FULL_DRAFT, validity_days }, 'create')
    assert.equal(check.ok, false, JSON.stringify(validity_days))
    if (!check.ok) {
      assert.equal(check.field, 'validity')
    }
  }
  const max = validatePackageDraft({ ...FULL_DRAFT, validity_days: String(MAX_VALIDITY_DAYS) }, 'create')
  assert.equal(max.ok && max.mode === 'create' ? max.payload.validity_days : null, 3650)
  // «Sin vencimiento» ignores whatever is left in the days field.
  assert.equal(validatePackageDraft({ ...FULL_DRAFT, no_expiry: true, validity_days: 'x' }, 'create').ok, true)
})

test('validatePackageDraft (update) sends the four keys with validity_days null for no expiry', () => {
  const noExpiry = validatePackageDraft({ ...FULL_DRAFT, no_expiry: true, active: false }, 'update')
  assert.deepEqual(noExpiry, {
    ok: true,
    mode: 'update',
    payload: { name: '5 cortes', price_cents: 25000, validity_days: null, active: false },
  })
  const withDays = validatePackageDraft(FULL_DRAFT, 'update')
  assert.deepEqual(withDays, {
    ok: true,
    mode: 'update',
    payload: { name: '5 cortes', price_cents: 25000, validity_days: 90, active: true },
  })
})

test('validatePackageDraft (update) does not need the service nor the sessions (they are not editable)', () => {
  const check = validatePackageDraft({ ...FULL_DRAFT, service_id: '', sessions: '' }, 'update')
  assert.equal(check.ok, true)
})

test('validatePackageDraft (update) says the update sentence on an empty name, price or validity', () => {
  for (const [patch, field] of [
    [{ name: '' }, 'name'],
    [{ price: '' }, 'price'],
    [{ validity_days: '' }, 'validity'],
  ] as const) {
    const check = validatePackageDraft({ ...FULL_DRAFT, ...patch }, 'update')
    assert.deepEqual(check, { ok: false, field, message: PACKAGE_MESSAGES.missingUpdate })
  }
})

test('an empty draft is refused on its first field', () => {
  const check = validatePackageDraft(EMPTY_DRAFT, 'create')
  assert.equal(check.ok, false)
})

// --- cuerpos ---

test('packageCreateBody carries only the keys with a value', () => {
  assert.deepEqual(
    packageCreateBody({ name: ' 5 cortes ', service_id: 'sv1', sessions: 5, price_cents: 25000 }),
    { name: '5 cortes', service_id: 'sv1', sessions: 5, price_cents: 25000 },
  )
  assert.deepEqual(
    packageCreateBody({
      name: '5 cortes',
      service_id: 'sv1',
      sessions: 5,
      price_cents: 25000,
      validity_days: 90,
    }),
    { name: '5 cortes', service_id: 'sv1', sessions: 5, price_cents: 25000, validity_days: 90 },
  )
})

test('packageUpdateBody always sends the four keys, validity_days null included, and never service or sessions', () => {
  const body = packageUpdateBody({ name: ' 5 cortes ', price_cents: 25000, validity_days: null, active: true })
  assert.deepEqual(body, { name: '5 cortes', price_cents: 25000, validity_days: null, active: true })
  assert.ok('validity_days' in body)
  assert.equal('service_id' in body, false)
  assert.equal('sessions' in body, false)
})

test('sellBody carries the three keys and the op id only when there is one', () => {
  const payload = { package_id: 'pk1', professional_id: 'u1', method: 'cash' as const }
  assert.deepEqual(sellBody(payload, 'op-1'), {
    package_id: 'pk1',
    professional_id: 'u1',
    method: 'cash',
    client_op_id: 'op-1',
  })
  assert.deepEqual(sellBody(payload, ''), payload)
  assert.deepEqual(sellBody(payload), payload)
})

test('redeemBody omits an empty professional, an empty note and an empty op id', () => {
  assert.deepEqual(redeemBody({}, ''), {})
  assert.deepEqual(redeemBody({ professional_id: '', note: '   ' }), {})
  assert.deepEqual(redeemBody({ professional_id: 'u1', note: ' vino con su mamá ' }, 'op-2'), {
    professional_id: 'u1',
    note: 'vino con su mamá',
    client_op_id: 'op-2',
  })
})

// --- vender, descontar, ajustar: formularios ---

test('validateSellDraft needs the package, the professional and a payment method of the POS', () => {
  assert.deepEqual(validateSellDraft({ package_id: 'pk1', professional_id: 'u1', method: 'card' }), {
    ok: true,
    payload: { package_id: 'pk1', professional_id: 'u1', method: 'card' },
  })
  assert.deepEqual(validateSellDraft({ package_id: '', professional_id: 'u1', method: 'card' }), {
    ok: false,
    field: 'package',
    message: PACKAGE_MESSAGES.sellPackageRequired,
  })
  assert.deepEqual(validateSellDraft({ package_id: 'pk1', professional_id: '', method: 'card' }), {
    ok: false,
    field: 'professional',
    message: PACKAGE_MESSAGES.sellProfessionalRequired,
  })
  assert.deepEqual(validateSellDraft({ package_id: 'pk1', professional_id: 'u1', method: 'bitcoin' }), {
    ok: false,
    field: 'method',
    message: PACKAGE_MESSAGES.sellMethodRequired,
  })
})

test('validateRedeemDraft: both fields optional, the note keeps the 200 / no line break rule', () => {
  assert.deepEqual(validateRedeemDraft({ professional_id: '', note: '' }), { ok: true, payload: {} })
  assert.deepEqual(validateRedeemDraft({ professional_id: 'u1', note: ' ok ' }), {
    ok: true,
    payload: { professional_id: 'u1', note: 'ok' },
  })
  assert.deepEqual(validateRedeemDraft({ professional_id: '', note: 'a\nb' }), {
    ok: false,
    field: 'note',
    message: PACKAGE_MESSAGES.invalidText,
  })
  assert.deepEqual(validateRedeemDraft({ professional_id: '', note: 'a'.repeat(MAX_PACKAGE_NOTE_LENGTH + 1) }), {
    ok: false,
    field: 'note',
    message: PACKAGE_MESSAGES.noteTooLong,
  })
  assert.equal(validateRedeemDraft({ professional_id: '', note: 'a'.repeat(MAX_PACKAGE_NOTE_LENGTH) }).ok, true)
})

test('validatePackageAdjustDraft: delta a whole number other than 0, note required of 1..200', () => {
  assert.deepEqual(validatePackageAdjustDraft({ delta: '+2', note: ' se descontó de más ' }), {
    ok: true,
    payload: { delta: 2, note: 'se descontó de más' },
  })
  assert.deepEqual(validatePackageAdjustDraft({ delta: '-1', note: 'x' }), {
    ok: true,
    payload: { delta: -1, note: 'x' },
  })
  assert.deepEqual(validatePackageAdjustDraft({ delta: '1', note: '   ' }), {
    ok: false,
    field: 'note',
    message: 'Escribí el motivo del ajuste',
  })
  assert.deepEqual(validatePackageAdjustDraft({ delta: '0', note: 'x' }), {
    ok: false,
    field: 'delta',
    message: PACKAGE_MESSAGES.adjustZero,
  })
  for (const delta of ['', 'abc', '1.5', '1,5']) {
    assert.deepEqual(validatePackageAdjustDraft({ delta, note: 'x' }), {
      ok: false,
      field: 'delta',
      message: PACKAGE_MESSAGES.adjustUnreadable,
    })
  }
  assert.deepEqual(validatePackageAdjustDraft({ delta: '101', note: 'x' }), {
    ok: false,
    field: 'delta',
    message: PACKAGE_MESSAGES.adjustRange,
  })
  assert.deepEqual(validatePackageAdjustDraft({ delta: '1', note: 'a\u0000b' }), {
    ok: false,
    field: 'note',
    message: PACKAGE_MESSAGES.invalidText,
  })
  assert.deepEqual(validatePackageAdjustDraft({ delta: '1', note: 'a'.repeat(201) }), {
    ok: false,
    field: 'note',
    message: PACKAGE_MESSAGES.noteTooLong,
  })
})

// --- normalizadores ---

test('normalizePackage reads the catalog row and refuses a price that is not integer cents', () => {
  const pkg = normalizePackage(RAW_PACKAGE)
  assert.deepEqual(pkg, {
    id: 'pk1',
    name: '5 cortes',
    service_id: 'sv1',
    service_name: 'Corte',
    sessions: 5,
    price_cents: 25000,
    validity_days: 90,
    active: true,
    created_at: '2026-11-03T18:00:00Z',
  })
  assert.equal(normalizePackage({ ...RAW_PACKAGE, validity_days: null })?.validity_days, null)
  assert.equal(normalizePackage({ ...RAW_PACKAGE, service_name: undefined })?.service_name, '')
  for (const bad of [
    null,
    'x',
    [],
    { ...RAW_PACKAGE, id: '' },
    { ...RAW_PACKAGE, name: '  ' },
    { ...RAW_PACKAGE, price_cents: 12.5 },
    { ...RAW_PACKAGE, price_cents: '250' },
    { ...RAW_PACKAGE, price_cents: -1 },
    { ...RAW_PACKAGE, sessions: 0 },
    { ...RAW_PACKAGE, validity_days: 1.5 },
  ]) {
    assert.equal(normalizePackage(bad), null, JSON.stringify(bad))
  }
})

test('normalizePackages sorts active first and then by name, and survives a bad body', () => {
  const list = normalizePackages({
    packages: [
      { ...RAW_PACKAGE, id: 'a', name: 'Zeta', active: true },
      { ...RAW_PACKAGE, id: 'b', name: 'Árbol', active: false },
      { ...RAW_PACKAGE, id: 'c', name: 'Beta', active: true },
      { nope: 1 },
    ],
  })
  assert.deepEqual(
    list.map((pkg) => pkg.id),
    ['c', 'a', 'b'],
  )
  assert.deepEqual(normalizePackages(null), [])
  assert.deepEqual(normalizePackages({ packages: 'x' }), [])
  assert.deepEqual(normalizePackages([{ ...RAW_PACKAGE, id: 'only' }]).map((pkg) => pkg.id), ['only'])
})

test('normalizeClientPackage reads the balance row exactly and keeps null for no expiry', () => {
  assert.deepEqual(normalizeClientPackage(RAW_CLIENT_PACKAGE), {
    id: 'cp1',
    name: '5 cortes',
    service_id: 'sv1',
    service_name: 'Corte',
    sessions_total: 5,
    sessions_left: 3,
    price_cents: 25000,
    purchased_at: '2026-11-03T18:00:00Z',
    expires_at: '2027-02-02T05:59:59Z',
    expired: false,
  })
  assert.equal(normalizeClientPackage({ ...RAW_CLIENT_PACKAGE, expires_at: null })?.expires_at, null)
  for (const bad of [
    null,
    { ...RAW_CLIENT_PACKAGE, id: '' },
    { ...RAW_CLIENT_PACKAGE, sessions_left: -1 },
    { ...RAW_CLIENT_PACKAGE, sessions_left: 6 },
    { ...RAW_CLIENT_PACKAGE, sessions_total: 0 },
    { ...RAW_CLIENT_PACKAGE, sessions_left: 1.5 },
    { ...RAW_CLIENT_PACKAGE, price_cents: 1.5 },
  ]) {
    assert.equal(normalizeClientPackage(bad), null, JSON.stringify(bad))
  }
  // `expired` is the server's call: only a literal true counts.
  assert.equal(normalizeClientPackage({ ...RAW_CLIENT_PACKAGE, expired: 'true' })?.expired, false)
  assert.equal(normalizeClientPackage({ ...RAW_CLIENT_PACKAGE, expired: true })?.expired, true)
})

test('normalizeClientPackages keeps the server order but puts the usable ones first', () => {
  const list = normalizeClientPackages({
    packages: [
      { ...RAW_CLIENT_PACKAGE, id: 'old', expired: true },
      { ...RAW_CLIENT_PACKAGE, id: 'empty', sessions_left: 0 },
      { ...RAW_CLIENT_PACKAGE, id: 'u1' },
      { ...RAW_CLIENT_PACKAGE, id: 'u2' },
    ],
  })
  assert.deepEqual(
    list.map((pkg) => pkg.id),
    ['u1', 'u2', 'old', 'empty'],
  )
  assert.deepEqual(normalizeClientPackages(undefined), [])
})

test('normalizePackageMovements keeps the server order and drops what is not a movement', () => {
  const list = normalizePackageMovements({
    movements: [
      { id: 'm3', delta: -1, reason: 'redeem', note: null, professional_id: 'u1', created_at: '2026-11-05T10:00:00Z' },
      { id: 'm2', delta: 2, reason: 'adjust', note: 'se descontó de más', professional_id: null, created_at: '2026-11-04T10:00:00Z' },
      { id: 'm1', delta: 5, reason: 'purchase', note: null, professional_id: null, created_at: '2026-11-03T10:00:00Z' },
      { id: 'zero', delta: 0, reason: 'adjust' },
      { id: 'float', delta: 0.5, reason: 'adjust' },
      { id: 'odd', delta: 1, reason: 'gift' },
      { delta: 1, reason: 'adjust' },
    ],
  })
  assert.deepEqual(
    list.map((movement) => movement.id),
    ['m3', 'm2', 'm1'],
  )
  assert.deepEqual(list[0], {
    id: 'm3',
    delta: -1,
    reason: 'redeem',
    note: '',
    professional_id: 'u1',
    created_at: '2026-11-05T10:00:00Z',
  })
  assert.equal(list[1]?.note, 'se descontó de más')
  assert.equal(list[1]?.professional_id, null)
  assert.deepEqual(normalizePackageMovements(null), [])
})

// --- textos de presentación ---

test('sessionsLabel says how many sessions are left of the total', () => {
  assert.equal(sessionsLabel(clientPackage({ id: 'x', sessions_left: 3, sessions_total: 5 })), '3 de 5 sesiones')
  assert.equal(sessionsLabel(clientPackage({ id: 'x', sessions_left: 1, sessions_total: 1 })), '1 de 1 sesión')
  assert.equal(sessionsLabel(clientPackage({ id: 'x', sessions_left: 0, sessions_total: 5 })), '0 de 5 sesiones')
})

test('clientPackageStatus: Vencido wins, then Sin sesiones, then Vigente', () => {
  assert.equal(clientPackageStatus(clientPackage({ id: 'x', expired: true })).label, 'Vencido')
  assert.equal(clientPackageStatus(clientPackage({ id: 'x', expired: true, sessions_left: 0 })).label, 'Vencido')
  assert.equal(clientPackageStatus(clientPackage({ id: 'x', sessions_left: 0 })).label, 'Sin sesiones')
  assert.equal(clientPackageStatus(clientPackage({ id: 'x' })).label, 'Vigente')
})

test('canRedeemPackage only with sessions left and not expired (the server decides on the rest)', () => {
  assert.equal(canRedeemPackage(clientPackage({ id: 'x' })), true)
  assert.equal(canRedeemPackage(clientPackage({ id: 'x', expired: true })), false)
  assert.equal(canRedeemPackage(clientPackage({ id: 'x', sessions_left: 0 })), false)
})

test('expiryText prints the local day of the salon, never a shifted one', () => {
  // 2027-02-02 05:59:59Z is still Feb 1st 23:59:59 in Mexico City.
  assert.equal(
    expiryText(clientPackage({ id: 'x', expires_at: '2027-02-02T05:59:59Z' }), 'America/Mexico_City'),
    'Vence el 2027-02-01',
  )
  assert.equal(
    expiryText(clientPackage({ id: 'x', expires_at: '2027-02-02T05:59:59Z', expired: true }), 'America/Mexico_City'),
    'Venció el 2027-02-01',
  )
  assert.equal(expiryText(clientPackage({ id: 'x', expires_at: null }), 'America/Mexico_City'), 'Sin vencimiento')
  // An unusable zone falls back instead of throwing inside a render.
  assert.equal(
    typeof expiryText(clientPackage({ id: 'x', expires_at: '2027-02-02T05:59:59Z' }), 'Nope/Zone'),
    'string',
  )
})

test('packageValidityLabel and movementReasonLabel', () => {
  assert.equal(packageValidityLabel(null), 'Sin vencimiento')
  assert.equal(packageValidityLabel(1), '1 día')
  assert.equal(packageValidityLabel(90), '90 días')
  assert.equal(movementReasonLabel('purchase'), 'Compra')
  assert.equal(movementReasonLabel('redeem'), 'Sesión descontada')
  assert.equal(movementReasonLabel('adjust'), 'Ajuste')
})

// --- errores: verbatim; la red es un aviso, nunca una cola ---

test('describePackageError prints the sentence of the server verbatim', () => {
  for (const [status, message] of [
    [400, PACKAGE_MESSAGES.invalidPackage],
    [404, PACKAGE_MESSAGES.notFound],
    [409, PACKAGE_MESSAGES.noSessions],
    [409, PACKAGE_MESSAGES.expired],
    [409, PACKAGE_MESSAGES.duplicateName],
    [409, PACKAGE_MESSAGES.adjustOutOfRange],
    [403, 'sin permiso'],
  ] as const) {
    assert.deepEqual(describePackageError(new ApiError(status, message), 'x'), { kind: 'server', message })
  }
})

test('describePackageError tells a network failure apart and says nothing was queued', () => {
  const failure = describePackageError(new NetworkError('No pudimos conectar con el servidor.'), 'x')
  assert.equal(failure.kind, 'network')
  assert.match(failure.message, /no quedó en cola/i)
  assert.equal(describePackageError(new Error('boom'), 'x').message, 'boom')
  assert.deepEqual(describePackageError('???', 'Fallback'), { kind: 'server', message: 'Fallback' })
})

// --- intentos: un client_op_id por intento, el mismo en el reintento ---

test('an attempt keeps its client_op_id for the same operation and changes it for another', () => {
  let n = 0
  const attempt = createOpAttempt(() => `op-${(n += 1)}`)
  const sell = sellFingerprint('c1', { package_id: 'pk1', professional_id: 'u1', method: 'cash' })
  assert.equal(attempt.idFor(sell), 'op-1')
  assert.equal(attempt.idFor(sell), 'op-1')
  // Another package, professional or method is another attempt.
  assert.equal(
    attempt.idFor(sellFingerprint('c1', { package_id: 'pk2', professional_id: 'u1', method: 'cash' })),
    'op-2',
  )
  assert.equal(
    attempt.idFor(sellFingerprint('c1', { package_id: 'pk2', professional_id: 'u1', method: 'card' })),
    'op-3',
  )
})

test('settle() closes the attempt: the next one gets a fresh client_op_id', () => {
  let n = 0
  const attempt = createOpAttempt(() => `op-${(n += 1)}`)
  const fingerprint = redeemFingerprint('c1', 'cp1', { professional_id: 'u1' })
  assert.equal(attempt.idFor(fingerprint), 'op-1')
  attempt.settle()
  assert.equal(attempt.idFor(fingerprint), 'op-2')
  assert.equal(attempt.current(), 'op-2')
  attempt.settle()
  assert.equal(attempt.current(), null)
})

test('fingerprints differ by client, package and every typed field', () => {
  const base = redeemFingerprint('c1', 'cp1', { professional_id: 'u1', note: 'a' })
  assert.notEqual(base, redeemFingerprint('c2', 'cp1', { professional_id: 'u1', note: 'a' }))
  assert.notEqual(base, redeemFingerprint('c1', 'cp2', { professional_id: 'u1', note: 'a' }))
  assert.notEqual(base, redeemFingerprint('c1', 'cp1', { professional_id: 'u2', note: 'a' }))
  assert.notEqual(base, redeemFingerprint('c1', 'cp1', { professional_id: 'u1', note: 'b' }))
  assert.equal(base, redeemFingerprint('c1', 'cp1', { professional_id: 'u1', note: ' a ' }))
})

// --- cliente HTTP ---

test('fetchPackages GETs the catalog with Bearer and normalizes it', async () => {
  const stub = stubFetch(() => json({ packages: [RAW_PACKAGE] }))
  try {
    setToken('jwt-packages')
    const list = await fetchPackages('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/packages`)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers['Authorization'], 'Bearer jwt-packages')
    assert.equal(list[0]?.id, 'pk1')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('createPackage POSTs the body of the contract and updatePackage PUTs the four keys', async () => {
  const stub = stubFetch(() => json(RAW_PACKAGE, 201))
  try {
    setToken('jwt-packages')
    const saved = await createPackage('s1', {
      name: '5 cortes',
      service_id: 'sv1',
      sessions: 5,
      price_cents: 25000,
    })
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/packages`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      name: '5 cortes',
      service_id: 'sv1',
      sessions: 5,
      price_cents: 25000,
    })
    assert.equal(saved?.id, 'pk1')

    await updatePackage('s1', 'pk 1', { name: '5 cortes', price_cents: 25000, validity_days: null, active: false })
    assert.equal(stub.calls[1]?.url, `${API_BASE}/api/v1/salons/s1/packages/pk%201`)
    assert.equal(stub.calls[1]?.method, 'PUT')
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), {
      name: '5 cortes',
      price_cents: 25000,
      validity_days: null,
      active: false,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 409 of the catalog reaches the caller with the sentence of the server', async () => {
  const stub = stubFetch(() => json({ error: 'Ya hay un paquete activo con ese nombre' }, 409))
  try {
    setToken('jwt-packages')
    await assert.rejects(
      createPackage('s1', { name: 'x', service_id: 'sv1', sessions: 1, price_cents: 0 }),
      (error: unknown) =>
        error instanceof ApiError && error.status === 409 && error.message === PACKAGE_MESSAGES.duplicateName,
    )
  } finally {
    stub.restore()
    clearToken()
  }
})

test('fetchClientPackages and fetchPackageMovements use the client routes', async () => {
  const stub = stubFetch((call) =>
    call.url.endsWith('/movements')
      ? json({ movements: [{ id: 'm1', delta: 5, reason: 'purchase', note: null, professional_id: null, created_at: '2026-11-03T10:00:00Z' }] })
      : json({ packages: [RAW_CLIENT_PACKAGE] }),
  )
  try {
    setToken('jwt-packages')
    const list = await fetchClientPackages('s1', 'c1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/packages`)
    assert.equal(list[0]?.sessions_left, 3)
    const movements = await fetchPackageMovements('s1', 'c1', 'cp1')
    assert.equal(stub.calls[1]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/packages/cp1/movements`)
    assert.equal(movements[0]?.reason, 'purchase')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('sellPackage POSTs {package_id, professional_id, method, client_op_id} and returns the sale and the balance', async () => {
  const stub = stubFetch(() => json({ sale_id: 'sa1', client_package: RAW_CLIENT_PACKAGE }, 201))
  try {
    setToken('jwt-packages')
    const result = await sellPackage(
      's1',
      'c1',
      { package_id: 'pk1', professional_id: 'u1', method: 'transfer' },
      'op-sell',
    )
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/packages`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), {
      package_id: 'pk1',
      professional_id: 'u1',
      method: 'transfer',
      client_op_id: 'op-sell',
    })
    assert.equal(result.sale_id, 'sa1')
    assert.equal(result.client_package?.id, 'cp1')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('redeemPackageSession POSTs only what was typed; adjustPackageBalance POSTs delta and note', async () => {
  const stub = stubFetch(() => json({ client_package: RAW_CLIENT_PACKAGE }))
  try {
    setToken('jwt-packages')
    const redeemed = await redeemPackageSession('s1', 'c1', 'cp1', {}, 'op-r')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/packages/cp1/redeem`)
    assert.deepEqual(JSON.parse(stub.calls[0]?.body ?? '{}'), { client_op_id: 'op-r' })
    assert.equal(redeemed?.id, 'cp1')

    await adjustPackageBalance('s1', 'c1', 'cp1', { delta: -1, note: 'error de carga' })
    assert.equal(stub.calls[1]?.url, `${API_BASE}/api/v1/salons/s1/clients/c1/packages/cp1/adjust`)
    assert.deepEqual(JSON.parse(stub.calls[1]?.body ?? '{}'), { delta: -1, note: 'error de carga' })
  } finally {
    stub.restore()
    clearToken()
  }
})

// --- una escritura a la vez, solo en línea ---

const SELL_WRITE: PackageWrite = {
  kind: 'sell',
  clientId: 'c1',
  payload: { package_id: 'pk1', professional_id: 'u1', method: 'cash' },
  clientOpId: 'op-1',
}

test('writePackage offline sends nothing, queues nothing and says so', async () => {
  const stub = stubFetch(() => json({}))
  try {
    setToken('jwt-packages')
    const gate = createPackagesGate()
    const outcome = await writePackage(gate, 's1', SELL_WRITE, () => false)
    assert.equal(outcome.status, 'offline')
    assert.match(outcome.status === 'offline' ? outcome.message : '', /sin conexión/i)
    assert.equal(stub.calls.length, 0)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writePackage on a network error reports `network`, frees the gate and does not retry by itself', async () => {
  const stub = stubFetch(() => {
    throw new TypeError('fetch failed')
  })
  try {
    setToken('jwt-packages')
    const gate = createPackagesGate()
    const outcome = await writePackage(gate, 's1', SELL_WRITE, () => true)
    assert.equal(outcome.status, 'network')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writePackage keeps the sentence of the server on a rejection', async () => {
  const stub = stubFetch(() => json({ error: 'No quedan sesiones en este paquete' }, 409))
  try {
    setToken('jwt-packages')
    const outcome = await writePackage(
      createPackagesGate(),
      's1',
      { kind: 'redeem', clientId: 'c1', clientPackageId: 'cp1', payload: {}, clientOpId: 'op-2' },
      () => true,
    )
    // A 4xx is a definitive rejection: nothing was stored, the attempt can close.
    assert.deepEqual(outcome, {
      status: 'failed',
      message: 'No quedan sesiones en este paquete',
      definitive: true,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writePackage treats a 5xx as an unknown result: not definitive, so the attempt stays open', async () => {
  const stub = stubFetch(() => json({ error: 'El servidor respondió 502. Intenta nuevamente.' }, 502))
  try {
    setToken('jwt-packages')
    const outcome = await writePackage(createPackagesGate(), 's1', SELL_WRITE, () => true)
    assert.deepEqual(outcome, {
      status: 'failed',
      message: 'El servidor respondió 502. Intenta nuevamente.',
      definitive: false,
    })
  } finally {
    stub.restore()
    clearToken()
  }
})

test('writePackage resends the same client_op_id on a retry of the same attempt', async () => {
  let fail = true
  const stub = stubFetch(() => {
    if (fail) {
      fail = false
      throw new TypeError('fetch failed')
    }
    return json({ sale_id: 'sa1', client_package: RAW_CLIENT_PACKAGE }, 201)
  })
  try {
    setToken('jwt-packages')
    const gate = createPackagesGate()
    const attempt = createOpAttempt(() => 'op-fixed')
    const payload = { package_id: 'pk1', professional_id: 'u1', method: 'cash' as const }
    const fingerprint = sellFingerprint('c1', payload)
    const first = await writePackage(
      gate,
      's1',
      { kind: 'sell', clientId: 'c1', payload, clientOpId: attempt.idFor(fingerprint) },
      () => true,
    )
    assert.equal(first.status, 'network')
    const second = await writePackage(
      gate,
      's1',
      { kind: 'sell', clientId: 'c1', payload, clientOpId: attempt.idFor(fingerprint) },
      () => true,
    )
    assert.equal(second.status, 'saved')
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
        release = () => resolve(json({ client_package: RAW_CLIENT_PACKAGE }))
      }),
  )
  try {
    setToken('jwt-packages')
    const gate = createPackagesGate()
    const write: PackageWrite = {
      kind: 'redeem',
      clientId: 'c1',
      clientPackageId: 'cp1',
      payload: {},
      clientOpId: 'op-x',
    }
    const first = writePackage(gate, 's1', write, () => true)
    const second = writePackage(gate, 's1', write, () => true)
    assert.deepEqual(await second, { status: 'busy', message: PACKAGES_BUSY_MESSAGE })
    release()
    assert.equal((await first).status, 'saved')
    assert.equal(stub.calls.length, 1)
    assert.equal(gate.busy(), false)
  } finally {
    stub.restore()
    clearToken()
  }
})

test('the packages module never touches the offline queue storage', async () => {
  const stub = stubFetch(() => json({ sale_id: 'sa1', client_package: RAW_CLIENT_PACKAGE }, 201))
  const store = new Map<string, string>()
  const original = (globalThis as { localStorage?: unknown }).localStorage
  ;(globalThis as { localStorage?: unknown }).localStorage = {
    getItem: (key: string) => store.get(key) ?? null,
    setItem: (key: string, value: string) => void store.set(key, value),
    removeItem: (key: string) => void store.delete(key),
  }
  try {
    setToken('jwt-packages')
    await writePackage(createPackagesGate(), 's1', SELL_WRITE, () => false)
    await writePackage(createPackagesGate(), 's1', SELL_WRITE, () => true)
    assert.equal([...store.keys()].some((key) => key.includes('offline_queue')), false)
  } finally {
    ;(globalThis as { localStorage?: unknown }).localStorage = original
    stub.restore()
    clearToken()
  }
})
