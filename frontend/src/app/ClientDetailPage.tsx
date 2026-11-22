import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  deleteClientPhoto,
  describeApiError,
  fetchClientPhotos,
  fetchClients,
  uploadClientPhoto,
  type ApiErrorNotice,
} from '../lib/api'
import { formatBytes, validatePhotoFile } from '../lib/photos'
import { displayPhone } from '../lib/phone'
import type { Client, ClientPhoto } from '../types'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { PageHeader } from './components/PageHeader'
import { PhotoUploader } from './components/PhotoUploader'

function photoSizeLabel(photo: ClientPhoto): string {
  const parts: string[] = []
  if (photo.width && photo.height) {
    parts.push(`${photo.width}×${photo.height} px`)
  }
  if (photo.bytes) {
    parts.push(formatBytes(photo.bytes))
  }
  return parts.join(' · ')
}

export function ClientDetailPage() {
  const { salonId } = useSalon()
  const { clientId = '' } = useParams<{ clientId: string }>()
  const [client, setClient] = useState<Client | null>(null)
  const [photos, setPhotos] = useState<ClientPhoto[]>([])
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [notice, setNotice] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  // The photo url is a signed URL that expires: a failed load triggers exactly one
  // refetch of the list, which comes back with fresh signatures.
  const signatureRefresh = useRef(false)

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || clientId === '') {
      return
    }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    // There is no GET /clients/:id in the contract yet, so the name comes from the list.
    Promise.all([
      fetchClients(salonId, controller.signal),
      fetchClientPhotos(salonId, clientId, controller.signal),
    ])
      .then(([clients, photoList]) => {
        if (!active) {
          return
        }
        setClient(clients.find((item) => item.id === clientId) ?? null)
        setPhotos(photoList)
        setLoaded(true)
        signatureRefresh.current = false
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setError(describeApiError(err, 'No pudimos cargar la ficha.'))
        }
      })
      .finally(() => {
        if (active) {
          setLoading(false)
        }
      })
    return () => {
      active = false
      controller.abort()
    }
  }, [salonId, clientId, reloadKey])

  const handlePhotoError = useCallback((): void => {
    if (signatureRefresh.current) {
      return
    }
    signatureRefresh.current = true
    setNotice('El enlace de la foto venció. Pedimos la lista otra vez con enlaces nuevos.')
    reload()
  }, [reload])

  const handleUpload = useCallback(
    async (file: File): Promise<void> => {
      if (!salonId) {
        throw new Error('Falta el salón activo.')
      }
      const validation = validatePhotoFile(file)
      if (!validation.ok) {
        throw new Error(validation.message)
      }
      const photo = await uploadClientPhoto(salonId, clientId, file)
      setPhotos((current) => [photo, ...current])
      setNotice('Foto subida. La variante WebP ya está en la galería.')
    },
    [salonId, clientId],
  )

  const confirmDelete = async (): Promise<void> => {
    if (!salonId || !deleteTarget) {
      return
    }
    setDeleting(true)
    setError(null)
    try {
      await deleteClientPhoto(salonId, clientId, deleteTarget)
      setPhotos((current) => current.filter((photo) => photo.id !== deleteTarget))
      setDeleteTarget(null)
      setNotice('Foto eliminada de la ficha.')
    } catch (err) {
      setError(describeApiError(err, 'No pudimos eliminar la foto.'))
    } finally {
      setDeleting(false)
    }
  }

  if (!salonId) {
    return (
      <>
        <PageHeader eyebrow="Clientes" title="Ficha del cliente" />
        <SalonRequired />
      </>
    )
  }

  const clientName = client?.name ?? 'Cliente sin nombre'

  return (
    <>
      <PageHeader
        eyebrow="Clientes"
        title={clientName}
        description="Fotos de referencia del cliente. Cada foto se guarda con su variante WebP optimizada."
        actions={
          <>
            <Link className="btn btn--quiet btn--sm" to="/panel/clientes">
              ← Todos los clientes
            </Link>
            <button type="button" className="btn btn--quiet btn--sm" onClick={reload}>
              Actualizar
            </button>
          </>
        }
      />

      {loading ? (
        <LoadingState label={loaded ? 'Actualizando ficha…' : 'Cargando ficha y fotos…'} />
      ) : null}
      {error ? <ErrorNotice error={error} onRetry={reload} /> : null}
      {notice ? (
        <p className="notice notice--success" role="status">
          {notice}
        </p>
      ) : null}

      <div className="client-detail">
        <section className="panel-card" aria-labelledby="client-info-title">
          <div className="panel-card__head">
            <h2 id="client-info-title">Datos del cliente</h2>
            <span className="panel-card__meta">{photos.length} fotos</span>
          </div>
          <dl className="client-info">
            <div>
              <dt>Nombre</dt>
              <dd>{clientName}</dd>
            </div>
            <div>
              <dt>Teléfono</dt>
              <dd>{displayPhone(client?.phone ?? '') || 'Sin teléfono'}</dd>
            </div>
            <div>
              <dt>Correo</dt>
              <dd>{client?.email ?? 'Sin correo'}</dd>
            </div>
            <div>
              <dt>Identificador</dt>
              <dd>
                <code>{clientId}</code>
              </dd>
            </div>
          </dl>
        </section>

        <section className="panel-card" aria-labelledby="client-photos-title">
          <div className="panel-card__head">
            <h2 id="client-photos-title">Fotos de referencia</h2>
            <span className="panel-card__meta">JPG, PNG o WebP · máx. 8 MB</span>
          </div>

          <PhotoUploader onUpload={handleUpload} disabled={!loaded} />

          {loaded && photos.length === 0 ? (
            <div className="empty-state">
              <h2>Sin fotos todavía</h2>
              <p>Sube una foto de referencia para que el equipo vea el resultado esperado.</p>
            </div>
          ) : null}

          <ul className="photos-grid">
            {photos.map((photo, index) => {
              const label = `Foto ${index + 1} de ${clientName}`
              return (
                <li key={photo.id}>
                  <figure className="photo-card">
                    <img
                      className="photo-card__img"
                      src={photo.url}
                      alt={label}
                      loading="lazy"
                      width={photo.width ?? undefined}
                      height={photo.height ?? undefined}
                      onError={handlePhotoError}
                    />
                    <figcaption className="photo-card__meta">
                      <span>{label}</span>
                      <span>{photoSizeLabel(photo)}</span>
                    </figcaption>
                    {deleteTarget === photo.id ? (
                      <div className="photo-confirm" role="group" aria-label="Confirmar borrado">
                        <span>¿Eliminar esta foto?</span>
                        <button
                          type="button"
                          className="btn btn--quiet btn--sm"
                          onClick={() => void confirmDelete()}
                          disabled={deleting}
                        >
                          {deleting ? 'Eliminando…' : 'Sí, eliminar'}
                        </button>
                        <button
                          type="button"
                          className="btn btn--quiet btn--sm"
                          onClick={() => setDeleteTarget(null)}
                          disabled={deleting}
                        >
                          Cancelar
                        </button>
                      </div>
                    ) : (
                      <div className="photo-card__actions">
                        <button
                          type="button"
                          className="btn btn--quiet btn--sm"
                          aria-label={`Eliminar ${label.toLowerCase()}`}
                          onClick={() => setDeleteTarget(photo.id)}
                        >
                          Eliminar
                        </button>
                      </div>
                    )}
                  </figure>
                </li>
              )
            })}
          </ul>
        </section>
      </div>
    </>
  )
}
