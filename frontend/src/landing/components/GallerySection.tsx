import { GALLERY } from '../content'
import { GalleryItem } from './GalleryItem'

export function GallerySection() {
  return (
    <section className="section gallery on-dark" id="galeria" aria-labelledby="gallery-title">
      <div className="container">
        <div className="gallery__head">
          <div className="section-head">
            <p className="eyebrow">Galería</p>
            <h2 id="gallery-title">Así se siente salir del salón</h2>
            <p className="lede">
              Referencias de color, corte, uñas y maquillaje que replicamos en cada cita. Las fotos
              vienen de bancos de imágenes libres: los créditos están al pie de la página.
            </p>
          </div>
          <a className="btn btn--ghost" href="#reservar">
            Quiero un cambio así
          </a>
        </div>

        <div className="gallery__grid">
          {GALLERY.map((photo) => (
            <GalleryItem key={photo.src} photo={photo} />
          ))}
        </div>
      </div>
    </section>
  )
}
