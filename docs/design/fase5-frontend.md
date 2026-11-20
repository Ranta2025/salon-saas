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

## 10. Ronda 2 — reserva pública, turnos, equipo y ajustes (T7)

Consume el contrato de la ronda 2 (`odd/tasks/fase5-hardening.md`). Sin
dependencias nuevas y con la misma paleta de `src/styles/theme.css`.

### 10.1 Reserva pública real (`src/landing/components/BookingCta.tsx`)

- Se elige **servicio** (catálogo público ya cargado), **fecha** (`min` = hoy en
  el calendario local del visitante, `toDateInputValue(new Date(), visitorTimeZone())`,
  nunca `toISOString()`) y después los **horarios** de
  `GET /public/salons/:slug/availability?date=&service_id=`.
- Los huecos llegan en UTC y se muestran en la hora local del visitante con
  `Intl.DateTimeFormat` (`hourCycle: 'h23'`). La interfaz dice la zona:
  “Horarios del viernes, 20 de noviembre en tu hora local: Ciudad de México
  (America/Mexico_City)”.
- `groupSlotsByTime` agrupa por hora local de inicio (`10:00` + `10:00` de dos
  profesionales = un solo botón). El POST no manda `professional_id`: el salón
  asigna a quien esté libre, así que un hueco con dos especialistas no choca con
  un 409 por haber elegido a la persona equivocada.
- `POST /public/salons/:slug/bookings` con `BookingAttempt`: el `client_op_id`
  se conserva en cada reintento (deduplicación del servidor) y sólo se renueva
  después de una reserva confirmada, para que la siguiente cita sea otra.
- Confirmación clara (fecha, hora, servicio, zona horaria y folio) con enlace
  secundario “Avisar también por WhatsApp”; el enlace de WhatsApp también queda
  en la letra chica del formulario para quien prefiera ese canal.
- Errores: `describeBookingError` distingue **409** (el hueco se acaba de
  ocupar: mensaje, recarga de horarios y datos del formulario intactos),
  **429** (demasiadas solicitudes), **400/422** (mensaje del servidor) y fallo
  de red. Si `/availability` falla, el botón “Reintentar” vuelve a consultar y
  WhatsApp sigue disponible.
- Accesibilidad: `label` en cada campo, `fieldset`/`legend` por paso, botones de
  horario con `aria-pressed` y `aria-label` (“10:00, dos especialistas
  disponibles”), región `role="status"` que anuncia el conteo y el foco se mueve
  al primer horario cuando la lista cambia (y al título de la confirmación al
  reservar).

### 10.2 Acciones sobre turnos (`CalendarPage`, `Timeline`, `BookingDetail`)

- Cada cita del calendario (bloque del timeline y fila de la agenda móvil) es un
  botón operable por teclado que abre el panel de acciones; el bloque muestra
  hora, cliente y su **badge de estado** (`Agendada`, `Atendida`, `Cancelada`,
  `No asistió`).
- El panel permite **cancelar** (con confirmación en línea), **marcar que no
  asistió**, **marcar como atendida** y **reprogramar** (fecha, hora y
  profesional desde `/staff`) vía `PATCH /salons/:id/bookings/:bid`
  (`src/lib/bookingActions.ts`). El cambio conserva la duración del turno
  (`reschedulePayload`) y sólo se ofrecen acciones válidas para el estado.
- El calendario pide `/salons/:id/settings` para dibujar con la zona del salón
  (respaldo `TIME_ZONE`) y usa los nombres del equipo en los encabezados de
  columna.
- `relativeRole`: owner/admin actúan sobre cualquier cita; professional sólo
  sobre las propias (`canManageBooking` con el id de `/me`). En el resto el
  panel muestra “Solo la administración del salón o la profesional asignada…”.
- `booking.updated` refresca el día visible (además de `booking.created`).

### 10.3 Equipo (`/panel/equipo`) y ajustes (`/panel/ajustes`)

- `TeamPage`: lista `GET /members`, agrega por correo (normalizado a minúsculas)
  y rol con `POST`, quita con confirmación `DELETE` y muestra el mensaje del
  servidor (“todavía no tiene cuenta”, “no se puede quitar al último owner”).
  Sólo la dueña/dueño puede asignar administración (`canAssignRole`).
- `SettingsPage`: zona horaria (lista de IANA de Latinoamérica, España y UTC),
  apertura, cierre y paso de turnos (15/30/60) con `GET/PUT /settings`.
  `validateSettings` reporta el campo exacto (formato `HH:MM`, cierre posterior
  a la apertura, paso permitido, zona válida) y el guardado muestra confirmación.
- Ambas páginas se guardan por rol antes de pedir nada: un professional ve una
  explicación en lugar de un 403. Si el 403 llega igual, `friendlyApiMessage`
  traduce el código a una frase útil (`src/lib/api.ts`).

### 10.4 Fotos firmadas

- `normalizeClientPhoto` usa la `url` tal como llega (la firma se resuelve
  contra `VITE_API_URL`, sin reconstruirla). Si una imagen falla al cargar
  (`onError`), `ClientDetailPage` vuelve a pedir el listado **una vez** para
  obtener firmas nuevas y evita un bucle de recargas.

### 10.5 Navegación y roles

- `AppShell` filtra las entradas `managerOnly` (`Equipo`, `Ajustes`) con el rol
  del salón activo (`activeRole`). Mientras `/me` no responde las entradas
  quedan visibles y la página explica el 403 en vez de romperse.
- La navegación móvil pasa a `repeat(auto-fit, minmax(5.4rem, 1fr))`: 8 entradas
  visibles, sin recortes ni scroll horizontal (verificado a 375 px).

### 10.6 Verificación de la ronda 2

- `npx tsc --noEmit`, `npm test` (140 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib`: `publicBooking.test.ts` (normalización, agrupación por hora
  local, formato, 409/429, reutilización del `client_op_id`), `permissions.test.ts`,
  `settings.test.ts`, `members.test.ts`, `bookingActions.test.ts`, más
  `toDateTimeIso`/`addMinutesToIso` en `datetime.test.ts` y
  `friendlyApiMessage` en `api.test.ts`.
- `vite preview` (proxy `/api` agregado a `preview` para revisar el build contra
  una API local sin recompilar con `VITE_API_URL`) + una API simulada en
  `/tmp/fase5-shot/server.mjs` y capturas Chromium headless a 375 y 1280 px de
  la reserva (formulario, horarios, confirmación, 409), del calendario con
  acciones (owner y professional), de equipo, de ajustes y de la navegación
  móvil con las ocho entradas.
