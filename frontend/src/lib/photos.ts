/** Photo rules for client attachments: the API accepts JPG/PNG/WebP up to 8 MB. */

/** Hard limit enforced again by the server; the UI checks it before uploading. */
export const MAX_PHOTO_BYTES = 8 * 1024 * 1024

export const ACCEPTED_PHOTO_TYPES = ['image/jpeg', 'image/png', 'image/webp'] as const

/** Value for `<input type="file" accept="…">`. */
export const PHOTO_ACCEPT = ACCEPTED_PHOTO_TYPES.join(',')

/** Some browsers report `image/jpg` even though the real MIME type is `image/jpeg`. */
const ACCEPTED: readonly string[] = [...ACCEPTED_PHOTO_TYPES, 'image/jpg']

/** Only the fields the validation needs, so it also works without a real File. */
export type PhotoFileLike = {
  name?: string
  type: string
  size: number
}

export type PhotoValidation = { ok: true } | { ok: false; message: string }

const FORMAT_MESSAGE = 'Formato no admitido. Sube una imagen JPG, PNG o WebP.'

/** Renders a byte count with a readable unit (`1.5 MB`, `204 KB`, `812 B`). */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) {
    return '0 B'
  }
  if (bytes < 1024) {
    return `${Math.round(bytes)} B`
  }
  const kilobytes = bytes / 1024
  if (kilobytes < 1024) {
    return `${Math.round(kilobytes)} KB`
  }
  const megabytes = Math.round((kilobytes / 1024) * 10) / 10
  return `${Number.isInteger(megabytes) ? megabytes.toFixed(0) : megabytes.toFixed(1)} MB`
}

/**
 * Validates a candidate photo before any request leaves the browser.
 * Order matters: a wrong type is reported even when the file is also too big.
 */
export function validatePhotoFile(file: PhotoFileLike): PhotoValidation {
  const type = file.type.trim().toLowerCase()
  if (!ACCEPTED.includes(type)) {
    return { ok: false, message: FORMAT_MESSAGE }
  }
  if (!Number.isFinite(file.size) || file.size <= 0) {
    return { ok: false, message: 'La imagen está vacía. Elige otra foto.' }
  }
  if (file.size > MAX_PHOTO_BYTES) {
    return {
      ok: false,
      message: `La foto pesa ${formatBytes(file.size)} y el máximo es 8 MB. Elige una más ligera.`,
    }
  }
  return { ok: true }
}
