import { useEffect, useState } from 'react'
import { describeApiError } from '../../lib/api'
import {
  GALLERY_FULL_MESSAGE,
  GALLERY_LIMIT,
  PROFILE_HELP,
  deleteGalleryPhoto,
  galleryCounter,
  saveCaption,
  uploadGalleryPhoto,
  validateCaption,
  validateGalleryUpload,
  type GalleryPhoto,
} from '../../lib/salonProfile'
import { PhotoUploader } from './PhotoUploader'

type SalonGalleryManagerProps = {
  salonId: string
  photos: readonly GalleryPhoto[]
  /** Called after a change on the server; the page reads the list again. */
  onChanged: () => void
  /** The caption of one photo was saved: the page updates its copy. */
  onCaption: (photoId: string, caption: string) => void
}

type CardProps = {
  salonId: string
  photo: GalleryPhoto
  index: number
  onDeleted: () => void
  onCaption: (photoId: string, caption: string) => void
}

function GalleryCard({ salonId, photo, index, onDeleted, onCaption }: CardProps) {
  const [caption, setCaption] = useState(photo.caption)
  // After a reload the server's caption wins: the input must not keep a stale value.
  useEffect(() => {
    setCaption(photo.caption)
  }, [photo.caption])
  const [error, setError] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [confirming, setConfirming] = useState(false)
  const [deleting, setDeleting] = useState(false)
  const label = `Foto ${index + 1} de la galería`
  const dirty = caption.trim() !== photo.caption

  const handleSaveCaption = async (): Promise<void> => {
    setError(null)
    const check = validateCaption(caption)
    if (!check.ok) {
      setError(check.message)
      return
    }
    setSaving(true)
    try {
      await saveCaption(salonId, photo.id, check.caption)
      onCaption(photo.id, check.caption ?? '')
      setCaption(check.caption ?? '')
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos guardar la leyenda.').message)
    } finally {
      setSaving(false)
    }
  }

  const handleDelete = async (): Promise<void> => {
    setError(null)
    setDeleting(true)
    try {
      await deleteGalleryPhoto(salonId, photo.id)
      onDeleted()
    } catch (err: unknown) {
      setError(describeApiError(err, 'No pudimos eliminar la foto.').message)
      setConfirming(false)
    } finally {
      setDeleting(false)
    }
  }

  return (
    <li className="gallery-admin__item">
      <figure className="photo-card">
        <img
          className="photo-card__img"
          src={photo.url}
          alt={photo.caption !== '' ? photo.caption : label}
          loading="lazy"
        />
        <div className="field">
          <label htmlFor={`caption-${photo.id}`}>Leyenda de la {label.toLowerCase()}</label>
          <input
            id={`caption-${photo.id}`}
            className="input"
            value={caption}
            placeholder="Sin leyenda"
            onChange={(event) => {
              setCaption(event.target.value)
              setError(null)
            }}
            aria-invalid={error ? true : undefined}
            aria-describedby={error ? `caption-${photo.id}-error` : undefined}
          />
        </div>
        {error ? (
          <p className="field__error" id={`caption-${photo.id}-error`} role="alert">
            {error}
          </p>
        ) : null}
        {confirming ? (
          <div className="photo-confirm" role="group" aria-label="Confirmar borrado">
            <span>¿Eliminar esta foto? También deja de verse en la página pública.</span>
            <button
              type="button"
              className="btn btn--quiet btn--sm"
              onClick={() => void handleDelete()}
              disabled={deleting}
            >
              {deleting ? 'Eliminando…' : 'Sí, eliminar'}
            </button>
            <button
              type="button"
              className="btn btn--quiet btn--sm"
              onClick={() => setConfirming(false)}
              disabled={deleting}
            >
              Cancelar
            </button>
          </div>
        ) : (
          <div className="gallery-admin__actions">
            <button
              type="button"
              className="btn btn--wine btn--sm"
              onClick={() => void handleSaveCaption()}
              disabled={saving || !dirty}
            >
              {saving ? 'Guardando…' : 'Guardar leyenda'}
            </button>
            <button
              type="button"
              className="btn btn--quiet btn--sm"
              aria-label={`Eliminar ${label.toLowerCase()}`}
              onClick={() => setConfirming(true)}
            >
              Eliminar
            </button>
          </div>
        )}
      </figure>
    </li>
  )
}

/**
 * Gallery of the public page (D3/D7): upload with the type and size checks before
 * sending (the same uploader as the client photos), caption edit, delete with
 * confirmation and the x/12 counter. The 409 of the server is shown as it sent it.
 */
export function SalonGalleryManager({
  salonId,
  photos,
  onChanged,
  onCaption,
}: SalonGalleryManagerProps) {
  const full = photos.length >= GALLERY_LIMIT

  const handleUpload = async (file: File): Promise<void> => {
    const check = validateGalleryUpload(file, photos.length)
    if (!check.ok) {
      throw new Error(check.message)
    }
    try {
      await uploadGalleryPhoto(salonId, file)
    } catch (err: unknown) {
      // The list may have changed under us (another admin): show it again.
      onChanged()
      throw new Error(describeApiError(err, 'No pudimos subir la foto. Intenta de nuevo.').message)
    }
    onChanged()
  }

  return (
    <section className="panel-card" aria-labelledby="gallery-admin-title">
      <div className="panel-card__head">
        <h2 id="gallery-admin-title">Galería de fotos</h2>
        <span className="panel-card__meta" aria-label={`${photos.length} de ${GALLERY_LIMIT} fotos`}>
          {galleryCounter(photos.length)}
        </span>
      </div>
      <p className="field__hint">{PROFILE_HELP.gallery}</p>

      {full ? (
        <p className="notice notice--info" role="status">
          {GALLERY_FULL_MESSAGE}. Elimina una para subir otra.
        </p>
      ) : null}
      <PhotoUploader onUpload={handleUpload} disabled={full} />

      {photos.length === 0 ? (
        <div className="empty-state">
          <h2>Sin fotos todavía</h2>
          <p>Sin fotos, la página pública no muestra la sección de galería.</p>
        </div>
      ) : (
        <ul className="photos-grid gallery-admin__grid">
          {photos.map((photo, index) => (
            <GalleryCard
              key={photo.id}
              salonId={salonId}
              photo={photo}
              index={index}
              onDeleted={onChanged}
              onCaption={onCaption}
            />
          ))}
        </ul>
      )}
    </section>
  )
}
