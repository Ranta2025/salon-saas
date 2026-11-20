import type { GalleryEntry } from '../content'

type GalleryItemProps = {
  photo: GalleryEntry
}

/** One photo of the gallery: varied span + caption revealed on hover/focus. */
export function GalleryItem({ photo }: GalleryItemProps) {
  return (
    <figure className={`gallery-item gallery-item--${photo.variant}`}>
      <img src={photo.src} alt={photo.alt} loading="lazy" decoding="async" />
      <figcaption>
        <span className="gallery-item__caption">{photo.caption}</span>
        <span className="gallery-item__credit">
          {photo.author} · {photo.license}
        </span>
      </figcaption>
    </figure>
  )
}
