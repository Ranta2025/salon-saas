import test from 'node:test'
import assert from 'node:assert/strict'
import { API_BASE, ApiError } from '../../../lib/api.ts'
import { canEditSalonProfile, canViewSalonProfile } from '../../../lib/permissions.ts'
import { clearToken, setToken } from '../../../lib/session.ts'
import {
  GALLERY_FULL_MESSAGE,
  GALLERY_LIMIT,
  PROFILE_HELP,
  PROFILE_LIMITS,
  PROFILE_MESSAGES,
  buildProfilePayload,
  captionBody,
  deleteGalleryPhoto,
  draftFromProfile,
  fetchGallery,
  fetchProfile,
  galleryCounter,
  normalizeGallery,
  normalizeProfile,
  previewLanding,
  saveCaption,
  saveProfile,
  serviceDescriptionBody,
  uploadGalleryPhoto,
  validateServiceDescription,
  validateCaption,
  validateGalleryUpload,
  validateProfileDraft,
  type ProfileDraft,
} from './salonProfile.ts'

type Call = { url: string; method: string; headers: Record<string, string>; body: unknown }

function stubFetch(handler: (call: Call) => Response) {
  const calls: Call[] = []
  const original = globalThis.fetch
  globalThis.fetch = (async (input: RequestInfo | URL, init?: RequestInit) => {
    const call: Call = {
      url: String(input),
      method: init?.method ?? 'GET',
      headers: (init?.headers ?? {}) as Record<string, string>,
      body: init?.body,
    }
    calls.push(call)
    return handler(call)
  }) as typeof globalThis.fetch
  return { calls, restore: () => void (globalThis.fetch = original) }
}

function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'content-type': 'application/json' },
  })
}

const draft: ProfileDraft = {
  name: 'Luna Studio',
  description: 'Cortes y color con cita.',
  address: 'Calle 1 #23',
  phone: '+53 5555 1234',
  email: 'hola@luna.test',
  instagram: 'luna.studio',
}

// ---------- roles ----------

test('owner and admin edit the salon profile; reception only reads; the rest get a notice', () => {
  assert.equal(canEditSalonProfile('owner'), true)
  assert.equal(canEditSalonProfile('admin'), true)
  assert.equal(canEditSalonProfile('reception'), false)
  assert.equal(canEditSalonProfile('professional'), false)
  assert.equal(canEditSalonProfile(null), false)
  assert.equal(canViewSalonProfile('owner'), true)
  assert.equal(canViewSalonProfile('admin'), true)
  assert.equal(canViewSalonProfile('reception'), true)
  assert.equal(canViewSalonProfile('professional'), false)
  assert.equal(canViewSalonProfile(undefined), false)
})

// ---------- profile payload ----------

test('the profile payload always carries the six keys the PUT requires and never the slug', () => {
  const payload = buildProfilePayload(draft)
  assert.deepEqual(Object.keys(payload).sort(), [
    'address',
    'description',
    'email',
    'instagram',
    'name',
    'phone',
  ])
  assert.deepEqual(payload, {
    name: 'Luna Studio',
    description: 'Cortes y color con cita.',
    address: 'Calle 1 #23',
    phone: '+53 5555 1234',
    email: 'hola@luna.test',
    instagram: 'luna.studio',
  })
})

test('empty optional fields travel as null, never omitted', () => {
  const payload = buildProfilePayload({
    name: ' Luna ',
    description: '',
    address: '   ',
    phone: '',
    email: '',
    instagram: '',
  })
  assert.deepEqual(payload, {
    name: 'Luna',
    description: null,
    address: null,
    phone: null,
    email: null,
    instagram: null,
  })
  assert.equal('description' in payload, true)
})

test('the instagram handle is sent without a leading @', () => {
  assert.equal(buildProfilePayload({ ...draft, instagram: '@luna.studio' }).instagram, 'luna.studio')
})

test('a valid draft validates into the payload', () => {
  const result = validateProfileDraft(draft)
  assert.equal(result.ok, true)
  if (result.ok) {
    assert.deepEqual(result.payload, buildProfilePayload(draft))
  }
})

test('the name is required and at most 120 characters', () => {
  assert.deepEqual(validateProfileDraft({ ...draft, name: '   ' }), {
    ok: false,
    field: 'name',
    message: PROFILE_MESSAGES.nameRequired,
  })
  assert.equal(validateProfileDraft({ ...draft, name: 'a'.repeat(120) }).ok, true)
  assert.deepEqual(validateProfileDraft({ ...draft, name: 'a'.repeat(121) }), {
    ok: false,
    field: 'name',
    message: PROFILE_MESSAGES.tooLong('El nombre', 120),
  })
})

test('each text field has its own length limit, at the border and one over', () => {
  const limits: [keyof ProfileDraft, number][] = [
    ['description', 500],
    ['address', 200],
    ['phone', 40],
    ['instagram', 60],
  ]
  for (const [field, max] of limits) {
    assert.equal(PROFILE_LIMITS[field], max)
    assert.equal(validateProfileDraft({ ...draft, [field]: 'a'.repeat(max) }).ok, true, `${field} ${max}`)
    const over = validateProfileDraft({ ...draft, [field]: 'a'.repeat(max + 1) })
    assert.equal(over.ok, false, `${field} ${max + 1}`)
    if (!over.ok) {
      assert.equal(over.field, field)
      assert.match(over.message, new RegExp(`${max}`))
    }
  }
  const long = `${'a'.repeat(250)}@x.co`
  assert.equal(PROFILE_LIMITS.email, 254)
  const email = validateProfileDraft({ ...draft, email: long })
  assert.equal(email.ok, false)
})

test('an email with a bad format gets the verbatim server text; empty is allowed', () => {
  assert.deepEqual(validateProfileDraft({ ...draft, email: 'no-es-un-correo' }), {
    ok: false,
    field: 'email',
    message: 'Email inválido',
  })
  assert.equal(validateProfileDraft({ ...draft, email: '' }).ok, true)
})

test('NUL and line breaks are rejected before sending, in every text field', () => {
  for (const field of ['name', 'description', 'address', 'phone', 'email', 'instagram'] as const) {
    for (const bad of ['a\nb', 'a\r\nb', 'a\u0000b']) {
      const result = validateProfileDraft({ ...draft, [field]: bad })
      assert.equal(result.ok, false, `${field} ${JSON.stringify(bad)}`)
      if (!result.ok) {
        assert.equal(result.field, field)
      }
    }
  }
  assert.equal(PROFILE_MESSAGES.noLineBreaks, 'No uses saltos de línea en este campo')
})

test('instagram takes only the user: links and extra @ are rejected', () => {
  for (const bad of ['https://instagram.com/luna', 'luna studio', 'luna/x', '@@luna', 'a@b']) {
    const result = validateProfileDraft({ ...draft, instagram: bad })
    assert.equal(result.ok, false, bad)
    if (!result.ok) {
      assert.equal(result.field, 'instagram')
      assert.equal(result.message, PROFILE_MESSAGES.instagramUser)
    }
  }
})

test('the lengths count characters, not UTF-16 units', () => {
  assert.equal(validateProfileDraft({ ...draft, name: '💇'.repeat(120) }).ok, true)
  assert.equal(validateProfileDraft({ ...draft, name: '💇'.repeat(121) }).ok, false)
})

test('the verbatim help and server texts', () => {
  assert.equal(PROFILE_HELP.preview, 'Esto es lo que ve el cliente en tu página pública')
  assert.equal(GALLERY_FULL_MESSAGE, 'La galería ya tiene 12 fotos')
  assert.equal(GALLERY_LIMIT, 12)
})

// ---------- normalizers ----------

test('normalizeProfile reads nulls as null and builds a draft with empty strings', () => {
  const profile = normalizeProfile({
    name: 'Luna',
    slug: 'luna',
    description: null,
    address: 'Calle 1',
    phone: null,
    email: null,
    instagram: 'luna',
  })
  assert.deepEqual(profile, {
    name: 'Luna',
    slug: 'luna',
    description: null,
    address: 'Calle 1',
    phone: null,
    email: null,
    instagram: 'luna',
  })
  assert.ok(profile)
  assert.deepEqual(draftFromProfile(profile), {
    name: 'Luna',
    description: '',
    address: 'Calle 1',
    phone: '',
    email: '',
    instagram: 'luna',
  })
  assert.equal(normalizeProfile(null), null)
  assert.equal(normalizeProfile({ slug: 'x' }), null)
})

test('normalizeGallery resolves the url under the API origin and tolerates null captions', () => {
  const photos = normalizeGallery(
    {
      photos: [
        { id: 'p1', url: '/api/v1/public/salons/luna/gallery/p1', caption: 'Frente', created_at: '2026-01-01T10:00:00Z' },
        { id: 'p2', url: 'https://cdn.test/x.webp', caption: null, created_at: '2026-01-02T10:00:00Z' },
        { id: '', url: '/bad' },
        'nope',
      ],
    },
    'https://api.test',
  )
  assert.deepEqual(photos, [
    { id: 'p1', url: 'https://api.test/api/v1/public/salons/luna/gallery/p1', caption: 'Frente', createdAt: '2026-01-01T10:00:00Z' },
    { id: 'p2', url: 'https://cdn.test/x.webp', caption: '', createdAt: '2026-01-02T10:00:00Z' },
  ])
  assert.deepEqual(normalizeGallery(null, ''), [])
  assert.deepEqual(normalizeGallery({ photos: 'x' }, ''), [])
})

// ---------- gallery rules ----------

test('the counter reads x/12', () => {
  assert.equal(galleryCounter(0), '0/12')
  assert.equal(galleryCounter(7), '7/12')
  assert.equal(galleryCounter(12), '12/12')
})

test('an upload is checked before sending: type, size, empty file and the 12 photo cap', () => {
  const ok = { name: 'a.jpg', type: 'image/jpeg', size: 1000 }
  assert.deepEqual(validateGalleryUpload(ok, 0), { ok: true })
  assert.deepEqual(validateGalleryUpload(ok, 11), { ok: true })
  assert.deepEqual(validateGalleryUpload(ok, 12), { ok: false, message: 'La galería ya tiene 12 fotos' })
  const gif = validateGalleryUpload({ name: 'a.gif', type: 'image/gif', size: 10 }, 0)
  assert.deepEqual(gif, { ok: false, message: 'Formato no admitido. Sube una imagen JPG, PNG o WebP.' })
  const big = validateGalleryUpload({ name: 'a.png', type: 'image/png', size: 8 * 1024 * 1024 + 1 }, 0)
  assert.equal(big.ok, false)
  if (!big.ok) {
    assert.match(big.message, /máximo es 8 MB/)
  }
  assert.equal(validateGalleryUpload({ name: 'a.png', type: 'image/png', size: 8 * 1024 * 1024 }, 0).ok, true)
  assert.equal(validateGalleryUpload({ name: 'a.png', type: 'image/png', size: 0 }, 0).ok, false)
})

test('a caption is at most 120 characters, single line, and empty means no caption', () => {
  assert.deepEqual(validateCaption('  Frente del salón '), { ok: true, caption: 'Frente del salón' })
  assert.deepEqual(validateCaption('   '), { ok: true, caption: null })
  assert.deepEqual(validateCaption('a'.repeat(120)), { ok: true, caption: 'a'.repeat(120) })
  const over = validateCaption('a'.repeat(121))
  assert.deepEqual(over, { ok: false, message: 'La leyenda no puede superar los 120 caracteres' })
  assert.deepEqual(validateCaption('a\nb'), { ok: false, message: 'No uses saltos de línea en este campo' })
  assert.deepEqual(validateCaption('a\u0000b'), { ok: false, message: 'No uses saltos de línea en este campo' })
})

test('the caption body always carries the key, null when empty', () => {
  assert.deepEqual(captionBody('Hola'), { caption: 'Hola' })
  assert.deepEqual(captionBody(null), { caption: null })
  assert.deepEqual(captionBody(''), { caption: null })
})

// ---------- preview ----------

test('the preview shows what the landing will render, from the same builder', () => {
  const gallery = normalizeGallery({ photos: [{ id: 'p1', url: '/g/p1', caption: 'Frente' }] }, '')
  const preview = previewLanding(draft, gallery)
  assert.equal(preview.name, 'Luna Studio')
  assert.equal(preview.description, 'Cortes y color con cita.')
  assert.equal(preview.address, 'Calle 1 #23')
  assert.equal(preview.phone?.whatsapp, 'https://wa.me/5355551234')
  assert.equal(preview.email, 'hola@luna.test')
  assert.equal(preview.instagram?.url, 'https://instagram.com/luna.studio')
  assert.equal(preview.sections.gallery, true)
})

test('the preview hides what is empty, even for an unsaved draft with spaces or @', () => {
  const preview = previewLanding(
    { name: 'Solo', description: '  ', address: '', phone: '', email: '', instagram: '@' },
    [],
  )
  assert.equal(preview.description, null)
  assert.equal(preview.address, null)
  assert.equal(preview.phone, null)
  assert.equal(preview.instagram, null)
  assert.equal(preview.sections.gallery, false)
})

// ---------- HTTP clients ----------

test('fetchProfile reads GET /profile with the session token', async () => {
  const stub = stubFetch(() =>
    json({ name: 'Luna', slug: 'luna', description: null, address: null, phone: null, email: null, instagram: null }),
  )
  try {
    setToken('jwt-1')
    const profile = await fetchProfile('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/profile`)
    assert.equal(stub.calls[0]?.method, 'GET')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-1')
    assert.equal(profile.name, 'Luna')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('saveProfile PUTs exactly the six keys and returns the new profile', async () => {
  const stub = stubFetch(() => json({ ...draft, slug: 'luna' }))
  try {
    const saved = await saveProfile('s1', buildProfilePayload(draft))
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/profile`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(String(stub.calls[0]?.body)), buildProfilePayload(draft))
    assert.equal(saved.slug, 'luna')
  } finally {
    stub.restore()
  }
})

test('a rejected profile shows the server text verbatim', async () => {
  const text = 'Faltan datos del salón: nombre, descripción, dirección, teléfono, email e instagram'
  const stub = stubFetch(() => json({ error: text }, 400))
  try {
    await assert.rejects(
      () => saveProfile('s1', buildProfilePayload(draft)),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 400)
        assert.equal(err.message, text)
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('fetchGallery GETs the list and resolves the urls', async () => {
  const stub = stubFetch(() => json({ photos: [{ id: 'p1', url: '/g/p1', caption: null, created_at: '2026-01-01T00:00:00Z' }] }))
  try {
    const photos = await fetchGallery('s1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/gallery`)
    assert.equal(photos[0]?.url, `${API_BASE}/g/p1`)
  } finally {
    stub.restore()
  }
})

test('uploadGalleryPhoto POSTs multipart with the file in the `file` field', async () => {
  const stub = stubFetch(() => json({ photo: { id: 'p9', url: '/g/p9', caption: null, created_at: '2026-01-01T00:00:00Z' } }, 201))
  try {
    setToken('jwt-2')
    const file = new File([new Uint8Array([1, 2, 3])], 'a.jpg', { type: 'image/jpeg' })
    const photo = await uploadGalleryPhoto('s1', file)
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/gallery`)
    assert.equal(stub.calls[0]?.method, 'POST')
    assert.equal(stub.calls[0]?.headers.Authorization, 'Bearer jwt-2')
    assert.equal(stub.calls[0]?.headers['Content-Type'], undefined)
    const form = stub.calls[0]?.body
    assert.ok(form instanceof FormData)
    assert.equal((form.get('file') as File).name, 'a.jpg')
    assert.equal(photo?.id, 'p9')
  } finally {
    stub.restore()
    clearToken()
  }
})

test('a 409 on upload shows the server text verbatim', async () => {
  const stub = stubFetch(() => json({ error: 'La galería ya tiene 12 fotos' }, 409))
  try {
    const file = new File([new Uint8Array([1])], 'a.png', { type: 'image/png' })
    await assert.rejects(
      () => uploadGalleryPhoto('s1', file),
      (err: unknown) => {
        assert.ok(err instanceof ApiError)
        assert.equal(err.status, 409)
        assert.equal(err.message, 'La galería ya tiene 12 fotos')
        return true
      },
    )
  } finally {
    stub.restore()
  }
})

test('saveCaption PUTs {caption} and deleteGalleryPhoto DELETEs the id', async () => {
  const stub = stubFetch((call) =>
    call.method === 'DELETE'
      ? new Response(null, { status: 204 })
      : json({ photo: { id: 'p/1', url: '/g/p1', caption: 'Hola', created_at: '2026-01-01T00:00:00Z' } }),
  )
  try {
    await saveCaption('s1', 'p/1', 'Hola')
    await saveCaption('s1', 'p/1', null)
    await deleteGalleryPhoto('s1', 'p/1')
    assert.equal(stub.calls[0]?.url, `${API_BASE}/api/v1/salons/s1/gallery/p%2F1`)
    assert.equal(stub.calls[0]?.method, 'PUT')
    assert.deepEqual(JSON.parse(String(stub.calls[0]?.body)), { caption: 'Hola' })
    assert.deepEqual(JSON.parse(String(stub.calls[1]?.body)), { caption: null })
    assert.equal(stub.calls[2]?.method, 'DELETE')
    assert.equal(stub.calls[2]?.url, `${API_BASE}/api/v1/salons/s1/gallery/p%2F1`)
  } finally {
    stub.restore()
  }
})

// ---------- service description (D2) ----------

test('a service description is at most 300 characters on a single line', () => {
  assert.deepEqual(validateServiceDescription('  Con lavado '), { ok: true, description: 'Con lavado' })
  assert.deepEqual(validateServiceDescription(''), { ok: true, description: '' })
  assert.equal(validateServiceDescription('a'.repeat(300)).ok, true)
  assert.deepEqual(validateServiceDescription('a'.repeat(301)), {
    ok: false,
    message: 'La descripción no puede superar los 300 caracteres',
  })
  assert.deepEqual(validateServiceDescription('a\nb'), {
    ok: false,
    message: 'No uses saltos de línea en este campo',
  })
})

test('the description key is omitted on create when empty and sent as "" on edit to clear it', () => {
  assert.deepEqual(serviceDescriptionBody('Con lavado', 'create'), { description: 'Con lavado' })
  assert.deepEqual(serviceDescriptionBody('', 'create'), {})
  assert.deepEqual(serviceDescriptionBody('Con lavado', 'edit'), { description: 'Con lavado' })
  assert.deepEqual(serviceDescriptionBody('', 'edit'), { description: '' })
})
