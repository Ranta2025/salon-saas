# Fotos de la demo (scripts/demo-assets)

Las usa el seed de demo (`backend/cmd/seed`) para la galería del salón y las
fotos de algunas fichas de clientas. El seed **no descarga nada**: lee estos
archivos ya versionados (o los encuentra montados en `/assets` dentro del stack
de Docker).

Cada archivo se descargó de Pexels, se recortó a 1200 px de ancho y se convirtió
a WebP con `cwebp` (calidad 76, sin metadatos). Todos pesan menos de 200 KB y
`file` los reconoce como WebP (VP8).

| Archivo | Qué muestra | Autor | Licencia | Origen |
|---|---|---|---|---|
| `salon-interior.webp` | interior del salón con espejos redondos iluminados | Max Vakhtbovych | Pexels License | https://www.pexels.com/photo/7750124/ |
| `corte-sillon.webp` | corte de pelo en el sillón, con capa | RDNE Stock project | Pexels License | https://www.pexels.com/photo/7697273/ |
| `manicura-detalle.webp` | manicura con torno y pulidor | José Antonio Otegui Auzmendi | Pexels License | https://www.pexels.com/photo/34930141/ |
| `manicura-torno.webp` | lima y torno sobre la mesa de manicura | Farhad Ibrahimzade | Pexels License | https://www.pexels.com/photo/19239100/ |
| `manicura-torno-rosa.webp` | torno con guantes de látex | Larysa Stratiichuk | Pexels License | https://www.pexels.com/photo/29877719/ |
| `manicura-lima.webp` | lima de uñas y guantes | Viktorya Sergeeva | Pexels License | https://www.pexels.com/photo/14267564/ |
| `manicura-esmaltado.webp` | esmaltado en el gabinete | Leeloo The First | Pexels License | https://www.pexels.com/photo/4677859/ |

## Licencia Pexels (resumen)

La [Pexels License](https://www.pexels.com/license/) permite usar, modificar y
distribuir las fotos gratis, incluso con fines comerciales, sin pedir permiso ni
dar atribución. Este archivo la da igual, porque es lo correcto: dice qué autor
hizo cada foto y de dónde salió. No se permite vender las fotos sin cambios,
usarlas para identificar a una persona ni insinuar que el autor respalda el
proyecto.

## Qué quedó afuera

Los candidatos de Unsplash (`3qREdx1J_OQ`, `je0w3E2BENQ`, `ShJ6l8_b4us`,
`taZSJ6xmt48`, `z8WmazKGtTY`) **no** se pudieron bajar: unsplash.com responde la
página de verificación anti-bot ("Checking your browser before accessing
unsplash.com") en vez de la imagen, también con `-L` y un `User-Agent` de
navegador. No se reemplazaron por originales dudosos: la galería usa solo las
siete fotos de Pexels verificadas de arriba.
