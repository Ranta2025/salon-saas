import test from 'node:test'
import assert from 'node:assert/strict'
import {
  ACCEPTED_PHOTO_TYPES,
  MAX_PHOTO_BYTES,
  formatBytes,
  validatePhotoFile,
} from './photos.ts'

test('accepts jpg, png and webp photos inside the 8 MB limit', () => {
  for (const type of ACCEPTED_PHOTO_TYPES) {
    assert.deepEqual(validatePhotoFile({ type, size: 1024 }), { ok: true })
  }
  assert.deepEqual(validatePhotoFile({ type: 'image/jpeg', size: MAX_PHOTO_BYTES }), { ok: true })
  assert.deepEqual(validatePhotoFile({ type: 'IMAGE/PNG', size: 1 }), { ok: true })
})

test('rejects any other format with a friendly message', () => {
  assert.deepEqual(validatePhotoFile({ type: 'image/gif', size: 1024 }), {
    ok: false,
    message: 'Formato no admitido. Sube una imagen JPG, PNG o WebP.',
  })
  assert.deepEqual(validatePhotoFile({ type: '', size: 1024 }), {
    ok: false,
    message: 'Formato no admitido. Sube una imagen JPG, PNG o WebP.',
  })
})

test('rejects photos over 8 MB and names the actual weight and the limit', () => {
  const tooBig = MAX_PHOTO_BYTES + 1
  assert.deepEqual(validatePhotoFile({ type: 'image/webp', size: tooBig }), {
    ok: false,
    message: 'La foto pesa 8 MB y el máximo es 8 MB. Elige una más ligera.',
  })
  assert.deepEqual(validatePhotoFile({ type: 'image/jpeg', size: Math.round(9.2 * 1024 * 1024) }), {
    ok: false,
    message: 'La foto pesa 9.2 MB y el máximo es 8 MB. Elige una más ligera.',
  })
})

test('rejects empty files before uploading anything', () => {
  assert.deepEqual(validatePhotoFile({ type: 'image/png', size: 0 }), {
    ok: false,
    message: 'La imagen está vacía. Elige otra foto.',
  })
})

test('formatBytes renders readable weights in Spanish', () => {
  assert.equal(formatBytes(0), '0 B')
  assert.equal(formatBytes(512), '512 B')
  assert.equal(formatBytes(2048), '2 KB')
  assert.equal(formatBytes(1024 * 1024), '1 MB')
  assert.equal(formatBytes(Math.round(1.5 * 1024 * 1024)), '1.5 MB')
  assert.equal(formatBytes(MAX_PHOTO_BYTES + 1), '8 MB')
})
