import { TESTIMONIALS } from '../content'

export function TestimonialsSection() {
  return (
    <section className="section testimonials" id="opiniones" aria-labelledby="testimonials-title">
      <div className="container">
        <div className="section-head">
          <p className="eyebrow">Opiniones</p>
          <h2 id="testimonials-title">Lo que dicen nuestras clientas</h2>
        </div>

        <div className="testimonials__grid">
          {TESTIMONIALS.map((testimonial) => (
            <figure key={testimonial.name} className="testimonial">
              <p className="stars">
                <span aria-hidden="true">★★★★★</span>
                <span className="visually-hidden">Calificación: 5 de 5 estrellas.</span>
              </p>
              <blockquote>
                <p>{testimonial.quote}</p>
              </blockquote>
              <figcaption className="testimonial__author">
                <strong>{testimonial.name}</strong>
                <span>{testimonial.service}</span>
              </figcaption>
            </figure>
          ))}
        </div>
      </div>
    </section>
  )
}
