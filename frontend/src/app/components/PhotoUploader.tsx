import { useEffect, useRef, useState, type ChangeEvent, type FormEvent } from 'react'
import { PHOTO_ACCEPT, formatBytes, validatePhotoFile } from '@/features/clients/lib/photos'

type PhotoUploaderProps = {
  /** Performs the multipart upload; a rejection is rendered as a friendly error. */
  onUpload: (file: File) => Promise<void>
  disabled?: boolean
}

/**
 * Local preview + client-side validation (type and 8 MB) before the file is
 * sent. The preview is an object URL revoked when it is replaced or unmounted.
 */
export function PhotoUploader({ onUpload, disabled = false }: PhotoUploaderProps) {
  const [file, setFile] = useState<File | null>(null)
  const [previewUrl, setPreviewUrl] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [pending, setPending] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  useEffect(() => {
    return () => {
      if (previewUrl) {
        URL.revokeObjectURL(previewUrl)
      }
    }
  }, [previewUrl])

  const clearSelection = (): void => {
    setFile(null)
    setPreviewUrl(null)
    if (inputRef.current) {
      inputRef.current.value = ''
    }
  }

  const handleChange = (event: ChangeEvent<HTMLInputElement>): void => {
    setError(null)
    const chosen = event.target.files?.[0] ?? null
    if (!chosen) {
      clearSelection()
      return
    }
    const validation = validatePhotoFile(chosen)
    if (!validation.ok) {
      setError(validation.message)
      clearSelection()
      return
    }
    setFile(chosen)
    setPreviewUrl(URL.createObjectURL(chosen))
  }

  const handleSubmit = async (event: FormEvent<HTMLFormElement>): Promise<void> => {
    event.preventDefault()
    if (!file || pending || disabled) {
      return
    }
    setError(null)
    setPending(true)
    try {
      await onUpload(file)
      clearSelection()
    } catch (err) {
      setError(
        err instanceof Error && err.message !== ''
          ? err.message
          : 'No pudimos subir la foto. Intenta de nuevo.',
      )
    } finally {
      setPending(false)
    }
  }

  return (
    <form className="uploader" onSubmit={(event) => void handleSubmit(event)} noValidate>
      <div className="field">
        <label htmlFor="photo-file">Añadir foto (JPG, PNG o WebP, máximo 8 MB)</label>
        <input
          ref={inputRef}
          id="photo-file"
          className="input input--file"
          type="file"
          accept={PHOTO_ACCEPT}
          onChange={handleChange}
          disabled={pending || disabled}
        />
      </div>

      {previewUrl && file ? (
        <div className="uploader__preview">
          <img
            className="uploader__img"
            src={previewUrl}
            alt="Vista previa de la foto que vas a subir"
          />
          <div className="uploader__meta">
            <p className="uploader__file">{file.name}</p>
            <p className="uploader__file">{formatBytes(file.size)}</p>
            <p className="field__hint">
              Se guarda una variante WebP de hasta 1280 px del lado mayor.
            </p>
          </div>
        </div>
      ) : (
        <p className="field__hint">
          Elige una foto para verla aquí antes de enviarla al servidor.
        </p>
      )}

      {error ? (
        <p className="notice notice--error" role="alert">
          {error}
        </p>
      ) : null}

      <div className="uploader__actions">
        <button
          type="submit"
          className="btn btn--wine btn--sm"
          disabled={!file || pending || disabled}
        >
          {pending ? 'Subiendo…' : 'Subir foto'}
        </button>
        <button
          type="button"
          className="btn btn--quiet btn--sm"
          onClick={clearSelection}
          disabled={!file || pending}
        >
          Descartar
        </button>
      </div>
    </form>
  )
}
