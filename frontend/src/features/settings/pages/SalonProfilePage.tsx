import { useCallback, useEffect, useMemo, useState } from 'react'
import { describeApiError, type ApiErrorNotice } from '@/lib/api'
import { activeRole, canEditSalonProfile, canViewSalonProfile, roleLabel } from '@/lib/permissions'
import {
  draftFromProfile,
  fetchGallery,
  fetchProfile,
  previewLanding,
  saveProfile,
  type GalleryPhoto,
  type ProfileDraft,
  type ProfilePayload,
} from '../lib/salonProfile'
import { useSalon } from '@/app/SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from '@/app/components/Feedback'
import { PageHeader } from '@/app/components/PageHeader'
import { SalonGalleryManager } from '../components/SalonGalleryManager'
import { SalonPreview } from '../components/SalonPreview'
import { SalonProfileForm } from '../components/SalonProfileForm'

const EMPTY_DRAFT: ProfileDraft = {
  name: '',
  description: '',
  address: '',
  phone: '',
  email: '',
  instagram: '',
}

/**
 * `/panel/mi-salon` (contract F16, D7): the profile and the gallery of the public
 * page, with a preview of what the client will see. Owner and admin edit; reception
 * reads the profile only (the gallery routes are for owner/admin); a professional
 * gets a notice, like the other pages that are not theirs.
 */
export function SalonProfilePage() {
  const { salonId, salons } = useSalon()
  const role = activeRole(salons, salonId)
  const canView = canViewSalonProfile(role)
  const canEdit = canEditSalonProfile(role)

  const [draft, setDraft] = useState<ProfileDraft>(EMPTY_DRAFT)
  const [photos, setPhotos] = useState<GalleryPhoto[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [galleryError, setGalleryError] = useState<ApiErrorNotice | null>(null)
  const [reloadKey, setReloadKey] = useState(0)
  const [galleryKey, setGalleryKey] = useState(0)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])
  const reloadGallery = useCallback(() => setGalleryKey((key) => key + 1), [])

  // The profile: every active member of the allowed roles reads it.
  useEffect(() => {
    if (!salonId || !canView) {
      return
    }
    let active = true
    setLoading(true)
    setError(null)
    // Another salon: never keep showing the previous salon's form while this loads.
    setLoaded(false)
    setDraft(EMPTY_DRAFT)
    fetchProfile(salonId)
      .then((profile) => {
        if (active) {
          setDraft(draftFromProfile(profile))
          setLoaded(true)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setError(describeApiError(err, 'No pudimos cargar los datos del salón.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
    }
  }, [salonId, canView, reloadKey])

  // The gallery routes are for owner/admin (D3): reception never asks for it.
  useEffect(() => {
    if (!salonId || !canEdit) {
      return
    }
    let active = true
    setGalleryError(null)
    setPhotos([])
    fetchGallery(salonId)
      .then((list) => {
        if (active) {
          setPhotos(list)
        }
      })
      .catch((err: unknown) => {
        if (active) {
          setGalleryError(describeApiError(err, 'No pudimos cargar la galería.'))
        }
      })
    return () => {
      active = false
    }
  }, [salonId, canEdit, galleryKey])

  const preview = useMemo(() => previewLanding(draft, photos), [draft, photos])

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Página pública" title="Mi salón" />
        <SalonRequired />
      </>
    )
  }

  if (!canView) {
    return (
      <>
        <PageHeader eyebrow="Página pública" title="Mi salón" />
        <div className="empty-state">
          <h2>Esta sección es de la administración</h2>
          <p>
            Tu rol ({roleLabel(role)}) no puede ver los datos de la página pública del salón. Si
            necesitas acceso, pídeselo a la dueña o a la administración.
          </p>
        </div>
      </>
    )
  }

  const handleSave = async (payload: ProfilePayload): Promise<void> => {
    const saved = await saveProfile(salonId, payload)
    setDraft(draftFromProfile(saved))
  }

  return (
    <>
      <PageHeader
        eyebrow="Página pública"
        title="Mi salón"
        description="Los datos de contacto, la descripción y las fotos que muestra tu página pública."
        actions={
          <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
            Recargar
          </button>
        }
      />

      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}
      {loading && !loaded ? <LoadingState label="Cargando los datos del salón…" /> : null}

      {loaded ? (
        <div className="salon-profile">
          <div className="salon-profile__main">
            <SalonProfileForm
              draft={draft}
              onChange={setDraft}
              onSave={handleSave}
              readOnly={!canEdit}
            />
            {canEdit ? (
              <>
                {galleryError ? (
                  <ErrorNotice error={galleryError} onRetry={reloadGallery} />
                ) : null}
                <SalonGalleryManager
                  salonId={salonId}
                  photos={photos}
                  onChanged={reloadGallery}
                  onCaption={(photoId, caption) =>
                    setPhotos((list) =>
                      list.map((photo) => (photo.id === photoId ? { ...photo, caption } : photo)),
                    )
                  }
                />
              </>
            ) : null}
          </div>
          <SalonPreview model={preview} galleryKnown={canEdit} />
        </div>
      ) : null}
    </>
  )
}
