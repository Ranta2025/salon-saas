# Fase 5 — frontend: hardening de la auditoría (T4)

Cierre de los hallazgos `F-01..F-24` de la auditoría de la fase 5 sobre el
frontend de la fase 3 y 4. Stack sin cambios: React 18 + TypeScript strict +
Vite, `react-router-dom`, CSS plano con custom properties y service worker
vanilla. Sin dependencias nuevas.

## 1. Entorno y despliegue

| variable | dónde | efecto |
| --- | --- | --- |
| `VITE_API_URL` | build (`ARG` del Dockerfile) | URL absoluta de la API que se hornea en el bundle |
| `VITE_API_PROXY_TARGET` | solo dev (`vite.config.ts`) | destino del proxy `/api` (por defecto `http://localhost:8080`) |
| `VITE_SALON_SLUG` | build | salón público que muestra `/` cuando no hay `:slug` (por defecto `aurelia`) |

- Sin `VITE_API_URL`, la app pide rutas relativas (`/api/v1/...`). En desarrollo
  Vite las proxya a la API, así que **el navegador nunca hace un request
  cross-origin y el desarrollo local no depende de CORS**.
- `buildWebSocketUrl` resuelve `WS /api/v1/ws` contra el origen de la página
  cuando la base es relativa.
- `frontend/Dockerfile` es multietapa: `node:20-slim` compila con
  `ARG VITE_API_URL`, `nginx:1.27-alpine` sirve `dist/` en el puerto 80 con
  `frontend/nginx.conf` (fallback SPA a `index.html`, cache inmutable para
  `/assets/`, `no-store` para `index.html` y `sw.js`, cache corta para imágenes,
  iconos y manifest).

## 2. Sesión y salón activo (F-03)

`GET /api/v1/me` → `{user, salons:[{id,slug,name,role}]}`.

- Tras el login se llama `fetchMe()`, se guarda `user.id` y se elige el salón
  con `pickSalonId`: el guardado si el usuario sigue perteneciendo a él; si no,
  el primero de la cuenta. Ya no se pega ningún id a mano.
- `SalonProvider` conserva `salons`, `user`, `loading` y `error`; si `/me`
  falla (API vieja o caída) la sesión sigue con el salón guardado y el panel
  explica el estado.
- Con más de un salón aparece un `<select>` en la barra superior (switcher);
  con uno solo se muestra el nombre. `SalonRequired` ya no pide ids.
- `logout` borra token, usuario y salón, vacía la cola offline del usuario y
  purga la caché de API del service worker.

## 3. POS y equipo (F-02)

- `GET /api/v1/salons/:id/staff` alimenta un `<select>` de profesional; el
  campo de texto libre desapareció.
- El error de `/staff` se muestra junto al select (`role="alert"`) con el
  mensaje en español del servidor y no oculta el catálogo ni las ventas.
- El payload de la venta sigue siendo `professional_id`, `client_name`,
  `method`, `items` y `client_op_id`; el backend valida el rol.

## 4. Cola offline (F-04, F-15)

`src/lib/offlineQueue.ts` clasifica el fallo de cada envío (`ApiError.status`):

| respuesta | acción |
| --- | --- |
| `401` | **pausa** la cola sin gastar intentos ni mover la operación |
| `4xx` definitivo (400, 403, 404, 409, 422, …) | mueve la operación a `dropped` **de inmediato** y sigue con la siguiente |
| `408`, `429`, `5xx`, error de red | reintento con backoff 5 s → 5 min, máximo 8 intentos |
| sin red (`navigator.onLine === false`) | espera el evento `online` sin consumir intentos |

- Las operaciones descartadas se muestran en el banner con su `client_op_id` y
  el mensaje del servidor; "Entendido" limpia la lista.
- La cola se persiste por usuario en
  `salon.offline_queue:<user.id>`; al cerrar sesión se vacía la del usuario
  actual y la siguiente cuenta no puede sincronizar trabajo ajeno.
- `request()` aborta a los 10 s (`DEFAULT_REQUEST_TIMEOUT_MS`) y lo reporta como
  `NetworkError`, de modo que una API colgada hace que el POS encole la venta
  en lugar de quedarse esperando.
- El service worker subió a `salon-v2`; `activate` borra caches de versiones
  anteriores, la clave de la API usa dos hashes del header `Authorization`
  (nunca el token), un `401` purga la caché de API y `sw.js` se registra con
  `updateViaCache: 'none'`.

## 5. Tiempo real

- Un solo websocket por sesión (`RealtimeProvider`) alimenta notificaciones,
  panel y calendario.
- `booking.created` y `sale.created` refrescan el dashboard; `booking.created`
  y `booking.updated` refrescan el calendario del día visible.
- El cliente deja de reconectar ante un fallo de autenticación: código de
  cierre `1008`, `4401`, `4403` o un token ausente. El resto sigue con backoff
  exponencial y jitter.

## 6. Landing pública (F-12, F-13, F-08, F-09, F-10)

- `GET /api/v1/public/salons/:slug` (sin auth) provee nombre, duración y precio
  de los servicios; el catálogo estático es el respaldo cuando la request
  falla. La copia de marketing se reutiliza por nombre.
- El mínimo de fecha del formulario usa la fecha local del salón
  (`toDateInputValue`, no `toISOString()`), sin corrimiento UTC.
- Galería re-balanceada: ocho piezas que forman filas completas (2×2 en móvil y
  tablet; 2×3 + 1×2 + 1×2 + 2×1 + 4×1 = 16 celdas en cuatro filas a ≥1100 px).
- Todas las imágenes declaran `width`/`height` intrínsecos; el hero usa
  `fetchpriority="high"`, `loading="eager"` y un `<link rel="preload">` en
  `index.html`, con punto focal distinto en móvil y escritorio.

## 7. Accesibilidad y diseño (F-05, F-06, F-07, F-24)

- `--ink-muted` pasó a `#6b5a5e` sólido: ≥ 5.9:1 sobre crema, superficie y fondo
  del panel. `src/lib/contrast.test.ts` verifica AA y que el dorado sobre crema
  (2.2:1) nunca se use como texto.
- El dorado queda para filetes, íconos y botones con texto `--ink`; las
  estrellas de testimonios usan `--wine`.
- La tabla de servicios se convierte en tarjetas etiquetadas por debajo de
  700 px (sin scroll horizontal); la navegación móvil es una grilla 3×2 con
  todas las secciones visibles.
- `[id]` tiene `scroll-margin-top` para que los anclajes no queden bajo el
  header sticky.

## 8. Iconos PWA (F-11)

Generados desde `public/icons/icon.svg` y `icon-maskable.svg` con Chromium
headless (`--headless --no-sandbox --screenshot`, wrapper HTML que fija el
tamaño del SVG):

| archivo | uso |
| --- | --- |
| `icon-192.png` | manifest `any` 192 |
| `icon-512.png` | manifest `any` 512 |
| `icon-maskable-512.png` | manifest `maskable` 512 |
| `apple-touch-icon.png` (180) | `index.html` para iOS |

`manifest.webmanifest` declara los PNG y conserva el SVG como `any`; el
precache del service worker incluye los cuatro binarios.

## 9. Verificación

- `npx tsc --noEmit`, `npm test` (node --test) y `npm run build` en verde.
- Lógica cubierta con TDD: cola offline (4xx/408/429/401/scope), contraste,
  timeout de fetch, clientes `/me`, `/staff` y `/public/salons/:slug`, y parada
  del websocket ante fallo de autenticación.
- Revisión visual con `vite preview` y capturas headless a 375, 768 y 1280 px
  de la landing; panel a 390 y 1280 px cuando la API compila.
