import type { PublicPhoto } from '../../lib/publicSalon'
import { GalleryItem } from './GalleryItem'

type GallerySectionProps = {
  photos: readonly PublicPhoto[]
  salonName: string
}

/** Rendered only when the salon uploaded photos from the panel. */
export function GallerySection({ photos, salonName }: GallerySectionProps) {
  return (
    <section className="section gallery on-dark" id="galeria" aria-labelledby="gallery-title">
      <div className="container">
        <div className="gallery__head">
          <div className="section-head">
            <p className="eyebrow">Galería</p>
            <h2 id="gallery-title">Galería</h2>
          </div>
        </div>

        <div className="gallery__grid" data-count={photos.length}>
          {photos.map((photo) => (
            <GalleryItem key={photo.id} photo={photo} salonName={salonName} />
          ))}
        </div>
      </div>
    </section>
  )
}
