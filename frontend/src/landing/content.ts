/** Copy and content of the public landing page. UI copy is Spanish, identifiers English. */

/**
 * Text shown while the public API does not send the `hours` of the salon
 * (F2d1, S-F2d1-6). The landing never invents opening hours: it invites to
 * confirm them on WhatsApp instead.
 */
export const PUBLIC_HOURS_FALLBACK = 'Consultá el horario por WhatsApp'

/**
 * Contact phone of the salon: ONE constant for the header, the footer and the
 * booking CTA. `display` is what the page prints, `tel` is the link of the
 * header/footer and `whatsapp` is the digits-only number `wa.me` expects.
 */
export const CONTACT_PHONE = {
  e164: '+5355528595',
  display: '+53 5552 8595',
  tel: 'tel:+5355528595',
  whatsapp: '5355528595',
} as const

export const SALON = {
  name: 'Aurelia',
  tagline: 'Estudio de belleza',
  district: 'Roma Norte, Ciudad de México',
  address: 'Av. Álvaro Obregón 128, Roma Norte, 06700, CDMX',
  phone: CONTACT_PHONE.display,
  phoneHref: CONTACT_PHONE.tel,
  whatsapp: CONTACT_PHONE.whatsapp,
  email: 'hola@aurelia.mx',
  instagram: '@aurelia.estudio',
  instagramUrl: 'https://instagram.com/',
} as const

export type NavLink = { href: string; label: string }

export const NAV_LINKS: readonly NavLink[] = [
  { href: '#servicios', label: 'Servicios' },
  { href: '#galeria', label: 'Galería' },
  { href: '#nosotros', label: 'Nosotras' },
  { href: '#opiniones', label: 'Opiniones' },
]

export type LandingService = {
  id: string
  name: string
  durationMin: number
  priceCents: number
  description: string
  tag?: string
}

export const SERVICES: readonly LandingService[] = [
  {
    id: 'corte',
    name: 'Corte y peinado',
    durationMin: 45,
    priceCents: 38000,
    description: 'Corte a la medida de tu rostro y textura, con lavado y terminado a mano.',
  },
  {
    id: 'coloracion',
    name: 'Coloración completa',
    durationMin: 120,
    priceCents: 129000,
    description: 'Color uniforme, retoque de raíz o cobertura de canas con diagnóstico previo.',
    tag: 'Más pedido',
  },
  {
    id: 'keratina',
    name: 'Keratina',
    durationMin: 120,
    priceCents: 145000,
    description: 'Alaciado progresivo que reduce el frizz y devuelve brillo sin maltratar.',
  },
  {
    id: 'alisado',
    name: 'Alisado permanente',
    durationMin: 150,
    priceCents: 169000,
    description: 'Liso duradero con productos libres de formol, según tu tipo de cabello.',
  },
  {
    id: 'manicure',
    name: 'Manicure y pedicure',
    durationMin: 75,
    priceCents: 45000,
    description: 'Limado, cutícula, esmaltado en gel o tradicional y diseño a elección.',
  },
  {
    id: 'maquillaje',
    name: 'Maquillaje profesional',
    durationMin: 60,
    priceCents: 89000,
    description: 'Maquillaje social o de novia con productos de larga duración y pestañas.',
  },
  {
    id: 'tratamiento',
    name: 'Tratamiento capilar',
    durationMin: 50,
    priceCents: 62000,
    description: 'Hidratación profunda con masaje de cuero cabelludo para cabello maltratado.',
  },
  {
    id: 'evento',
    name: 'Peinado de evento',
    durationMin: 60,
    priceCents: 75000,
    description: 'Ondas, recogidos y trenzas para boda, graduación o sesión de fotos.',
  },
]

export type PhotoLicense = 'CC0' | 'CC BY 2.0' | 'CC BY-SA 4.0'

export type SalonPhoto = {
  src: string
  /** Intrinsic size of the file; reserves space and avoids layout shift. */
  width: number
  height: number
  alt: string
  caption: string
  title: string
  author: string
  license: PhotoLicense
  commons: string
}

/** Verified Wikimedia Commons photos (see docs/assets/images.md). */
export const PHOTOS = {
  hero: {
    src: '/img/hero-atwork.webp',
    width: 1400,
    height: 2100,
    alt: 'Estilista acomodando el cabello trenzado de una clienta en el salón',
    caption: 'Estilista trabajando en el salón',
    title: 'Hairdresser at work.jpg',
    author: 'Rwebogora',
    license: 'CC BY-SA 4.0',
    commons: 'https://commons.wikimedia.org/wiki/File:Hairdresser_at_work.jpg',
  },
  interior: {
    src: '/img/gallery-02-iran01.webp',
    width: 1400,
    height: 934,
    alt: 'Estilista atendiendo a un cliente con capa de rayas en el interior del salón',
    caption: 'Atención personalizada',
    title: 'Haircut or hair Salon From Iran- Canon Photography- Mostafa Meraji- fashion Photo 01.jpg',
    author: 'Mostafameraji',
    license: 'CC0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Haircut_or_hair_Salon_From_Iran-_Canon_Photography-_Mostafa_Meraji-_fashion_Photo_01.jpg',
  },
  stylistDetail: {
    src: '/img/gallery-01-iran09.webp',
    width: 1400,
    height: 2100,
    alt: 'Barbero recortando el cabello de un cliente con tijera y máquina',
    caption: 'Corte a tijera',
    title: 'Haircut or hair Salon From Iran- Canon Photography- Mostafa Meraji- fashion Photo 09.jpg',
    author: 'Mostafameraji',
    license: 'CC0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Haircut_or_hair_Salon_From_Iran-_Canon_Photography-_Mostafa_Meraji-_fashion_Photo_09.jpg',
  },
  salonWide: {
    src: '/img/gallery-03-iran04.webp',
    width: 1400,
    height: 934,
    alt: 'Cliente en el sillón mientras le cortan el cabello frente a los espejos del salón',
    caption: 'Sillón y espejos',
    title: 'Haircut or hair Salon From Iran- Canon Photography- Mostafa Meraji- fashion Photo 04.jpg',
    author: 'Mostafameraji',
    license: 'CC0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Haircut_or_hair_Salon_From_Iran-_Canon_Photography-_Mostafa_Meraji-_fashion_Photo_04.jpg',
  },
  beardTrim: {
    src: '/img/gallery-04-iran06.webp',
    width: 1400,
    height: 934,
    alt: 'Detalle de un barbero perfilando el corte y la barba de un cliente',
    caption: 'Detalle y precisión',
    title: 'Haircut or hair Salon From Iran- Canon Photography- Mostafa Meraji- fashion Photo 06.jpg',
    author: 'Mostafameraji',
    license: 'CC0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Haircut_or_hair_Salon_From_Iran-_Canon_Photography-_Mostafa_Meraji-_fashion_Photo_06.jpg',
  },
  stylistWide: {
    src: '/img/gallery-05-iran08.webp',
    width: 1400,
    height: 2100,
    alt: 'Barbero trabajando el corte de un cliente junto a una planta del salón',
    caption: 'Oficio en el salón',
    title: 'Haircut or hair Salon From Iran- Canon Photography- Mostafa Meraji- fashion Photo 08.jpg',
    author: 'Mostafameraji',
    license: 'CC0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Haircut_or_hair_Salon_From_Iran-_Canon_Photography-_Mostafa_Meraji-_fashion_Photo_08.jpg',
  },
  coloring: {
    src: '/img/gallery-06-coloring.webp',
    width: 1400,
    height: 1621,
    alt: 'Estilista aplicando mechas con papel aluminio sobre cabello rubio',
    caption: 'Mechas y coloración',
    title: 'Hair coloring at hair salon (51213255633).jpg',
    author: 'Hair Spies from US',
    license: 'CC BY 2.0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Hair_coloring_at_hair_salon_(51213255633).jpg',
  },
  makeup: {
    src: '/img/gallery-08-makeup.webp',
    width: 1400,
    height: 934,
    alt: 'Maquillista aplicando sombra en el ojo de una clienta',
    caption: 'Maquillaje de cerca',
    title: 'Makeup Technician.jpg',
    author: 'Fquasie',
    license: 'CC BY-SA 4.0',
    commons: 'https://commons.wikimedia.org/wiki/File:Makeup_Technician.jpg',
  },
  portrait: {
    src: '/img/gallery-09-portrait.webp',
    width: 1400,
    height: 1542,
    alt: 'Retrato de una mujer de cabello largo y oscuro con una flor blanca en el cabello',
    caption: 'Retrato con styling',
    title: 'Ishitta R Chowdhary.jpg',
    author: 'Ishitta Chowdhary',
    license: 'CC BY-SA 4.0',
    commons: 'https://commons.wikimedia.org/wiki/File:Ishitta_R_Chowdhary.jpg',
  },
  lipLiner: {
    src: '/img/gallery-10-lip.webp',
    width: 1400,
    height: 935,
    alt: 'Acercamiento de una maquillista delineando los labios de una clienta',
    caption: 'Delineado perfecto',
    title: 'Applying lip liner in a beauty setting during a makeup session.jpg',
    author: 'Shixart1985',
    license: 'CC BY 2.0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Applying_lip_liner_in_a_beauty_setting_during_a_makeup_session.jpg',
  },
  nails: {
    src: '/img/gallery-11-nails.webp',
    width: 1400,
    height: 2098,
    alt: 'Manicurista dibujando un diseño en la uña de una clienta con un pincel',
    caption: 'Nail art a mano alzada',
    title: 'Nail technician applies a nail design at a manicure table during a salon appointment.jpg',
    author: 'Shixart1985',
    license: 'CC BY 2.0',
    commons:
      'https://commons.wikimedia.org/wiki/File:Nail_technician_applies_a_nail_design_at_a_manicure_table_during_a_salon_appointment.jpg',
  },
} satisfies Record<string, SalonPhoto>

export type GalleryVariant = 'feature' | 'tall' | 'wide' | 'small'

export type GalleryEntry = SalonPhoto & { variant: GalleryVariant }

/**
 * Gallery with complete rows: 2×2 base layout up to 1099 px (eight equal cells)
 * and, from 1100 px, 2×3 + 1×2 + 1×2 + 2×1 + 4×1 = 16 cells in a 4-column grid.
 */
export const GALLERY: readonly GalleryEntry[] = [
  { ...PHOTOS.portrait, variant: 'feature' },
  { ...PHOTOS.coloring, variant: 'tall' },
  { ...PHOTOS.nails, variant: 'tall' },
  { ...PHOTOS.salonWide, variant: 'wide' },
  { ...PHOTOS.makeup, variant: 'small' },
  { ...PHOTOS.stylistDetail, variant: 'small' },
  { ...PHOTOS.lipLiner, variant: 'small' },
  { ...PHOTOS.beardTrim, variant: 'small' },
]

/** Every photo used on the page, for the licence attribution in the footer. */
export const ATTRIBUTION: readonly SalonPhoto[] = [
  PHOTOS.hero,
  PHOTOS.stylistDetail,
  PHOTOS.interior,
  PHOTOS.salonWide,
  PHOTOS.beardTrim,
  PHOTOS.coloring,
  PHOTOS.makeup,
  PHOTOS.portrait,
  PHOTOS.lipLiner,
  PHOTOS.nails,
]

export type Reason = { title: string; body: string }

export const REASONS: readonly Reason[] = [
  {
    title: 'Diagnóstico antes del primer corte',
    body: 'Revisamos tu historial de color y la salud de tu cabello antes de proponerte cualquier servicio.',
  },
  {
    title: 'Especialistas certificadas',
    body: 'Coloristas y estilistas con formación continua en técnicas de color y tratamientos capilares.',
  },
  {
    title: 'Productos profesionales y sin crueldad',
    body: 'Marcas de salón, libres de formol y nunca probadas en animales. Te decimos qué usamos.',
  },
  {
    title: 'Tiempo real, sin prisas',
    body: 'Agenda por servicio y profesional: reservas el tiempo que necesitas y nadie corre contigo.',
  },
]

export type Testimonial = { quote: string; name: string; service: string }

export const TESTIMONIALS: readonly Testimonial[] = [
  {
    quote:
      'Llegué con un rubio desteñido y salí con el tono que quería. Me explicaron cada paso y cómo cuidarlo en casa.',
    name: 'Mariana T.',
    service: 'Coloración y tratamiento',
  },
  {
    quote:
      'La keratina me duró casi cuatro meses y no me dejó el cabello rígido. Ya soy clienta fija del salón.',
    name: 'Paola R.',
    service: 'Keratina',
  },
  {
    quote:
      'Reservé peinado para la boda de mi hermana y llegué puntual, sin esperas. El recogido aguantó toda la noche.',
    name: 'Daniela V.',
    service: 'Peinado de evento',
  },
]

export type Stat = { value: string; label: string }

export const STATS: readonly Stat[] = [
  { value: '12 años', label: 'en Roma Norte' },
  { value: '+4 800', label: 'clientas por año' },
  { value: '4.9 / 5', label: 'en reseñas públicas' },
]

export const TRUST_POINTS: readonly string[] = [
  'Diagnóstico personalizado',
  'Productos profesionales',
  'Reserva en línea',
  'Pago en efectivo, tarjeta o transferencia',
]
