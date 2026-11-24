import type { PublicPhoto } from '../../lib/publicSalon'

type GalleryItemProps = {
  photo: PublicPhoto
  salonName: string
}

/** One photo of the salon gallery; the caption appears only when the salon wrote one. */
export function GalleryItem({ photo, salonName }: GalleryItemProps) {
  return (
    <figure className="gallery-item">
      <img
        src={photo.url}
        alt={photo.caption !== '' ? photo.caption : `Foto de ${salonName}`}
        loading="lazy"
        decoding="async"
      />
      {photo.caption !== '' ? (
        <figcaption>
          <span className="gallery-item__caption">{photo.caption}</span>
        </figcaption>
      ) : null}
    </figure>
  )
}
