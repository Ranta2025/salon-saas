# Fase 3 — frontend: landing pública + panel del salón

Documento de diseño de la unidad de trabajo de frontend (T3 y T4 de
`odd/tasks/fase3.md`). Stack: React 18 + TypeScript + Vite, `react-router-dom`
y CSS plano con custom properties. Sin librerías de UI, sin Tailwind.

## 1. Paleta y tokens

La paleta la fija el orquestador y vive **en un solo lugar**:
`frontend/src/styles/theme.css`.

| token        | valor     | uso                                                     |
| ------------ | --------- | ------------------------------------------------------- |
| `--ink`      | `#1E1418` | texto principal sobre fondo claro, pie de página        |
| `--plum`     | `#3B1E33` | hero, galería, sidebar del panel                        |
| `--wine`     | `#6B2D4F` | primario de marca (precios, botones, foco en claro)     |
| `--gold`     | `#C9A227` | CTA principal, filetes, acentos                         |
| `--champagne`| `#E8D9B5` | acento suave sobre oscuro, texto secundario en plum     |
| `--cream`    | `#FAF5EF` | fondo claro de la landing                               |
| `--rose`     | `#E7C9D2` | tinte secundario (barras, estado "no asistió")          |

Tokens derivados (`--ink-soft`, `--line`, `--gold-tint`, `--rose-tint`,
`--focus-light`, `--focus-dark`) evitan colores ad-hoc en los componentes.

Contraste verificado para texto de cuerpo: `--ink` sobre `--cream` y
`--cream` / `--champagne` sobre `--plum` superan 4.5:1. El dorado **no** se usa
como color de texto sobre crema (2.2:1): solo filetes, íconos y botones con
texto `--ink` encima (7.4:1).

## 2. Tipografía

- Display: **Cormorant Garamond** (SIL OFL), woff2 autohospedado en
  `frontend/public/fonts/cormorant-garamond-latin.woff2` (subset latino,
  37 KB), declarado con `@font-face` y fallback
  `Georgia, 'Times New Roman', serif`. Se precarga en `index.html`.
- Texto: stack del sistema (`system-ui, -apple-system, 'Segoe UI', Roboto…`).
- Sin `<link>` a Google Fonts: la página renderiza igual sin red.

## 3. Landing pública (`/` y `/salon/:slug`)

Ruta `/` y `/salon/:slug` comparten `LandingPage`. Orden de secciones:

| sección | id | foto |
| --- | --- | --- |
| header sticky (nav + CTA Reservar) | — | — |
| hero con overlay en degradado | `inicio` | `hero-atwork.jpg` |
| franja de confianza (4 puntos) | — | — |
| servicios y precios (8 tarjetas) | `servicios` | — |
| galería en mosaico con hover | `galeria` | 10 fotos (ver abajo) |
| por qué elegirnos (imagen + 4 razones) | `nosotros` | `gallery-02-iran01.jpg` |
| testimonios | `opiniones` | — |
| CTA final + formulario de reserva | `reservar` | — |
| footer con contacto y créditos | — | — |

Reparto de las 12 fotos verificadas:

| archivo | sección |
| --- | --- |
| `hero-atwork.jpg` | hero (fondo con degradado plum) |
| `gallery-02-iran01.jpg` | "por qué elegirnos" (columna de imagen) |
| `gallery-09-portrait.jpg` | galería, pieza destacada (2×3) |
| `gallery-06-coloring.jpg` | galería, alta |
| `gallery-11-nails.jpg` | galería, alta |
| `gallery-05-iran08.jpg` | galería, alta |
| `gallery-01-iran09.jpg` | galería, cuadrada |
| `gallery-03-iran04.jpg` | galería, ancha (2 columnas) |
| `gallery-04-iran06.jpg` | galería, cuadrada |
| `gallery-08-makeup.jpg` | galería, cuadrada |
| `gallery-10-lip.jpg` | galería, cuadrada |
| `gallery-07-completing.jpg` | galería, cuadrada (fuente 240×320: se usa en tile chico) |

La galería no es una grilla plana: `grid-auto-flow: dense` con variantes
`--feature` (2×3), `--tall` (1×2), `--wide` (2×1) y `--small`, y caption con
autor y licencia que aparece en hover/focus (siempre visible en táctil).
Las 12 fotos, con autor, licencia y enlace a Commons, están listadas en el
footer ("Créditos de fotografía") como exigen las licencias CC BY / CC BY-SA.

### Reserva

No existe endpoint público de reservas, así que el CTA final es un formulario
(nombre, teléfono, servicio, fecha y horario preferidos) que arma un mensaje y
abre WhatsApp del salón, con teléfono y correo como alternativas. No simula una
API que todavía no existe.

### Responsive y accesibilidad

- Mobile-first; breakpoints en 560, 700, 900, 980, 1100 px. Verificado a 375,
  768 y 1280 px con capturas headless.
- Landmarks: `header`/`main`/`footer`, un solo `h1`, `section` con
  `aria-labelledby`, `nav` con `aria-label`, skip-link al contenido.
- Todas las fotos tienen `alt` que describe el sujeto real de la imagen;
  las decorativas van con `aria-hidden`.
- Foco visible: `:focus-visible` con outline de 3 px, wine en claro y
  champagne en oscuro.
- `prefers-reduced-motion` desactiva transiciones y scroll suave.

## 4. Panel interno

| ruta | página | endpoint |
| --- | --- | --- |
| `/login` | `LoginPage` | `POST /api/v1/auth/login` → token en `localStorage` |
| `/panel` | `DashboardPage` | `GET /salons/:id/dashboard` |
| `/panel/calendario` | `CalendarPage` | `GET /salons/:id/bookings?from=&to=` |
| `/panel/pos` | `PosPage` + `POSPanel` | `GET /salons/:id/services`, `POST /salons/:id/sales`, `GET /salons/:id/sales?from=&to=` |
| `/panel/servicios` | `ServicesPage` + `ServiceForm` | `GET/POST /salons/:id/services`, `PUT /salons/:id/services/:sid` |

Las rutas viejas del esqueleto (`/dashboard`, `/calendar`, `/pos`, `/clients`)
redirigen a sus equivalentes nuevas. `RequireAuth` protege `/panel/**` y un 401
en cualquier request limpia el token y manda a `/login` (evento
`salon:unauthorized`).

- **Salón activo**: la API todavía no expone "mis salones"; el id se guarda en
  `localStorage` (`salon.salon_id`) y se edita desde el selector de la barra
  superior. Sin id, cada página muestra un estado que lo explica.
- **Dinero**: la UI calcula el total de la venta con enteros (centavos) para
  mostrarlo, pero muestra también el total que devolvió el servidor y advierte
  si difieren. El payload de venta no incluye `total_cents`.
- **Calendario**: columna por profesional en desktop (bloques posicionados por
  minuto, con carriles para citas solapadas) y agenda en lista en móvil. Los
  profesionales se agrupan por `professional_id` porque aún no hay endpoint de
  profesionales.
- **POS**: catálogo filtrable, carrito con cantidades, comisión y duración
  visibles solo en el catálogo de servicios. El ID de profesional se captura a
  mano (la API lo exige).
- **CSS del panel**: `frontend/src/styles/app.css`, misma paleta, densidad
  mayor (sidebar, cards, tablas), sin inline styles salvo geometría calculada
  (posición de citas y ancho de barras).

## 5. Estructura de archivos

```
frontend/
  index.html                 meta, precarga de fuente, título
  public/img/                12 fotos verificadas
  public/fonts/              woff2 de Cormorant Garamond
  src/main.tsx               estilos globales + <App/>
  src/App.tsx                rutas y redirección por 401
  src/types.ts               contrato de la API (centavos enteros)
  src/lib/api.ts             base URL, auth, errores tipados, endpoints
  src/lib/session.ts         token y salón en localStorage (con fallback)
  src/lib/money.ts           formato/parseo de centavos, duraciones
  src/lib/datetime.ts        día del salón, minutos, solapes, agenda
  src/lib/metrics.ts         normalización del dashboard
  src/lib/posCart.ts         carrito del POS (lógica pura)
  src/styles/theme.css       paleta y tokens
  src/styles/base.css        reset, a11y, botones, formularios
  src/styles/landing.css     landing
  src/styles/app.css         panel
  src/landing/               LandingPage, contenido y componentes
  src/app/                   páginas y componentes del panel
```

## 6. Pruebas

Lógica de negocio del frontend probada con el runner nativo de Node
(`node --test`, sin dependencias nuevas): `money`, `datetime`, `metrics`,
`posCart` y el cliente `api` (URL, Authorization, errores HTTP, 401, red).
Los componentes son presentacionales; su validación es `tsc --noEmit` +
`vite build` + revisión visual a 375/768/1280.
