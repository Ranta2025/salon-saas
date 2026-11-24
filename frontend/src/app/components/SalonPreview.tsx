import type { LandingModel } from '../../lib/publicSalon'
import { PROFILE_HELP } from '../../lib/salonProfile'

type SalonPreviewProps = {
  /** Built by `previewLanding`: the same builder the public page uses. */
  model: LandingModel
  /** Reception cannot read the gallery, so its absence here says nothing about the page. */
  galleryKnown: boolean
}

/**
 * «Esto es lo que ve el cliente en tu página pública»: the blocks of the public
 * page that depend on this screen, with what is empty marked as hidden. The
 * services, team and packages of the page come from their own screens.
 */
export function SalonPreview({ model, galleryKnown }: SalonPreviewProps) {
  const rows: { label: string; value: string | null }[] = [
    { label: 'Dirección', value: model.address },
    { label: 'Teléfono', value: model.phone?.display ?? null },
    { label: 'Botón de WhatsApp', value: model.phone?.whatsapp ? 'Se muestra' : null },
    { label: 'Email', value: model.email },
    { label: 'Instagram', value: model.instagram ? `@${model.instagram.handle}` : null },
  ]
  return (
    <section className="panel-card salon-preview" aria-labelledby="salon-preview-title">
      <div className="panel-card__head">
        <h2 id="salon-preview-title">Vista previa</h2>
      </div>
      <p className="field__hint">{PROFILE_HELP.preview}</p>

      <div className="salon-preview__hero">
        {model.heroPhoto ? (
          <img
            className="salon-preview__photo"
            src={model.heroPhoto.url}
            alt="Primera foto de la galería, que va de fondo en la portada"
          />
        ) : null}
        <p className="salon-preview__name">{model.name === '' ? 'Sin nombre' : model.name}</p>
        {model.description !== null ? (
          <p className="salon-preview__lede">{model.description}</p>
        ) : (
          <p className="salon-preview__lede salon-preview__lede--hidden">
            Sin descripción: la portada no muestra texto bajo el nombre.
          </p>
        )}
      </div>

      <dl className="salon-preview__rows">
        {rows.map((row) => (
          <div key={row.label} data-hidden={row.value === null ? 'true' : undefined}>
            <dt>{row.label}</dt>
            <dd>{row.value ?? 'Vacío: no se muestra'}</dd>
          </div>
        ))}
        <div data-hidden={galleryKnown && !model.sections.gallery ? 'true' : undefined}>
          <dt>Galería</dt>
          <dd>
            {!galleryKnown
              ? 'La administra la dueña o la administración'
              : model.sections.gallery
                ? `${model.gallery.length} ${model.gallery.length === 1 ? 'foto' : 'fotos'}`
                : 'Sin fotos: la sección no se muestra'}
          </dd>
        </div>
      </dl>

      {model.gallery.length > 0 ? (
        <ul className="salon-preview__thumbs" aria-label="Fotos de la galería pública">
          {model.gallery.map((photo) => (
            <li key={photo.id}>
              <img src={photo.url} alt={photo.caption !== '' ? photo.caption : 'Foto del salón'} loading="lazy" />
            </li>
          ))}
        </ul>
      ) : null}
      <p className="field__hint">
        Los servicios con su descripción, el equipo, los paquetes y los horarios se editan en sus
        propias pantallas y se publican solos.
      </p>
    </section>
  )
}
