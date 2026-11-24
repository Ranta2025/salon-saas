import { useCallback, useEffect, useRef, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import {
  deleteClientPhoto,
  describeApiError,
  fetchClientPhotos,
  uploadClientPhoto,
  type ApiErrorNotice,
} from '../lib/api'
import {
  createClientSaveGate,
  fetchClientRecord,
  saveClientRecord,
  type ClientRecord,
  type ClientRecordInput,
} from '../lib/clientRecord'
import { fetchClientHistory, type ClientHistory } from '../lib/clientHistory'
import { canEditClient, canSeeClientMoney, activeRole } from '../lib/permissions'
import { formatBytes, validatePhotoFile } from '../lib/photos'
import type { ClientPhoto } from '../types'
import { useSalon } from './SalonContext'
import { ErrorNotice, LoadingState, SalonRequired } from './components/Feedback'
import { ClientHistorySection } from './components/ClientHistorySection'
import { ClientRecordAllergies } from './components/ClientRecordAllergies'
import { ClientRecordForm } from './components/ClientRecordForm'
import { ClientRecordSummary } from './components/ClientRecordSummary'
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

/**
 * `/panel/clientes/:id` (contract F5): the file comes from `GET /clients/:cid`,
 * the allergies are shown to every member, the edit form (five keys, owner/admin/
 * reception only) reloads the file from the server after saving, and the history
 * shows visits, no-shows, next and last visit — with the money ONLY when the API
 * sends `spend`, which it never does for a professional.
 */
export function ClientDetailPage() {
  const { salonId, salons } = useSalon()
  const { clientId = '' } = useParams<{ clientId: string }>()
  const [record, setRecord] = useState<ClientRecord | null>(null)
  const [photos, setPhotos] = useState<ClientPhoto[]>([])
  const [history, setHistory] = useState<ClientHistory | null>(null)
  const [loading, setLoading] = useState(false)
  const [loaded, setLoaded] = useState(false)
  const [error, setError] = useState<ApiErrorNotice | null>(null)
  const [historyError, setHistoryError] = useState<ApiErrorNotice | null>(null)
  const [historyLoading, setHistoryLoading] = useState(false)
  const [notice, setNotice] = useState<string | null>(null)
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null)
  const [deleting, setDeleting] = useState(false)
  const [reloadKey, setReloadKey] = useState(0)
  // The photo url is a signed URL that expires: a failed load triggers exactly one
  // refetch of the list, which comes back with fresh signatures.
  const signatureRefresh = useRef(false)
  // One save at a time: a double click cannot send two PUTs (the state only paints
  // the disabled button).
  const saveGate = useRef(createClientSaveGate())

  const reload = useCallback(() => setReloadKey((key) => key + 1), [])

  useEffect(() => {
    if (!salonId || clientId === '') {
      return
    }
    let active = true
    const controller = new AbortController()
    setLoading(true)
    setError(null)
    Promise.all([
      fetchClientRecord(salonId, clientId, controller.signal),
      fetchClientPhotos(salonId, clientId, controller.signal),
    ])
      .then(([clientRecord, photoList]) => {
        if (!active) {
          return
        }
        setRecord(clientRecord)
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

  // The history is its own request: a failure here never hides the file.
  useEffect(() => {
    if (!salonId || clientId === '') {
      return
    }
    let active = true
    const controller = new AbortController()
    setHistoryLoading(true)
    setHistoryError(null)
    fetchClientHistory(salonId, clientId, controller.signal)
      .then((loadedHistory) => {
        if (active) {
          setHistory(loadedHistory)
        }
      })
      .catch((err: unknown) => {
        if (active && !controller.signal.aborted) {
          setHistoryError(describeApiError(err, 'No pudimos cargar el historial de la clienta.'))
        }
      })
      .finally(() => {
        if (active) {
          setHistoryLoading(false)
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

  const handleSave = useCallback(
    async (input: ClientRecordInput): Promise<void> => {
      if (!salonId) {
        return
      }
      setSaving(true)
      setSaveError(null)
      setNotice(null)
      const outcome = await saveClientRecord(saveGate.current, salonId, clientId, input)
      setSaving(false)
      if (outcome.status !== 'saved') {
        setSaveError(outcome.message)
        return
      }
      setNotice('Ficha guardada. Volvimos a pedirla al servidor.')
      // No optimistic state: the file, the allergies and the history are read
      // again from the server, so the screen shows what the API stored.
      reload()
    },
    [salonId, clientId, reload],
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

  const role = activeRole(salons, salonId)
  const canEdit = canEditClient(role)
  const clientName = record?.name ?? 'Cliente sin nombre'

  return (
    <>
      <PageHeader
        eyebrow="Clientes"
        title={clientName}
        description="Ficha, alergias, historial de visitas y fotos de referencia del trabajo."
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

      {record ? <ClientRecordAllergies allergies={record.allergies} /> : null}

      {record ? <ClientRecordSummary record={record} /> : null}

      <ClientHistorySection
        history={history}
        error={historyError}
        loading={historyLoading}
        onRetry={reload}
        canSeeMoney={canSeeClientMoney(role)}
      />

      {record && canEdit ? (
        <ClientRecordForm
          record={record}
          saving={saving}
          serverError={saveError}
          onSave={(input) => void handleSave(input)}
        />
      ) : null}

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
    </>
  )
}
