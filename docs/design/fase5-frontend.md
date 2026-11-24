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

## 11. Ronda 3 — hardening de la auditoría end-to-end (E-02…E-13)

Cierre de los hallazgos de la auditoría en navegador sobre la fase 5. Sin
dependencias nuevas ni cambios de API: todo lo que sigue usa los endpoints ya
documentados.

### 11.1 Roles y `/panel` (E-02)

- `SalonRole` reconoce `reception` (antes se degradaba a `professional`), con
  etiqueta «Recepción». `reception` no es manager: no ve Equipo ni Ajustes, pero
  sí administra turnos y crea fichas, como el backend.
- `panelHomePath(role)` devuelve `/panel/mi-agenda` para `professional` (F2b,
  S-F2b-4) y `null` para el resto. El índice de `/panel` (`PanelHome`) redirige
  antes de montar `DashboardPage`, así que un professional **nunca** pide
  `/dashboard` y nunca ve el 403. Mientras `/me` carga se muestra un estado de
  carga, no el panel.
- `describeApiError` reemplaza a `friendlyApiMessage` para las páginas: un 403
  devuelve el texto amable con `retryable: false`, y `ErrorNotice` no dibuja
  «Reintentar» cuando reintentar no puede ayudar. El resto de errores conserva
  el mensaje del servidor y su botón.

### 11.2 Fichas de clientes a mano (E-03, mitad UI)

- `/panel/clientes` tiene el formulario «Nueva clienta» (nombre, teléfono,
  correo, notas) que llama a `POST /salons/:id/clients`. Solo aparece para
  `owner`, `admin` y `reception` (`canCreateClient`): un professional no ve un
  formulario que respondería 403.
- La lista se mantiene alfabética al insertar la ficha nueva y cada tarjeta
  muestra teléfono y **correo** (o «Sin correo»).
- La copia dejó de mentir: las fichas se crean solas cuando alguien reserva en
  el sitio público **o** a mano con el formulario.

### 11.3 Cola offline: motivo real del descarte (E-05)

Cada operación descartada guarda `cause`:

| `cause` | cuándo | copia en el banner |
| --- | --- | --- |
| `server_rejected` | 4xx definitivo (400/403/404/409/422) | «El servidor rechazó esta operación: &lt;mensaje del servidor&gt;» |
| `attempts_exhausted` | red o 5xx hasta agotar los 8 intentos | «Se agotaron los intentos: &lt;último error&gt;» |

Se acabó el «error desconocido»: el motivo del servidor (por ejemplo que la
profesional ya no pertenece al salón) llega tal cual a la persona. El estado
antiguo sin `cause` se lee como `attempts_exhausted`. En ese momento la cola ya
no tiene nada pendiente, así que el banner titula «Operaciones descartadas» y no
imprime «0 operaciones esperan al servidor».

### 11.4 POS y service worker sin conexión (E-06)

- `POSPanel` muestra el error de `/staff` **solo** cuando no hay equipo en
  memoria (`staff.length === 0`). Con datos guardados el select sigue usable y
  no aparece el mensaje alarmante.
- El service worker (`salon-v3`) ya no responde a un GET de API sin red con un
  fetch rechazado: devuelve JSON `{"error": "...", "offline": true}` con estado
  `503`, y el cliente lo convierte en `NetworkError` (no en `ApiError`), de modo
  que la UI lo trata como conectividad y no imprime errores de consola por una
  situación esperada.
- Un GET de API servido desde la caché sale con la cabecera
  `X-Salon-Offline-Cache: 1`; `request()` avisa por `salon:offline-data` y la
  pantalla sabe que está mostrando datos guardados (ver 11.6).

### 11.5 Calendario: panel de acciones, horas y plural (E-08)

- `Timeline` recibe la ventana del salón: `GET /salons/:id/settings`
  (`open_time`/`close_time`) se convierte con `timelineWindowFromHours` (la
  apertura redondea hacia abajo y el cierre hacia arriba para no recortar citas;
  un valor inusable conserva 09:00–20:00). Las líneas, la regla de horas y la
  posición de cada cita usan esa ventana.
- El panel de la cita ya no cae debajo de la grilla: en ≥900 px es una columna
  fija (`position: sticky`, 22 rem) al lado del calendario, con scroll propio y
  altura máxima de la ventana; por debajo de 900 px es una hoja de ancho
  completo anclada abajo (82 vh como máximo, con scroll propio). El foco se mueve
  al título del panel al abrirlo.
- Con el panel abierto la grilla cede ancho, así que las columnas bajan a 10 rem
  y tres especialistas siguen viéndose enteras; con más equipo la fila mantiene
  su scroll horizontal. Los campos de reprogramación van en una sola columna: en
  22 rem, tres campos en línea recortaban la fecha y el nombre de la profesional.
- `canReschedule` sólo acepta turnos abiertos (`booked` o estado vacío): un turno
  atendido, cancelado o `no_show` no ofrece «Reprogramar» y explica por qué.
- El encabezado de cada columna usa `bookingCountLabel`: «1 cita», «2 citas».

### 11.6 Calendario sin API: datos guardados, nunca vacío (E-10)

`CalendarPage` escucha `salon:offline-data` y marca la lista como guardada
cuando el service worker la sirvió de su caché; además `online === false` marca
lo mismo. En ese estado muestra «Sin conexión: mostrando datos guardados…» y, si
la copia del día no tiene citas, lo dice explícitamente en lugar del «No hay
citas agendadas para este día» (que se lee como dato real). Si la request falla
sin copia, se muestra el error con su botón; el estado vacío sólo aparece con
una respuesta viva y vacía.

### 11.7 Landing: horario (E-09) y precarga del hero (E-11)

- **Limitación conocida**: la API pública no expone el horario del salón.
  `GET /public/salons/:slug` devuelve salón y servicios, y
  `/public/salons/:slug/availability` devuelve **huecos libres** dentro del
  horario, no la ventana: derivar «abre a las 9» del primer hueco sería falso en
  cuanto la mañana esté ocupada (y el endpoint exige `service_id` y fecha). Por
  eso la landing sigue mostrando horas estáticas, pero salen de **una sola
  constante** (`PUBLIC_OPENING_HOURS` en `src/landing/content.ts`), cada bloque
  las etiqueta como «horario indicativo» y la copia invita a confirmar al
  reservar. Cuando el backend publique el horario público, basta cambiar
  `content.ts` y quitar la etiqueta.
- `index.html` ya no precarga `hero-atwork.webp` en todas las rutas. La precarga
  la inyecta `main.tsx` sólo si la URL actual es la landing (`isLandingPath`) y
  `LandingPage` la repite (idempotente) al navegar dentro del SPA; así el panel
  no dispara el warning «preload not used» de Chrome.

### 11.8 Navegación móvil compacta (E-13)

Por debajo de 900 px la barra lateral es una cabecera de tres filas: marca y
acciones (sitio público, cerrar sesión) en la primera, las entradas del panel en
la segunda y la barra del salón en una sola línea. Las entradas van en una fila
que **envuelve** (`flex-wrap: wrap`): con ocho secciones en 390 px quedan dos
líneas de cuatro, todas completas. Cada entrada mide 44 px de alto, conserva el
`outline` de foco visible y ninguna queda recortada ni exige scroll horizontal
(probado a 375 y 390 px: `scrollWidth === clientWidth` en la lista). La nota de
moneda de la barra superior se oculta en móvil para que esa barra ocupe una sola
línea, con lo que el contenido empieza a ~213 px del borde superior en vez de a
~270 px, sin perder ninguna entrada como en la versión con scroll horizontal.

### 11.9 Verificación de la ronda 3

- `npx tsc --noEmit`, `npm test` (156 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib`: `describeApiError` y respuesta offline del worker
  (`api.test.ts`), causa y motivo del descarte (`offlineQueue.test.ts`),
  `timelineWindowFromHours` (`datetime.test.ts`), `canReschedule` cerrado y
  `bookingCountLabel` (`bookingActions.test.ts`), `panelHomePath` (ahora
  `/panel/mi-agenda`),
  `canCreateClient` y `reception` (`permissions.test.ts`, `me.test.ts`),
  `offlineData.test.ts` y `landingPreload.test.ts`.
- Revisión visual con `vite preview` contra una API simulada en `/tmp` y
  capturas Chromium headless a 390 y 1280 px de `/panel`, `/panel/calendario`
  (panel de acciones abierto, también con un turno cerrado), `/panel/clientes`
  (formulario y creación de una ficha), `/panel/pos` (sin conexión, venta en
  cola y operación descartada con el motivo del servidor), `/panel` como
  professional, y la landing con la nota de horario. Estados sin conexión
  revisados con el emulador de red de Chromium: el calendario muestra la copia
  guardada y, sin copia del día, dice que es una copia guardada; no aparece
  «No hay citas» como dato real y la consola queda sin errores.

## 12. Ronda 3 — dólares, teléfono con código de país y correo en la reserva (T11, T12)

Pedido de la ronda 3 (`odd/tasks/fase5-hardening.md`, «L-usuario (ronda 3)»).
Sin dependencias nuevas: todo lo de abajo usa React, CSS con custom properties y
módulos puros de `src/lib`.

### 12.1 Moneda: dólares (T11)

`src/lib/money.ts` pasa a `CURRENCY = 'USD'` y `CURRENCY_LABEL = 'USD'`. `LOCALE`
sigue en `es-MX` porque lo que define es la **forma** `$1,234.55` (coma de miles,
punto decimal) y el formato de fechas, no la moneda.

- Los montos siguen viajando y guardándose como **centavos enteros**:
  `formatCents` no cambió, así que no hay migración de datos ni riesgo de
  `float`. `sumCents`, `percentOf` y `parseAmountToCents` no cambian.
- Toda la copia visible dejó de decir pesos/MXN: barra del panel («Los montos se
  muestran en dólares estadounidenses (USD)»), «Montos en dólares» del panel,
  `Precio (USD)` del formulario de servicio, el catálogo («Precios finales en
  dólares estadounidenses…»), el listado de acceso y la descripción de respaldo
  de un servicio que llega sin copia de marketing. `grep -rniE "peso|mxn|mexican"
  frontend/src` queda vacío.
- `money.test.ts` fija el contrato: `CURRENCY`/`CURRENCY_LABEL` en `USD` y
  `formatCents(123455) === '$1,234.55'`.
- Moneda por salón queda fuera de alcance (una sola moneda para el producto).

### 12.2 Teléfono de contacto de la landing (T11)

`CONTACT_PHONE` en `src/landing/content.ts` es la única fuente:

| campo | valor | uso |
| --- | --- | --- |
| `e164` | `+5355528595` | valor canónico del número |
| `display` | `+53 5552 8595` | lo que se imprime |
| `tel` | `tel:+5355528595` | enlace del header y del pie |
| `whatsapp` | `5355528595` | dígitos que espera `wa.me` |

`SALON.phone`, `SALON.phoneHref` y `SALON.whatsapp` derivan de esa constante, de
modo que el header (`≥700 px`), el pie, el bloque de contacto del CTA de reserva
y el mensaje de WhatsApp imprimen siempre el mismo número.

### 12.3 Campo de teléfono con código de país (T11)

`src/lib/phone.ts` (puro, con tests) + `src/components/PhoneInput.tsx`
(reutilizable). Se usa en el formulario público de reserva y en el formulario de
ficha del panel; `displayPhone` alimenta las lecturas (tarjeta de la lista y
ficha del detalle).

**Catálogo** (`COUNTRIES`, Cuba primero porque es el país del salón):

| país | código | dígitos | ejemplo |
| --- | --- | --- | --- |
| Cuba | +53 | 8 | `5xxx xxxx` |
| México | +52 | 10 | `55 xxxx xxxx` |
| Estados Unidos/Canadá | +1 | 10 | `555 123 4567` |
| España | +34 | 9 | `612 345 678` |
| Argentina | +54 | 10 | `11 xxxx xxxx` |
| Colombia | +57 | 10 | `300 123 4567` |
| Venezuela | +58 | 10 | `412 xxx xxxx` |
| Chile | +56 | 9 | `9 xxxx xxxx` |
| Perú | +51 | 9 | `912 345 678` |
| Uruguay | +598 | 8 | `94 123 456` |
| Panamá | +507 | 7–8 | `6xxx xxxx` |
| República Dominicana | +1 | 10 | `809 123 4567` |

**Comportamiento**

- El selector es un `<select>` nativo con `aria-label="Código de país"` y el
  texto «Cuba +53»/«EE. UU./Canadá +1» (etiqueta corta: la más larga tiene que
  entrar cerrada a 390 px). Es operable con teclado y conserva el anillo de foco
  de `base.css`.
- El número nacional es `type="tel"`, `inputMode="tel"` y
  `autoComplete="tel-national"`; se guardan **solo dígitos** y se muestran
  agrupados con espacios según el país (`5552 8595`, `55 1234 5678`). El
  placeholder y la ayuda viva salen del país elegido («Ejemplo: 5xxx xxxx · 8
  dígitos»).
- Los dígitos **nunca se recortan en silencio**: si sobran, se ven y la
  validación lo dice («El teléfono de Cuba tiene 8 dígitos (escribiste 10)»), en
  lugar de convertirse en otro número válido.
- Al pegar un número internacional (`+53 5552 8595`, `0053…`, `53…` cuando no
  cabe en el país del selector) se detecta el prefijo —el código más largo gana,
  así `+598` no se confunde con `+5…`—, el selector salta a ese país y los dígitos
  se reparten. Un fragmento corto se inserta donde está el cursor.
- `validatePhone` es la única fuente de mensajes (mínimo/máximo por país, campo
  vacío) y el formulario la usa antes de enviar; el error se anuncia con
  `role="alert"` y `aria-describedby`.
- **Contrato**: al API se le manda `+<código><dígitos>` (por ejemplo
  `+5355528595`) como **string**; el backend normaliza a dígitos
  (`phone_norm`), así que su contrato no cambia. La reserva pública conserva
  `client_phone` como string, de modo que la cola offline no cambia de forma.
- Un valor existente se lee al revés: si empieza con `+` se reparte en código +
  número. Una ficha vieja con teléfono libre (`55 1234 5678`) se muestra **tal
  como se guardó** cuando sus dígitos no caben en el país por defecto: inventarle
  un prefijo mentiría sobre el número. En el formulario, un valor sin prefijo
  abre con Cuba.

### 12.4 Correo opcional en la reserva pública (T12, mitad frontend)

- Paso «Tus datos»: campo **opcional** «Correo electrónico (te enviamos la
  confirmación y un recordatorio)», `type="email"`, `autoComplete="email"`,
  `inputMode="email"`, con la ayuda «Opcional. Sin correo la cita se agenda
  igual, pero no te enviamos recordatorio.».
- `src/lib/email.ts` replica la regla del servidor
  (`domain.NormalizeAndValidateEmail`): recorta, pasa a minúsculas, exige `@` y
  entre 5 y 254 caracteres; vacío es válido. El mensaje es exactamente
  `Email inválido`, el mismo del API.
- El cuerpo del POST solo lleva `client_email` cuando hay valor (normalizado a
  minúsculas); sin correo la reserva es anónima y el JSON es el de siempre.
- Si el servidor responde `400 {"error":"Email inválido"}`, el mensaje se pinta
  **junto al campo** (no en el aviso general): `describeBookingError` clasifica
  el 400 como validación y la pantalla mueve el texto al campo del correo.
- Confirmación: con correo dice «Te enviamos la confirmación a <correo>» (nunca
  afirma una entrega: el envío es best-effort en el servidor, así que el texto
  queda en «te la enviamos» y avisa de revisar el correo no deseado). Sin correo
  muestra la invitación amable a agregarlo para recibir recordatorios.
- El formulario de ficha del panel (ya construido en T10) usa la **misma**
  validación y el mismo mensaje antes de enviar, y su ayuda explica para qué lo
  pide la reserva pública; no hay lógica duplicada.

### 12.5 Estilos

`base.css` suma `.phone-field__row` (fila flexible selector + número),
`.phone-field__country` (máximo `10.5rem`, tipografía algo menor para que
«EE. UU./Canadá +1» entre cerrada a 390 px) y `.phone-field__number`
(`flex: 1 1 6rem`, `min-width: 0`, cifras tabulares). Por debajo de 360 px el
selector pasa a ocupar su propia fila. `.booking__fields` usa `align-items:
start` para que una etiqueta larga (el correo) no estire el campo de al lado.

### 12.6 Verificación de la ronda 3

- `npx tsc --noEmit`, `npm test` (172 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib`: `phone.test.ts` (catálogo, dígitos, agrupación, `toE164`,
  detección de prefijo al pegar, mensajes de longitud, lectura de valores
  viejos), `email.test.ts` (normalización, regla del servidor, mensaje exacto),
  `money.test.ts` (USD y forma `$1,234.55`) y `publicBooking.test.ts`
  (`client_email` solo cuando hay valor).
- Revisión visual con `vite preview` contra una API simulada en `/tmp` y
  capturas Chromium headless a 390 y 1280 px de: la landing (header, bloque de
  contacto del CTA y pie con el teléfono nuevo), el formulario de reserva
  (selector de país + correo), el error local del teléfono, el `Email inválido`
  del cliente y el que devuelve el servidor junto al campo, la confirmación con
  correo y sin correo, el panel con montos en dólares y el formulario de ficha
  con el teléfono y el correo. Se corrigieron en la revisión: el campo de nombre
  que se estiraba a la altura del bloque de correo (1280), el `+1` recortado del
  selector «EE. UU./Canadá» a 390 px y el recorte silencioso de dígitos de más.

## 13. Fase 6 — F2a: quién atiende clientes (equipo)

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2a» (S-F2a-5 mitad
frontend y S-F2a-6). Sin dependencias nuevas y con los tokens de
`src/styles/theme.css`.

### 13.1 Tipo y cliente de API (`src/lib/members.ts`)

- `SalonMember` suma `attends: boolean` (`memberships.attends`).
  `normalizeMembers` lo lee con `attends(value, role)`: un booleano se respeta y
  cualquier otra cosa (campo ausente en una API vieja, texto) cae en la regla del
  dominio — `professional` atiende siempre, el resto no.
- `setMemberAttends(salonId, userId, attends)` hace
  `PATCH /api/v1/salons/:id/members/:uid` con el cuerpo `{"attends": bool}` y
  devuelve el miembro guardado (se normaliza igual que el listado). Si el
  servidor responde `400 {"error":"Las profesionales siempre atienden"}` o
  `400 {"error":"Ese rol no atiende clientes"}`, `ApiError.message` conserva la
  frase del servidor y la pantalla la imprime tal cual (sin el prefijo interno
  «validación:», que el API ya limpia).
- `POST /members` sigue mandando solo `email` y `role`: el `attends` por defecto
  lo decide el servidor (S-F2a-4), así que la UI no lo duplica.

### 13.2 Reglas puras (`src/lib/attends.ts`)

| helper | regla |
| --- | --- |
| `memberAttends(member)` | `professional` siempre atiende, aunque el flag guardado sea `false`; `owner`/`admin` solo con `attends = true`; `reception` (y clientes) nunca |
| `attendsControl(actorRole, targetRole)` | `'switch'` en `owner`/`admin` (y solo si el actor es dueño/administración), `'fixed'` en `professional`, `'none'` en `reception` |
| `nobodyAttends(members)` | `true` solo con la lista cargada, no vacía y sin nadie que atienda |
| `applyAttends(members, userId, attends)` | devuelve una lista nueva con el flag de una persona; llamarla otra vez con el valor anterior es la vuelta atrás exacta |
| `ATTENDS_NOTICE` | texto único del aviso: `Nadie atiende todavía: activá 'Atiende clientes' en alguien para recibir turnos` |

Que `memberAttends` ignore el flag de un `professional` es deliberado: un
payload viejo no puede sacar a una profesional de la agenda.

### 13.3 `/panel/equipo` (S-F2a-6)

- La tabla «Quiénes tienen acceso» gana la columna **Atiende** (solo se dibuja
  si alguien puede atender o ser marcado). En dueño y administración es un
  interruptor «Atiende clientes»; en profesionales, la etiqueta fija «Atiende»;
  en recepción, nada (la fila del listado móvil desaparece con
  `td:empty`).
- El interruptor es un `input type="checkbox"` con `role="switch"`
  (operable con `Space`), `aria-checked`, etiqueta visual «Atiende clientes» y
  `aria-label` con el nombre de la persona («Atiende clientes: Ana») para que la
  tabla no repita un control anónimo. El `input` cubre toda la etiqueta, así que
  el objetivo real es 153 × 44 px a 390 px, y el foco se dibuja sobre el riel
  con `--focus-width`/`--focus-light`.
- Cambio **optimista**: la fila se invierte antes de la respuesta
  (`applyAttends`), se bloquea un segundo cambio mientras el `PATCH` está en
  vuelo (`aria-busy`, sin deshabilitar el control para no perder el foco) y, si
  el servidor rechaza, la fila vuelve al valor anterior y el mensaje del
  servidor aparece en un `role="alert"` dentro de la tarjeta del equipo. Éxito:
  aviso `role="status"` («Ana atiende clientes desde ahora.» / «Ana ya no atiende
  clientes.»).
- Aviso de salón sin nadie que atienda, entre el encabezado y la tabla, con
  `role="status"` y el texto de `ATTENDS_NOTICE`.
- Nada más cambia: alta por correo, baja con confirmación, etiquetas de rol,
  `canAssignRole` y los mensajes de error siguen igual.

### 13.4 Estilos

`app.css` suma el bloque «who attends clients»: `.attends` (fila con
`min-height: 44px`), `.attends__input` (checkbox transparente que cubre la
etiqueta), `.attends__track`/`.attends__thumb` (riel de 3 × 1.75 rem con pulgar
de 1.25 rem), el verde `#2b6847` del estado encendido y
`input:focus-visible + .attends__track` para el anillo de foco. En el listado
móvil (`≤ 699 px`) la celda «Atiende» permite envolver y las celdas vacías de
recepción no se dibujan. A 390 y 1280 px no hay scroll horizontal
(`scrollWidth === clientWidth`).

### 13.5 Verificación

- `npx tsc --noEmit`, `npm test` (184 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib`: `attends.test.ts` (quién atiende por rol, switch/etiqueta/
  nada por rol del actor y del objetivo, regla del aviso, texto exacto, cambio
  optimista con vuelta atrás e inmutabilidad) y `members.test.ts` (`attends` en
  el listado, respaldo por rol cuando falta, `PATCH` con `{"attends": bool}` y
  la frase del servidor tal cual).
- Revisión visual con el bundle de `vite build` servido junto a una API
  simulada en `/tmp/f2a-shot` y Chromium headless a 390 y 1280 px: interruptor
  encendido y apagado, etiqueta fija de profesional, recepción sin nada, aviso
  de «nadie atiende» y error con vuelta atrás (el `400` del servidor se lee
  entero). Con el protocolo de DevTools se comprobó además, a 390 y 1280 px,
  `role="switch"` con `aria-checked` y nombre accesible, objetivo de 44 px,
  `Tab` que llega al control, `:focus-visible` con `outline: solid 3px`, `Space`
  que conmuta y el `PATCH` que sale con el flag nuevo.

## 14. Fase 6 — F2b: Mi agenda

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2b» (S-F2b-1..6). Solo
frontend: no hay endpoint nuevo y no se agregaron dependencias. Todo lo que se
muestra sale de `GET /salons/:id/bookings?from&to` (el mismo del calendario y su
caché offline), `GET /salons/:id/staff` (la lista que F2a ya filtra a quien
atiende), `/me` y `/settings`.

### 14.1 Reglas puras (`src/lib/myAgenda.ts`)

| helper | regla |
| --- | --- |
| `selectMyBookings(bookings, userId)` | filtra `professional_id === user.id`; un id vacío no selecciona nada |
| `sortAgendaBookings` | orden por inicio, desempate por fin y luego id; no muta la entrada |
| `agendaMarkers(bookings, now)` | marca `now` en el turno en curso y `next` en el primero abierto que empieza después; `now = null` (día que no es hoy) no marca nada |
| `isOpenBooking` | solo `booked` o estado vacío pueden ser «Ahora»/«Siguiente»; atendida, cancelada y no asistió son historia |
| `weekStartOf`/`weekDates` | lunes a domingo ISO del calendario del salón |
| `weekOverview` | cantidad por día con la fecha local del salón: un turno a las 23:30 cuenta en su día local aunque en UTC sea el día siguiente; canceladas no cuentan |
| `todayAgendaCount`/`todayCountLabel` | «Sin turnos hoy», «1 turno hoy», «3 turnos hoy» |
| `weekRange` | `from` lunes 00:00 local a `to` lunes siguiente 00:00 local, en la zona del salón |
| `isBookableInStaff` | reservable = aparece en `/staff` (autoridad de F2a) |
| `canOpenMyAgenda` | usa `/staff`; solo si esa request falló cae a la regla del dominio (`professional` siempre atiende); dueño/administración sin `attends` no |
| `initialCalendarView`/`visibleCalendarBookings` | quien atiende abre el calendario en «Mi agenda»; el resto sigue en «Todo el salón» |

`formatAgendaDate` imprime «Viernes 21 de noviembre de 2026» y
`formatAgendaDateLower` la misma fecha en minúscula para la frase «Hoy es …».

### 14.2 `/panel/mi-agenda` (S-F2b-1..3)

- Encabezado «Mi agenda» con la fecha de hoy en la zona del salón (de
  `/settings`, respaldo `TIME_ZONE`), resumen «N turnos hoy» y «Próximo turno:
  HH:MM · cliente».
- Tira de la semana (lunes a domingo) con la cantidad de mis turnos por día; el
  día de hoy queda marcado aunque no sea el seleccionado (`aria-current="date"`),
  el seleccionado se pinta lleno (`aria-pressed`) y cada día es un botón de
  44 px con nombre accesible («Lunes 16: 3 turnos»). Elegir un día cambia la
  lista sin volver a pedir la semana.
- Lista del día en orden, con hora, clienta, estado y las marcas «Ahora» /
  «Siguiente». Cada fila abre el **mismo** `BookingDetail` del calendario:
  cancelar (con confirmación), no asistió y atendida con `canManageBooking`, y
  «Reprogramar» solo si el rol puede actuar y el turno está `booked`.
- Día vacío honesto: «No tenés turnos hoy» / «No tenés turnos este día» solo con
  una respuesta viva. Con la API caída se muestra el error y **no** el resumen,
  la tira ni el vacío (misma regla E-10). Con una copia del service worker
  (`X-Salon-Offline-Cache`) o sin red: «Sin conexión: mostrando datos guardados»
  y, si la copia no trae turnos, lo dice explícitamente.
- Quien no atiende y entra directo ve una explicación con enlace al calendario,
  nunca un 403 ni un vacío falso.

### 14.3 Entrada, menú y calendario (S-F2b-4..6)

- `panelHomePath('professional')` pasa de `/panel/calendario` a
  `/panel/mi-agenda`: es el destino del índice de `/panel` después del login.
- `AppShell` pide `/staff` por salón activo y muestra «Mi agenda» solo si la
  persona está en esa lista (recalculado en cada cambio de salón). Dueño y
  administración que atienden ven además una tarjeta «Mi agenda» en el resumen.
- `CalendarPage` suma el alternador «Mi agenda / Todo el salón» para quien
  atiende: abre en «Mi agenda», dibuja solo su columna y la resalta en ambas
  vistas (`highlightProfessionalId`). Recepción y administración que no atienden
  no ven el alternador y siguen con todo el salón.
- La navegación móvil pasa a cinco columnas para que las nueve entradas del
  dueño que atiende sigan entrando en dos filas a 375 y 390 px.

### 14.4 Estilos

`app.css` suma `.my-agenda__summary`, `.week-strip` (siete botones de 44 px con
estados `data-today`/`data-selected`), `.my-agenda__row`/`.my-agenda__marker`
(«Ahora» en vino, «Siguiente» en ciruela), `.my-agenda-layout` (dos columnas
desde 900 px, panel de acciones fijo como en el calendario), `.view-toggle` y
`.timeline__column--mine`, más `.dashboard-agenda`. La barra lateral móvil fija
`align-content: start` para que una página corta no centre el menú.

### 14.5 Verificación

- `npx tsc --noEmit`, `npm test` (199 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib`: `myAgenda.test.ts` (selección, orden sin mutar, ahora/
  siguiente con turnos cerrados y sin reloj, semana lunes–domingo, conteo con un
  turno de 23:30 que en UTC cae al día siguiente, etiquetas exactas, rango
  semanal, reservable desde `/staff`, regla de entrada y filtrado del
  alternador) y `panelHomePath` actualizado en `permissions.test.ts`.
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2b-shot` y Chromium headless (protocolo DevTools; el flag
  `--screenshot` de esta versión se cuelga en el entorno) a 390 y 1280 px:
  `/panel/mi-agenda` con turnos, «Ahora»/«Siguiente», tira semanal, día vacío,
  día elegido con turnos, copia guardada, copia guardada sin turnos, error,
  panel de acciones de un turno abierto y de uno cerrado, calendario en «Mi
  agenda» y «Todo el salón», calendario de recepción sin alternador, `/panel`
  del dueño con la tarjeta y las nueve entradas (375 y 390 px: `scrollWidth ===
  clientWidth`, dos filas, 44 px) y recepción sin «Mi agenda». Se corrigieron en
  la revisión: la fecha capitalizada a mitad de frase, el conteo del encabezado
  del día que incluía canceladas, el resumen y la tira visibles durante un error
  (se leían como dato real), el alternador estirado a todo el ancho y el menú
  centrado en páginas cortas.

## 15. Fase 6 — F2c: oficio y servicios por profesional

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2c» (mitad frontend de
S-F2c-3 y S-F2c-6, más S-F2c-7). Sin dependencias nuevas. El backend que
implementa el contrato en paralelo no se tocó: la UI consume
`PATCH /salons/:id/members/:uid {trade?, attends?}`,
`PUT /salons/:id/members/:uid/services {service_ids}`,
`GET /members` con `trade` y `service_ids`, y `POST /services` con
`professional_ids` opcional.

### 15.1 Tipo y cliente de API

`SalonMember` (`src/lib/members.ts`) suma:

| campo | lectura |
| --- | --- |
| `trade: string` | `normalizeTrade`: recorta, quita saltos de línea y corta en 40; el oficio es un texto visible, no un permiso |
| `service_ids: string[]` | `normalizeServiceIds`: únicos, recortados, sin vacíos ni no-strings (una lista vacía es válida) |

- `setMemberTrade(salonId, userId, trade)` hace `PATCH` con `{"trade": …}` ya
  normalizado y devuelve el miembro guardado. Un `400 {"error":"Ese rol no tiene
  oficio"}` llega tal cual a la pantalla (sin prefijo interno de validación).
- `setMemberServices(salonId, userId, serviceIds)` hace `PUT` a
  `/members/:uid/services` con `{"service_ids": […]}` y devuelve lo que el
  servidor guardó. `400 {"error":"Servicio inválido"}` se muestra verbatim.
- `createService` (`src/lib/api.ts`) manda `professional_ids` normalizado y
  **omite la clave cuando la lista queda vacía** (S-F2c-5: sin lista nadie queda
  asignado, y una API anterior ve el mismo cuerpo de siempre). El tipo
  `ServiceInput` documenta el campo opcional.

### 15.2 Reglas puras (`src/lib/tradeServices.ts`)

| helper | regla |
| --- | --- |
| `normalizeTrade` | `trim`, saltos de línea/tabs a espacio, espacios repetidos a uno, máximo 40 (`TRADE_MAX_LENGTH`) |
| `normalizeServiceIds` | lista de ids únicos y recortados; cualquier otra cosa se descarta |
| `canEditTrade` / `canEditServices` | solo dueño/administración y solo sobre quien atiende clientes (F2a: profesional, o dueño/admin con `attends`) |
| `bookableMembers` | quien atiende, según `memberAttends` (un flag viejo nunca saca a un profesional) |
| `professionalsForService` / `professionalCount` | reservables de `GET /members` cuyo `service_ids` incluye el servicio; un id vacío cuenta 0 |
| `nobodyDoesService` | `true` con conteo 0: no se ofrecen turnos para ese servicio |
| `professionalsCountLabel` | «Ningún profesional lo hace» / «1 profesional lo hace» / «3 profesionales lo hacen» |
| `applyMemberServices` | actualización optimista: lista nueva con los servicios de una persona; repetirla con los ids anteriores es la vuelta atrás exacta y no toca a nadie más |

Textos exactos: `Nadie hace este servicio: no se ofrecen turnos` y
`Todavía no hace ningún servicio: no recibirá turnos`.

### 15.3 `/panel/equipo` (S-F2c-6)

- Debajo de «Quiénes tienen acceso» hay una tarjeta por persona que atiende
  (`bookableMembers`), con su nombre y el `roleLabel` del rol.
- **Oficio**: `input` con etiqueta «Oficio», `maxLength` 40, ayuda «Se guarda al
  salir del campo o con Enter. Hasta 40 caracteres.» y `placeholder`
  «Barbero, estilista, colorista…». Guarda en `onBlur` o con `Enter` (Escape
  descarta el borrador); si el valor normalizado no cambió no hay request. Éxito:
  `role="status"` «Oficio guardado.»; error: el mensaje del servidor en
  `role="alert"` junto al campo, sin prefijo.
- **Servicios que hace**: `fieldset` con `legend` y una casilla con etiqueta por
  servicio **activo** (se piden con `fetchServices(salonId, false)`). Se marca un
  borrador y se guarda con el botón «Guardar servicios» (deshabilitado si no hay
  cambios): la lista se aplica antes de la respuesta (`applyMemberServices`) y,
  si el servidor rechaza, vuelve a los ids anteriores y el mensaje se lee en un
  `role="alert"` dentro de la tarjeta. Éxito: «Servicios guardados.». Al lado del
  botón se lee «N de M marcados».
- Quien atiende y no hace ningún servicio ve el aviso
  «Todavía no hace ningún servicio: no recibirá turnos».
- Sin servicios activos el control explica que hay que crear uno en Servicios.
- Nada más cambia: alta por correo, baja con confirmación, interruptor
  «Atiende clientes» (F2a), etiquetas de rol y mensajes de error.

### 15.4 `/panel/servicios` (S-F2c-7)

- La tabla gana la columna «Profesionales» con el conteo leído de `GET /members`
  (solo dueño/administración). Cuando el conteo es 0 aparece en la misma celda
  «Ningún profesional lo hace» y el aviso «Nadie hace este servicio: no se
  ofrecen turnos» (`role="status"`), también para un servicio inactivo: la regla
  no distingue estado.
- Si `/members` falla (por ejemplo, un profesional que no puede leerlo) **no se
  muestra ningún conteo ni aviso**: un cero inventado sería mentir sobre el
  salón. La lista de servicios sigue funcionando igual.
- El formulario de alta suma el `fieldset` «Quiénes lo hacen» con las personas
  que atienden y su oficio al lado; al crear, los ids marcados viajan como
  `professional_ids`. En edición no se muestra (el contrato asigna servicios
  desde Equipo). El texto avisa que sin nadie marcado el servicio queda sin
  profesionales y no ofrecerá turnos.

### 15.5 Accesibilidad y estilos

`app.css` suma el bloque F2c: `.work-grid`/`.work-card` (una tarjeta por
persona), `.check-group`/`.check-group__list`/`.check-group__option` (grupo de
casillas real, `legend`, etiqueta por opción, `min-height: 44px`, foco visible de
`base.css`, casilla de 1.125 rem con `accent-color: var(--wine)`) y
`.service-count`/`.service-count__alert` para el conteo y el aviso. A ≥760 px el
oficio queda en una columna angosta y los servicios al lado; por debajo todo va
en una columna. Con 12 servicios no hay scroll interno ni desborde horizontal a
390 px: la lista crece y la página scrollea. En la tabla apilada (≤699 px) la
celda «Profesionales» se dibuja como fila etiquetada y el aviso envuelve debajo
del conteo.

### 15.6 Verificación

- `npx tsc --noEmit`, `npm test` (213 tests, node --test) y `npm run build` en
  verde.
- TDD en `src/lib` (primero en rojo): `tradeServices.test.ts` (normalización del
  oficio e ids, quién edita, conteo por servicio ignorando ids obsoletos de quien
  no atiende, etiquetas y textos exactos, optimista y vuelta atrás) y
  `members.test.ts` (`trade`/`service_ids` en el listado, omisión con payload
  viejo, `PATCH` de oficio y `PUT` de servicios con la frase del servidor tal
  cual, lista vacía); `services.test.ts` (`professional_ids` normalizado y
  omitido si está vacío).
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2c-shot` (13 servicios, 5 personas, una profesional sin servicios,
  un servicio que nadie hace y un `PUT` que responde
  `400 {"error":"Servicio inválido"}`) y Chromium headless por protocolo DevTools
  (el flag `--screenshot` se cuelga en este entorno) a 390 y 1280 px:
  `/panel/equipo` (oficios y listas, aviso de cero servicios, oficio guardado al
  salir del campo y error con vuelta atrás) y `/panel/servicios` (conteos,
  aviso de nadie y formulario de alta con las casillas). Comprobado en el
  navegador: `scrollWidth === clientWidth` en ambas páginas y anchos, cero scroll
  interno en las listas de 12 servicios, `fieldset`+`legend` en los tres grupos,
  36 opciones con 44 px de alto, `Tab` desde el campo Oficio llega a la primera
  casilla con `outline` sólido de 3 px y `Space` la conmuta. Se corrigieron en la
  revisión: la lista de 12 servicios con scroll interno a 390 px (ahora crece) y
  la etiqueta del rol en la tarjeta (decía «Atiende» donde debía decir el rol).
- Fuera de alcance de esta entrega: consumir `trade` de `/staff` y
  `booking.service_id` (no los usa ninguna de las dos pantallas).

## 16. Fase 6 — F2d1: horario semanal del salón y de cada profesional

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2d1» (S-F2d1-4 forma del
contrato y S-F2d1-6). Sin dependencias nuevas. El backend se implementa en
paralelo y no se tocó: la UI consume `GET/PUT /salons/:id/hours`,
`GET/PUT /salons/:id/members/:uid/hours` y el campo `hours` de
`GET /public/salons/:slug`. Semana ISO 1..7 (lunes primero) leída en la zona del
salón, horas `HH:MM` de 24 h.

### 16.1 Reglas puras (`src/lib/weeklyHours.ts`)

| helper | regla |
| --- | --- |
| `parseClock` / `formatClock` / `formatClockShort` / `isClock` | `HH:MM` estricto ↔ minutos desde medianoche; `09:00` para la API y los `input type="time"`, `9:00` para los resúmenes |
| `normalizeSalonHours` / `normalizeMemberHours` | lectura del contrato: filas con día 1..7, hora válida e inicio < fin; el salón respeta el flag `custom`, la profesional lo deriva de las filas (lista vacía = «igual que el salón») |
| `weekDraft` / `draftToDays` / `setDraftDay` | editor de 7 filas: el día cerrado conserva la ventana única como borrador y `PUT` solo manda los días abiertos |
| `copyDraftDay` / `copyTargets` | copiar un día a todos, a lunes–viernes, al fin de semana o a un día concreto |
| `validateDraft` / `validateSegments` | mensajes del API verbatim: «Horario inválido: el inicio debe ser anterior al fin», «Los tramos de un mismo día no pueden solaparse», «Máximo 4 tramos por día», «Día inválido»; formato inválido se explica antes de enviar |
| `weekSummary` | «Lun a vie 9:00–19:00 · Sáb 9:00–14:00 · Dom cerrado»: agrupa días consecutivos iguales y dice «cerrado» donde no hay fila |
| `segmentsSummary` / `daySummary` | la misma agrupación para los tramos de una profesional, con «libre» en el día sin tramos |
| `segmentsForDay` / `setDaySegments` / `addSegment` / `removeSegment` | edición de un día a la vez; el quinto tramo no se agrega |
| `copySegmentsDay` | copia los tramos de un día a los días elegidos (un día origen vacío los deja libres) |
| `dayBreaks` / `breakLabel` | los huecos entre tramos son los descansos: «Descanso 13:00–14:00» |
| `followsSalon` / `salonSegments` / `sameSegments` | «igual que el salón» = sin filas; los tramos del salón son el punto de partida del horario propio |
| `todayWeekday` | día ISO de un instante **en la zona del salón**: 2026-03-10T02:30Z es lunes en Ciudad de México y martes en UTC; una zona inválida cae en UTC en vez de romper |
| `publicHoursRows` | siete filas de la landing con «Cerrado» donde no hay fila y la marca de hoy |
| `beginSave` / `commitSave` / `rollbackSave` | guardado optimista genérico: se muestra el valor nuevo y `rollbackSave` devuelve exactamente el guardado |

### 16.2 Cliente de API

- `fetchSalonHours` / `saveSalonHours` (`src/lib/settings.ts`) contra
  `/salons/:id/hours`; la lista vacía de `saveSalonHours` es «volver al horario
  único». Un `400` conserva la frase del servidor, que se imprime tal cual.
- `fetchMemberHours` / `saveMemberHours` (`src/lib/members.ts`) contra
  `/members/:uid/hours`; `segments: []` significa «igual que el salón».
- `normalizePublicSalon` (`src/lib/publicSalon.ts`) suma `hours: SalonDay[] | null`:
  `null` cuando el campo falta o no trae ninguna fila usable.

### 16.3 `/panel/ajustes` (S-F2d1-6)

- Debajo del formulario de zona/apertura/cierre/paso aparece la tarjeta
  «Horario semanal» (`SalonHoursCard`) con siete filas: interruptor
  Abierto/Cerrado (checkbox con `role="switch"`, `aria-label` «Abierto: Lunes»,
  operable con `Space` y 44 px), hora de apertura y de cierre (`type="time"`,
  deshabilitadas cuando el día está cerrado) y «Copiar este día a…».
- «Guardar horario» valida antes de enviar y muestra el campo con error en
  `role="alert"`; si el servidor rechaza, su frase se lee verbatim dentro de la
  tarjeta con «Reintentar».
- «Volver al horario único» solo existe con días propios y pide confirmación en
  línea («Sí, volver» / «Cancelar»); al guardar, el servidor devuelve la ventana
  única expandida a los siete días (`custom=false`) y la tarjeta vuelve al aviso
  informativo.
- El editor existente (zona horaria, apertura, cierre, paso) no cambió.

### 16.4 `/panel/equipo` (S-F2d1-6)

- Nueva tarjeta «Horario de quien atiende» con un bloque por persona reservable
  (`bookableMembers`, la regla de F2a). Cada bloque
  (`MemberHoursCard`) tiene:
  - `fieldset` «Horario de trabajo» con dos radios: «Igual que el salón» (por
    defecto, muestran el resumen del salón) y «Horario propio» (arranca con los
    tramos del salón como copia).
  - Con horario propio, siete días con hasta cuatro tramos cada uno: «Desde» y
    «Hasta» (`type="time"` con etiqueta propia), «Quitar» por tramo, «Agregar
    tramo» (deshabilitado en el cuarto) y «Copiar este día a…».
  - Los huecos se dibujan como «Descanso 13:00–14:00» entre los tramos, y el día
    sin tramos dice «Día libre».
  - «Guardar horario» envía el reemplazo atómico: el estado se aplica antes de
    la respuesta (`beginSave`), el éxito confirma con `role="status"` y un
    rechazo del servidor vuelve exactamente al horario guardado
    (`rollbackSave`) y muestra la frase del servidor en `role="alert"`
    («Máximo 4 tramos por día», «Los tramos de un mismo día no pueden
    solaparse»…). «Descartar cambios» también vuelve al guardado.
- Si `/hours` del salón falla, la sección avisa y los bloques no inventan el
  resumen del salón.

### 16.5 Landing (S-F2d1-6)

- `LandingPage` guarda `hours` de `GET /public/salons/:slug` y lo pasa al pie y al
  bloque de contacto del CTA. Se eliminaron `PUBLIC_OPENING_HOURS`,
  `PUBLIC_HOURS_NOTE` y `SALON.hours`: desaparece el texto «horario indicativo»
  y con él la limitación conocida de §11.7.
- El pie imprime las siete filas con la hora real, «Cerrado» donde el salón no
  abre y una marca «Hoy» en el día actual. El bloque de contacto del CTA muestra
  el resumen de la semana (`weekSummary`).
- Si la respuesta no trae `hours` (API vieja o campo ausente), ambos lugares
  dicen «Consultá el horario por WhatsApp» con el enlace al número: la landing
  nunca inventa horas.
- El formulario de reserva no cambió: sigue eligiendo servicio, fecha y hueco
  contra `/availability`.
- El payload público no incluye la zona del salón, así que el día de hoy se lee
  en la zona del visitante (la misma que ya usa el formulario para los huecos).
  Es una desviación consciente: las horas impresas son siempre las del salón.

### 16.6 Accesibilidad y estilos

`app.css` suma «F2d1: weekly hours»: `.hours-row` (fieldset con `legend`),
`.hours-row__grid` (una columna en el teléfono, tres desde 760 px),
`.hours-row__times`/`.hours-segment` con `repeat(auto-fit, minmax(8.5rem, 1fr))`
para que un `input type="time"` nunca recorte «09:00 a.m.» a 390 px,
`.attends` reutilizado para el interruptor, `.hours-mode` (radios de 44 px),
`.hours-break` (descanso con filete dorado), `.hours-day__actions` y
`.hours-card__actions`. «Quitar» y «Agregar tramo» suben a 44 px dentro de los
editores. En la landing, `landing.css` marca `[data-today]`, `[data-closed]` y
`.site-footer__hours-today` («Hoy»), y la frase de respaldo usa `--cream-soft`
para ser legible sobre el pie oscuro.

### 16.7 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (238 tests, node --test; 213 eran
  de la entrega anterior, 25 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` del módulo nuevo y
  exports faltantes en `settings.ts`/`members.ts`/`publicSalon.ts`):
  `weeklyHours.test.ts` (relojes, borrador de siete filas, validaciones con las
  frases exactas, resumen y agrupación, copia de día, tramos/descansos,
  «igual que el salón», día de hoy con un instante cerca de medianoche en dos
  zonas, filas públicas, guardado optimista con vuelta atrás y normalización),
  `settings.test.ts` y `members.test.ts` (rutas, cuerpos `{days}`/`{segments}`,
  lista vacía y frase del servidor tal cual) y `publicSalon.test.ts` (`hours`
  presente, vacío y ausente).
- Revisión visual con el bundle de `vite build` servido con `vite preview`
  contra una API simulada en `/tmp/f2d1-shot` (horario del salón con domingo
  cerrado y sábado corto, una profesional con descanso de 13 a 14, otra «igual
  que el salón», un salón público con `hours` y `/salon/legacy` sin el campo) y
  Chromium headless por protocolo DevTools (el flag `--screenshot` se cuelga en
  este entorno) a 390 y 1280 px: `/panel/ajustes` (editor completo, día cerrado,
  error del servidor con «Reintentar», copia de lunes a todos, confirmación y
  vuelta al horario único), `/panel/equipo` (bloques «igual que el salón» y con
  horario propio, descansos, rollback con «Máximo 4 tramos por día») y la
  landing (pie con siete días y «Hoy», bloque de contacto con el resumen, y el
  respaldo por WhatsApp sin `hours`). Comprobado en el navegador:
  `scrollWidth === clientWidth` en ambas páginas y anchos; interruptor de 44 px,
  `Space` que conmuta (`aria-checked` incluido), `Tab` del interruptor al campo
  «Apertura» con `outline: solid 3px`, radios de 44 px, `input type="time"` de
  46 px, select de 44 px y botones pequeños de los editores a 44 px. Se
  corrigieron en la revisión: el texto «09:00 a.m.» recortado dentro de los
  tramos a 390 px (ahora cada hora usa su propia fila cuando no entra) y
  «Quitar»/«Agregar tramo» de 38 px.
- Fuera de alcance de esta entrega: vacaciones y días libres puntuales (F2d2) y
  cubrir ausencias (F2d3).

## 17. Fase 6 — F2d2: vacaciones y días libres puntuales

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2d2» (mitad frontend de
S-F2d2-5, más S-F2d2-6 y S-F2d2-7). Sin dependencias nuevas. El backend se
implementa en paralelo y no se tocó: la UI consume
`GET /salons/:id/time-off?from&to` (`{time_off:[…]}` con el motivo vacío para
quien no puede leerlo), `POST /salons/:id/members/:uid/time-off` (201
`{time_off, conflicts}`) y `DELETE /salons/:id/time-off/:tid` (204).

### 17.1 Reglas puras (`src/lib/timeOff.ts`)

| helper | regla |
| --- | --- |
| `timeOffRange({fromDate,toDate,fromTime?,toTime?}, zone)` | día completo = 00:00 local al 00:00 local del día siguiente al último; con horas parciales usa esos relojes de pared. Se apoya en `fromDateInputValue`/`toDateTimeIso`/`addDaysToDateValue`, así que sobrevive un cambio de horario (2026-03-08 en Nueva York es un día de 23 h) y una fecha cerca de medianoche (23:30 local del 24 ya es el 25 en UTC) |
| `validateTimeOff` | reglas del servidor en su orden y con sus frases exactas: «El fin debe ser posterior al inicio», «Una ausencia no puede durar más de 90 días», «No se puede cargar una ausencia que ya terminó», «Ya hay una ausencia en ese período»; el motivo se normaliza (trim, una línea, 120) |
| `validateTimeOffDraft` | lo mismo desde el formulario: primero las dos fechas («Elegí la fecha de inicio y la de fin»), después las dos horas del horario parcial («Completá las dos horas del horario parcial»), después la validación anterior. Esos dos textos son del formulario, no del API |
| `overlapsRange` / `absencesOnDay` / `absenceDays` | `[inicio, fin)` tocándose en el borde no se pisa; el día es el del salón, no el de UTC; una ausencia que termina en la medianoche del 27 no marca el 27 |
| `canSeeReason` / `visibleReason` | la administración lee todos los motivos; cualquier otra persona solo los de sus propias ausencias. Un payload que traiga el texto no lo imprime fuera de esa regla |
| `formatTimeOffRange` | «Del 24 al 26 de diciembre», «Del 24 de diciembre al 3 de enero», «El 24 de diciembre», «Hoy de 10:00 a 12:00»; el último día impreso de una ausencia de días completos es el que realmente cubre (el fin es exclusivo) |
| `timeOffSentence` | la misma frase en minúscula para el aviso: «Estás ausente del 24 al 26 de diciembre» |
| `currentAbsence` / `upcomingAbsences` | la ausencia en curso y, si no hay, la próxima; las terminadas no cuentan |
| `conflictsNotice` | «Tiene 1 turno en ese período: reasignalo o cancelalo desde el calendario» / «Tiene 3 turnos en ese período: reasignalos o cancelalos desde el calendario» |
| `timeOffBands` | una banda por persona para la ventana visible (un día), con las horas cuando la ausencia cubre solo una parte y el motivo solo si quien mira puede leerlo; `names` agrega el nombre para el día sin citas |
| `canEditAbsences` | solo dueño/administración (el API responde 403 al resto) |

`normalizeTimeOff`, `normalizeConflicts` y `normalizeTimeOffCreated` leen el
contrato y descartan filas sin id, sin persona o con fechas inutilizables.

### 17.2 `/panel/equipo` (S-F2d2-6)

- Nueva tarjeta **«Ausencias de quien atiende»** con un bloque (`TimeOffCard`)
  por persona reservable (`bookableMembers`, la regla de F2a). La lista de
  ausencias se pide **una vez** para todo el equipo
  (`GET /time-off?from&to`, desde hoy hasta 91 días después) con la zona de
  `/settings` (respaldo `TIME_ZONE`).
- Cada bloque muestra las ausencias **en curso y próximas** con su rango
  («Hoy», «El 22 de noviembre», «Del 26 al 27 de noviembre»), la etiqueta «En
  curso», el motivo y «Borrar» con confirmación en línea («¿Borrar la
  ausencia?» / «Sí, borrar» / «Volver»). Sin ausencias dice «No tiene ausencias
  cargadas de hoy en adelante».
- Alta: **Desde** y **Hasta** (`type="date"`), dos horas opcionales para una
  ausencia parcial y un **Motivo** opcional (120). Se valida con
  `validateTimeOffDraft` antes de enviar (el mensaje va junto al formulario en
  `role="alert"`), el `POST` manda `starts_at`/`ends_at` en UTC y `reason` solo
  cuando tiene texto. Éxito: «Ausencia cargada.» y la lista se recarga.
- Si la respuesta trae `conflicts`, aparece el aviso de
  `conflictsNotice(n)` con la lista de turnos (`HH:MM–HH:MM · clienta`) y el
  enlace **«Ver el calendario de ese día»** a
  `/panel/calendario?date=<día local del primer conflicto>`. `CalendarPage`
  acepta ese `?date=` (solo si es `YYYY-MM-DD`) como día inicial y al cambiar.
- Si `/time-off` falla, la tarjeta muestra el error del servidor y no inventa
  ausencias.

### 17.3 Calendario (S-F2d2-7)

- `CalendarPage` pide las ausencias del día visible con la misma ventana que
  las citas y las proyecta con `timeOffBands`. Un fallo de esa request deja la
  agenda como estaba (§11): nunca convierte la página en un error.
- `Timeline` dibuja la banda **«Ausente»** debajo del encabezado de la columna:
  las horas cuando la ausencia no cubre el día completo (`14:00–18:00`) y el
  motivo solo si quien mira puede leerlo. En la agenda móvil la banda es una
  fila más del grupo (`Todo el día` / `HH:MM–HH:MM`, «Ausente · motivo»), y con
  un día sin citas las bandas se imprimen encima del estado vacío.
- La banda aparece en la columna que se está dibujando: en «Mi agenda» se ve la
  propia, en «Todo el salón» la de todo el equipo.

### 17.4 Mi agenda (S-F2d2-7)

- Las ausencias de la semana se piden con el mismo rango que los turnos y se
  filtran a la persona (`timeOffForUser`). Si hay una en curso o próxima:
  **«Estás ausente del 24 al 26 de diciembre.»** (`role="status"`, solo con la
  respuesta viva, nunca durante un error).
- Cada día de la tira semanal con ausencia lleva un punto visible
  (`data-absent`) y el nombre accesible lo dice («Sábado 21: 1 turno,
  ausente»).

### 17.5 Accesibilidad y estilos

`app.css` suma el bloque «F2d2»: `.timeoff-card`/`.timeoff-list`/`.timeoff-item`
(con borde vino para la ausencia en curso y la etiqueta «En curso»),
`.timeoff-form__grid` (`repeat(auto-fit, minmax(9.5rem, 1fr))`: una columna en el
teléfono para que un `type="date"` no se recorte, dos desde que entran),
`.timeoff-conflicts` (aviso en dorado tenue con la lista y el enlace),
`.timeline__absence`/`.timeline-absences` (banda sobre la columna) y
`.week-strip__absent` (punto de la tira). Todos los controles nuevos son
`label` reales y botones de 44 px; el foco es el anillo de 3 px de `base.css`.
A 390 y 1280 px no hay scroll horizontal (`scrollWidth === clientWidth`).

### 17.6 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (260 tests, node --test; 238 eran
  de la entrega anterior, 22 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` del módulo nuevo):
  `timeOff.test.ts` cubre la conversión local→UTC (día completo, un solo día,
  cambio de año, 23:30 local que en UTC ya es el día siguiente, cambio de
  horario de 23 h, horas parciales), el borrador del formulario, la
  normalización del motivo, las cinco frases del servidor en su orden, el límite
  de 90 días exacto, la ausencia en curso, el solape con borde exclusivo, el día
  local de cada ausencia, la tira semanal, la privacidad del motivo, las bandas
  del calendario (con y sin horas, con y sin nombre), el aviso de conflictos en
  singular y plural, el cliente de API (`from`/`to`, cuerpo sin `reason` cuando
  está vacío, frase del servidor verbatim, `DELETE` 204) y `canEditAbsences`.
- Revisión visual con el bundle de `vite build` servido con `vite preview`
  contra una API simulada en `/tmp/f2d2-shot` (owner y una profesional, dos
  ausencias de hoy —una parcial y una de día completo—, dos próximas, un `POST`
  que devuelve dos conflictos) y Chromium headless por protocolo DevTools (el
  flag `--screenshot` se cuelga en este entorno) a 390 y 1280 px:
  `/panel/equipo` (lista, etiqueta «En curso», formulario y aviso de conflictos
  con el enlace), `/panel/calendario` (banda «Ausente» con horas y motivo, y
  sin motivo al mirarla otra profesional) y `/panel/mi-agenda` (aviso «Estás
  ausente…» y puntos de la tira). Comprobado en el navegador:
  `scrollWidth === clientWidth` en las tres páginas a 390 y 1280 px, campos de
  fecha y hora de 46 px, botones de 44 px, foco visible con `outline: solid
  3px`, y el calendario sin red mostrando la copia guardada (§11.6) pese a la
  request nueva. Se corrigieron en la revisión: el nombre accesible de «Borrar»
  («del el 22 de noviembre») y el espacio entre el rango y la etiqueta «En
  curso».
- Fuera de alcance de esta entrega: reasignar los turnos en conflicto (F2d3),
  que la profesional pida sus propias ausencias y las ausencias del salón
  completo.

## 18. Fase 6 — F2d3: cubrir una ausencia reasignando sus turnos

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2d3» (forma de S-F2d3-2 y
S-F2d3-5). Sin dependencias nuevas. El backend se implementa en paralelo y no se
tocó: la UI consume `GET /salons/:id/absences?from&to`,
`POST /salons/:id/absences/:tid/reassign {"professional_id"}` y
`POST /salons/:id/bookings/:bid/reassign` con `{"professional_id"}` o
`{"cancel":true}`. Las frases del servidor se muestran verbatim.

### 18.1 Reglas puras (`src/lib/coverAbsence.ts`)

| helper | regla |
| --- | --- |
| `normalizeCoverAbsences` | lee la respuesta real del API, `{absences:[{id,user_id,user_name,starts_at,ends_at,reason,bookings:[{id,client_name,starts_at,ends_at,service_id,service_name,candidates:[{professional_id,name,available,reason}]}]}]}`, y tolera además `id`/`time_off_id` en la ausencia y las banderas viejas `bookable`/`does_service`/`absent`; descarta ausencias sin id, sin persona o con rango inutilizable y turnos sin id o sin horas; `service_id` vacío es `null` («el turno no tiene servicio»); ordena por inicio |
| `absenceForBooking` / `coverBookingOf` | la ausencia y el turno que la API relaciona con un id de turno; `null` cuando el turno no es de ninguna ausencia |
| `affectedTurnsLabel` | «Ningún turno afectado» / «1 turno afectado» / «3 turnos afectados» (el plural del conteo) |
| `coverCandidates` | una fila por candidata, **elegibles primero**. Manda el servidor: si trae `reason` se imprime tal cual (es la frase que respondería el POST) y `available: true` es un visto bueno aunque la lista local de ausencias diga otra cosa. Sin respuesta del servidor, las reglas de S-F2d3-3 en su orden: «Esa persona no atiende clientes» (`bookable === false`), «Esa persona no está de turno ese día` (ausente en ese período, por la bandera del servidor o por la lista de `/time-off`) y «Esa persona no hace ese servicio» (solo si el turno tiene `service_id`) |
| `coverAllCandidates` | quienes pueden tomar **todos** los turnos de la ausencia, con el motivo del primer turno que esa persona no puede tomar |
| `candidateOptionLabel` / `ineligibleSummary` / `firstEligible` | «Caro — no disponible: Esa persona no hace ese servicio» y la nota «No puede tomarlo: …» debajo del selector; la primera elegible es el valor por defecto |
| `beginCover` / `chooseFor` / `chooseForAll` / `chosenFor` / `forgetChoice` / `rollbackCover` | estado optimista de los selectores sobre `beginSave`/`rollbackSave` de `weeklyHours.ts`: la elección se pinta antes de la respuesta; un rechazo devuelve **solo ese turno** a lo que el servidor tiene y «Reasignar todo» fallido vuelve al mapa guardado sin tocar a nadie más |
| `reassignedNotice` | «1 turno reasignado a Beto.» / «3 turnos reasignados a Beto.» / «No se reasignó ningún turno.» |
| `coveredNotices` | «Tu turno del 24 de diciembre a las 10:00 lo cubre otra profesional.» (o «de hoy a las …») para los turnos que el servidor marca con `covered_from` = mi id, `booked` y todavía por venir |
| `COVER_MESSAGES` | las seis frases del contrato (S-F2d3-3) en un solo lugar |

Una bandera que el servidor no manda (`null`) **nunca** se convierte en rechazo:
se ofrece el nombre y el servidor decide al reasignar. Si el servidor dice
`available: false` sin frase ni banderas, la UI escribe «No puede tomar el turno»
(texto solo de la UI; hoy el API siempre manda el motivo). La propia profesional
ausente no aparece como candidata de su ausencia aunque el payload la incluya.

### 18.2 `/panel/equipo` (S-F2d3-5)

- Cada ausencia de la tarjeta («Hoy · En curso · Motivo») gana un botón
  **«Cubrir»** (`aria-expanded` + `aria-controls`) que abre el panel de la
  ausencia; «Borrar» sigue al lado y con la misma confirmación.
- El panel muestra el conteo (`affectedTurnsLabel`) y, arriba, **«Reasignar
  todo a»**: un selector con quienes pueden tomar todos los turnos y el botón
  «Reasignar todo» (`POST …/absences/:tid/reassign`).
- Debajo, un renglón por turno afectado (horario y clienta) con **el mismo
  selector** —las que no sirven quedan deshabilitadas y el motivo se lee en
  texto debajo, porque una opción deshabilitada no recibe foco— y «Reasignar»
  (`POST …/bookings/:bid/reassign`).
- Los turnos salen de `GET /absences` con la misma ventana que las ausencias
  (hoy + 91 días). Si esa request falla o no trae la ausencia, el panel dice
  «Esta ausencia no tiene turnos agendados»; si la request falla, «No pudimos
  cargar los turnos de esta ausencia» (un cero inventado mentiría sobre el día).
- Éxito: aviso arriba de la página con `reassignedNotice` y recarga de ausencias
  y turnos. Rechazo: la frase del servidor en `role="alert"` dentro del panel y
  la elección de ese turno vuelve atrás. Sin candidatas elegibles el botón queda
  deshabilitado (el servidor respondería «No hay a quién reasignar»).

### 18.3 Calendario (S-F2d3-5)

- `CalendarPage` pide `GET /absences` con la ventana del día visible. Si falla
  (por ejemplo un 403 para recepción, porque el motivo de la ausencia es
  privado) la agenda queda exactamente como en §11 y no se ofrece reasignar.
- En el panel de un turno que la API relaciona con una ausencia aparece el bloque
  **«Reasignar»**: «Ana Ruiz está ausente hoy: elegí quién toma este turno», el
  mismo selector con los motivos, el botón «Reasignar» y **«Cancelar el turno»**
  con confirmación en línea que explica que el salón se contacta con la clienta
  («Se cancela el turno y el salón se contacta con la clienta para avisarle.
  ¿Cancelar el turno?» → «Sí, cancelar el turno» / «Volver»).
- Para un turno afectado, ese «Cancelar el turno» **reemplaza** al «Cancelar
  turno» genérico: una sola acción de cancelación por turno, y es la que avisa a
  la clienta. Las demás acciones (no asistió, atendida, reprogramar) no cambian.
- La banda «Ausente» de la columna (F2d2) ya marcaba a la profesional; el bloque
  aparece al abrir ese turno, con las mismas reglas de permisos de siempre
  (`canManageBooking`).

### 18.4 Mi agenda (S-F2d3-5)

- Cuando un turno que era mío lo cubre otra profesional, el servidor lo marca
  con `covered_from` (mi id). «Mi agenda» lista esos turnos arriba:
  **«Tu turno del 24 de diciembre a las 10:00 lo cubre otra profesional.»**
  (`role="status"`, solo con la respuesta viva: nunca durante un error o con la
  copia guardada).
- **Dependencia de contrato (desviación consciente, pendiente del backend):**
  `GET /bookings` no publica hoy de quién se movió el turno: el backend solo deja
  el aviso interno del salón «Turno reasignado · clienta · horario · ahora la
  atiende X» (`internal/app/coverage.go`), que no dice que el turno era mío y por
  lo tanto no sirve para este aviso. La UI lee el campo opcional `covered_from`
  de la reserva (y sus alias `reassigned_from` / `previous_professional_id`); sin
  alguno de ellos el aviso simplemente no aparece: no se inventa ni se deduce del
  texto de una notificación. Falta que `bookingJSON` agregue
  `"covered_from": <id de la profesional anterior>` al reasignar (una columna o
  el valor del `UPDATE`, que ya lo tiene a mano). El tipo `Booking` documenta el
  campo como opcional.

### 18.5 Accesibilidad y estilos

`app.css` suma el bloque «F2d3»: `.timeoff-item__buttons` (Cubrir y Borrar con el
margen automático en el grupo, no en cada botón), `.timeoff-item__cover`,
`.cover-turns`/`.cover-turns__all`/`.cover-turns__list`, `.cover-turn` (una
columna en el teléfono, cuatro desde 760 px), `.cover-field` (el `<select>`
nunca desborda y la nota de motivos envuelve) y `.booking-detail__cover`. Todos
los controles son `label` reales o botones de 44 px y el foco es el anillo de
3 px de `base.css`; medido en el navegador: `select` y botones nuevos de 44 px,
`Tab` desde el control anterior llega a «Cubrir» con `outline: 3px solid`. A 390
y 1280 px no hay scroll horizontal (`scrollWidth === clientWidth` en las tres
páginas).

### 18.6 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (282 tests, node --test; 260 eran de
  la entrega anterior, 22 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` del módulo nuevo):
  `coverAbsence.test.ts` cubre la lectura del payload (filas inutilizables,
  aliases, orden, `service_id` vacío, la forma real `professional_id`/
  `available`/`reason`), la relación turno↔ausencia, los plurales del conteo y
  del aviso, las tres etiquetas de rechazo en el orden del contrato, el motivo
  del servidor impreso tal cual y su veredicto por encima de la lista local, la
  ausencia leída de `/time-off` cuando el servidor no opina, el turno sin
  servicio (cualquiera puede tomarlo), la bandera desconocida (nunca rechaza),
  el `available: false` sin frase, la exclusión de la propia ausente,
  «Reasignar todo» con el motivo del primer turno imposible, las etiquetas de las
  opciones, el estado optimista con vuelta atrás (una fila y el lote completo),
  el aviso de Mi agenda (día, hora, «hoy», turnos cancelados o pasados fuera,
  alias del campo) y el cliente de API (`from`/`to`, los tres cuerpos, `409` con
  la frase del servidor tal cual).
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2d3-shot` (dueña con cuatro reservables, una profesional ausente hoy
  con dos turnos —uno con servicio de color y otro de corte—, candidatas con la
  forma real del API —`professional_id`, `available`, `reason`—: una que no hace
  el servicio y otra ausente ese día) y
  Chromium headless por protocolo DevTools (el flag `--screenshot` se cuelga en
  este entorno) a 390 y 1280 px: `/panel/equipo` (panel «Cubrir» abierto, dos
  turnos con su selector, «Reasignar todo» y los motivos), `/panel/calendario`
  (turno de la profesional ausente con «Reasignar», el selector y la
  confirmación de «Cancelar el turno») y `/panel/mi-agenda` (avisos «Tu turno de
  hoy a las 15:00 lo cubre otra profesional.»). Comprobado en el navegador:
  `scrollWidth === clientWidth` en las tres páginas y anchos, controles nuevos de
  44 px, `Tab` con `outline: 3px solid`, y el calendario sin la request de
  ausencias (403 de recepción) mostrando la agenda igual que antes.
- Fuera de alcance de esta entrega: reasignación automática (es una decisión del
  salón), la lista de espera (F7) y los avisos por WhatsApp (F12).

## 19. Fase 6 — F2e: puestos y equipamiento con capacidad

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2e» (forma de S-F2e-3 y
S-F2e-5). Sin dependencias nuevas. El backend que implementa el contrato en
paralelo no se tocó: la UI consume `GET/POST /salons/:id/resources`,
`PATCH /salons/:id/resources/:tid`, `POST /salons/:id/resources/:tid/units`,
`PATCH /salons/:id/resource-units/:uid` (con `future_bookings` en la respuesta) y
`DELETE /salons/:id/resources/:tid`, y los servicios aceptan y devuelven
`resource_type_id`. Las frases del servidor se muestran verbatim.

### 19.1 Tipo y cliente de API (`src/lib/equipment.ts`)

| campo | lectura |
| --- | --- |
| `ResourceType` | `{id, name, units, services}`: `normalizeEquipment` lee `{types:[…]}`, descarta filas sin id y deja el nombre recortado (o el id si viene vacío) |
| `ResourceUnit` | `{id, name, active}`: una unidad sin el flag booleano cuenta como activa (la columna nace en `true`; el equipo se quita desactivando, nunca borrando turnos) |
| `services` | entero ≥ 0; un valor raro cae en 0 |
| `Service.resource_type_id` | opcional en el tipo (`src/types.ts`) porque un API anterior no lo manda; se lee con `serviceResourceTypeId` (trim, `''`/`null` = sin equipo) |
| `Booking.resource_unit_id` | opcional, documentado para los turnos del personal |

- `fetchEquipment`, `createResourceType({name,count})`, `renameResourceType`,
  `addResourceUnits(typeId,count)`, `setResourceUnit(unitId,{name?,active?})` y
  `deleteResourceType` son el cliente fino del contrato. La respuesta del `PATCH`
  de unidad se lee con `normalizeResourceUnitUpdate` (tolera los campos sueltos o
  bajo `unit`, y `future_bookings` anidado).
- `createService`/`updateService` (`src/lib/api.ts`) normalizan
  `resource_type_id`: un id recortado, `null` para «sin equipo» y la clave
  omitida si el cuerpo no la trae (un servidor anterior ve el mismo cuerpo de
  siempre).

### 19.2 Reglas puras (`src/lib/equipment.ts`)

| helper | regla |
| --- | --- |
| `canEditEquipment` | solo dueño/administración: el resto no ve la entrada del menú ni la página (que igual explica el rol) |
| `activeUnitCount` | unidades activas de un tipo: la capacidad que se ofrece |
| `serviceOffersSlots` | un servicio sin equipo siempre ofrece turnos; con un tipo, solo si tiene al menos una unidad activa; un tipo borrado no ofrece ninguno |
| `futureBookingsNotice` | `null` con 0, «1 turno futuro usa este puesto (lo conserva)» y «N turnos futuros usan este puesto (los conservan)» |
| `validateResourceName` | espejo del servidor y en su orden: `trim`, saltos de línea a espacio, vacío → «El nombre del equipo es obligatorio», más de 60 → «El nombre no puede superar los 60 caracteres», repetido sin distinguir mayúsculas («Ya existe un equipo con ese nombre»); renombrar un tipo no choca consigo mismo |
| `canAddType` / `checkUnitCount` / `canAddUnits` | 20 tipos → «Máximo 20 tipos de equipo»; cantidad entera 1..50 (texto del formulario) y 50 unidades por tipo → «Máximo 50 unidades por tipo» |
| `nextUnitName` | primer número libre del tipo: «Silla 1», «Silla 3» → «Silla 2» |
| `applyUnitActive` | interruptor optimista sobre una copia: repetirlo con el valor anterior es la vuelta atrás exacta y nunca muta la lista guardada |
| `normalizeResourceUnitUpdate` | `{active: boolean \| null, futureBookings: number}` del `PATCH` de unidad |

### 19.3 `/panel/equipamiento` (S-F2e-5)

- Página nueva de la administración (menú y `EquipmentPage`) con el encabezado
  «Equipamiento del salón» y el formulario «Nuevo tipo de equipo»: nombre y
  cantidad de unidades (1..50). Al crear, la respuesta se recarga y el aviso dice
  cuántas unidades se crearon («Equipo creado con 2 unidades: «Manicura 1» a
  «Manicura 2».»). Los errores del servidor se leen tal cual en `role="alert"`.
- «Tipos y unidades» lista una tarjeta por tipo con el conteo «N de M unidades
  activas», cuántos servicios lo usan («1 servicio lo usa.», «Ningún servicio lo
  usa todavía.»), el renombrado del tipo (campo + «Guardar nombre», con la
  validación local y la frase del servidor si rechaza), «Unidades nuevas» con el
  nombre que recibirá la próxima («Se crearán como «Pelo 4»…») y «Agregar
  unidades» (deshabilitado en el límite, con su frase).
- Cada unidad es una fila: interruptor `role="switch"` («Equipo activo: Pelo 2»),
  su nombre, la píldora «Activa»/«Inactiva» y «Renombrar» (campo en línea con
  «Guardar»/«Cancelar»). Desactivar es la forma de quitar equipo: la fila cambia
  antes de la respuesta y, si el `PATCH` trae `future_bookings`, el aviso dice
  «Pelo 2: 2 turnos futuros usan este puesto (los conservan).»; un rechazo la
  devuelve a su valor anterior con el mensaje del servidor.
- «Borrar equipo» pide confirmación en línea («¿Borrar el equipo? Las unidades
  con turnos no se pueden borrar.» → «Sí, borrar» / «Volver»); un `409` muestra
  «Hay servicios o turnos que lo usan: desactivá las unidades en lugar de
  borrarlo» dentro de la tarjeta y no borra nada.

### 19.4 `/panel/servicios` (S-F2e-5)

- El formulario de alta y de edición suma el selector «Equipo que usa» con
  «Ninguno (sin equipo)» y los tipos del salón, más la ayuda de que un turno
  ocupa una unidad activa. Si el tipo elegido no tiene unidades activas aparece
  «No hay equipo activo: no se ofrecen turnos» (`role="status"`). Si el
  equipamiento no se pudo leer, el tipo actual del servicio se conserva como
  opción («Equipo actual (sin nombre)»): guardar el servicio nunca lo borra en
  silencio.
- La tabla gana la columna «Equipo»: el nombre del tipo, «Sin equipo» o «Equipo
  borrado», y bajo el nombre el aviso «No hay equipo activo: no se ofrecen
  turnos» cuando el tipo no tiene ninguna unidad activa. Si `GET /resources`
  falla no se dibuja la columna ni se afirma nada del equipamiento (un cero
  inventado mentiría, como en F2c con `/members`).
- La columna «Profesionales» pasó su `display: grid` a un `span` interno
  (`td.service-cell`): con dos celdas no-tabla contiguas el navegador las
  fusionaba en una celda anónima y «Equipo» se dibujaba dentro de
  «Profesionales». El bug existía desde F2c y solo se veía al sumar la segunda
  columna.

### 19.5 Menú y navegación móvil

- `NAV` suma «Puestos» (`/panel/equipamiento`, `managerOnly`) después de
  «Servicios»: la décima entrada queda en dos filas de cinco a 375 y 390 px con
  44 px de alto cada enlace (`scrollWidth === clientWidth`). **Desviación
  consciente:** la etiqueta del menú es «Puestos» y no «Equipamiento» porque
  «Equipamiento» no entra en una columna de ~66 px a 375 px y partiría el
  enlace; el `h1` de la página mantiene «Equipamiento del salón». Un
  profesional o recepción no ve la entrada.

### 19.6 Accesibilidad y estilos

`app.css` suma el bloque «F2e»: `.equip-form__grid`, `.equip-grid` (dos columnas
desde 760 px, una en el teléfono), `.equip-card`/`.equip-card__fields`,
`.equip-card__add` (el número y su botón envuelven), `.equip-units`/`.equip-unit`
(fila de 44 px con filete, nombre con `overflow-wrap: anywhere`),
`.equip-unit__rename` y `.equip-card__foot`. Todos los botones del formulario y
de las tarjetas son de 44 px; el interruptor reutiliza `.attends` de F2a (44 px,
foco de 3 px de `base.css`), y la celda nueva de la tabla usa `.service-cell`.
Medido en el navegador: ningún control nuevo por debajo de 44 px (los únicos
menores son el botón de avisos y los `btn--sm` que ya existían), `Tab` desde el
campo de cantidad llega al siguiente control con `outline: 3px solid`, y a 390 y
1280 px no hay scroll horizontal en ninguna de las dos páginas.

### 19.7 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (296 tests, node --test; 282 eran
  de la entrega anterior, 14 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` del módulo nuevo y
  `resource_type_id` sin normalizar en `createService`): `equipment.test.ts`
  cubre la lectura del payload (unidades sin flag, filas inutilizables,
  `services` raro), quién edita, el conteo de unidades activas, la regla de «sin
  equipo activo no se ofrecen turnos», el aviso de turnos futuros en singular y
  plural, la validación del nombre con las cinco frases del servidor (trim, una
  línea, 60 exactos, 61, repetido sin mayúsculas, renombrado de sí mismo), los
  límites 20/50 y la cantidad 1..50, el siguiente número libre con huecos,
  el interruptor optimista con vuelta atrás exacta sin mutar la lista y la
  lectura del `PATCH` de unidad. `services.test.ts` cubre `resource_type_id` en
  el alta (id recortado, `null` y clave omitida) y su borrado en la edición.
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2e-shot` (tres tipos con unidades activas e inactivas, un tipo sin
  ninguna activa usado por un servicio, un `PATCH` que devuelve
  `future_bookings: 2` y un `DELETE` que responde `409` con la frase del
  contrato) y Chromium headless por protocolo DevTools (el flag `--screenshot` se
  cuelga en este entorno) a 390 y 1280 px: `/panel/equipamiento` (lista, aviso
  «Pelo 2: 2 turnos futuros usan este puesto (los conservan).», alta de
  «Manicura» con dos unidades y rechazo del borrado de «Barbería» con el mensaje
  del servidor) y `/panel/servicios` (columna «Equipo» con el aviso de falta de
  unidades activas, selector en alta con «Ninguno» y los tipos, y edición de
  «Color y planchado» con «Barbería» y el aviso). Comprobado en el navegador:
  `scrollWidth === clientWidth` en las dos páginas y anchos, la barra móvil con
  las diez entradas en dos filas y 44 px, y el foco de 3 px al tabular.
- Fuera de alcance de esta entrega: equipo en la reserva de staff y el
  mantenimiento programado (el contrato los excluye).

## 20. Fase 6 — F2g1: invitaciones de equipo por email

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2g1» (formas de S-F2g1-3 y
S-F2g1-4, más S-F2g1-6). Sin dependencias nuevas. El backend que implementa el
contrato en paralelo no se tocó: la UI consume
`POST/GET /salons/:id/invitations`, `DELETE /salons/:id/invitations/:iid`,
`POST /public/invitations/lookup {token}`,
`POST /public/invitations/accept {token,name,password}` y
`POST /invitations/accept {token}`. Las frases del servidor se muestran verbatim.

### 20.1 El token: fragmento, memoria y cuerpo

El enlace es `/invitacion#<token>`: el fragmento no viaja al servidor ni a los
logs de nginx. La página (`src/app/InvitationPage.tsx`) cumple, en este orden:

1. `readInvitationHash(location.href)` lee el token **en el primer render** y
   devuelve la URL sin él; `history.replaceState` la escribe en el mismo tick,
   **antes de cualquier request**.
2. El token vive solo en un `useRef` del componente: nunca `localStorage`, nunca
   `sessionStorage`, nunca la consola, nunca un mensaje de error, nunca el query
   ni la ruta. Recargar la página lo pierde: el enlace es de un solo uso y no se
   puede recuperar (S-F2g1-3).
3. Solo viaja en el **cuerpo** de `lookup` y de `accept` (`{token}`).
4. Un token inexistente, vencido, usado o revocado produce **la misma pantalla**:
   el título y el mensaje son `Invitación inválida o vencida` (la única frase del
   servidor), sin botón «Reintentar» (reintentar no puede ayudar) y con la
   instrucción de pedirle una invitación nueva al salón. Un fallo de conexión es
   otro estado y sí se puede reintentar. Nunca se distingue la causa.
5. Los formularios llevan `method="post"` además de `onSubmit` con
   `preventDefault()`: ni siquiera un fallo de JavaScript puede mandar la
   contraseña en la URL. El campo es `type="password"` con
   `autocomplete="new-password"`; el del login existente,
   `current-password` como en `/login`.

Reglas puras de `readInvitationHash` (probadas):

| entrada | token | URL resultante |
| --- | --- | --- |
| `…/invitacion#<43 chars>` | el token | `…/invitacion` |
| `…/invitacion?x=1#<token>` | el token | `…/invitacion?x=1` |
| `…/invitacion` o `#` | `null` | sin cambios / sin fragmento |
| `#corto`, `#con espacio`, `#abc=` | `null` | fragmento eliminado |
| `#<token>#paso-2` | el token | `#paso-2` (ancla conservada) |
| `#paso-2#<token>` | `null` | `#paso-2` (el token se borra, no se usa) |

Un token es base64url de 20 a 128 caracteres (`crypto/rand` de 32 bytes son 43).
El token se busca en cualquier fragmento para **borrarlo**, pero solo se **usa**
si está en el primero (la forma del enlace del contrato): una URL rara nunca
entrega una sesión.

### 20.2 Reglas puras (`src/lib/invitations.ts`)

| helper | regla |
| --- | --- |
| `isInvitationToken` / `readInvitationHash` | el token del contrato y la limpieza de la URL (§20.1) |
| `validateSignup` | nombre recortado y no vacío, hasta 120 caracteres; contraseña de 8 a 72 **bytes** (bcrypt trunca en 72 y una letra acentuada ocupa dos), nunca recortada. Mensajes: «Escribe tu nombre», «El nombre no puede superar los 120 caracteres», «La contraseña debe tener al menos 8 caracteres», «La contraseña no puede superar los 72 caracteres» |
| `emailHintLabel` | el `email_hint` **tal como llega** (`l***@mail.com`), recortado; vacío → «el correo de la invitación». La UI no enmascara ni reconstruye: el correo real nunca lo tiene |
| `invitationRoleLabel` | la etiqueta del servidor si viene; si no, la de `roleLabel` en español |
| `expiryDay` / `expiryLabel` / `expirySentence` | el día del vencimiento en la zona del salón: «Vence el 29 de noviembre» (con el año solo si no es el actual) y «vence el 29 de noviembre» en minúscula; `null` con una fecha inusable, para no inventar un día |
| `canInviteRole` / `invitableRoles` | espejo de `domain.Role.AddableBy`: la dueña invita administración, profesional y recepción; una cuenta de administración, profesional y recepción; **nunca** dueño ni cliente |
| `canInvite` | solo dueño/administración |
| `emailIsMember` | comparación recortada y en minúsculas contra `GET /members` |
| `applyRevoke` / `rollbackRevoke` | baja optimista sin mutar la lista y vuelta atrás exacta, reinsertando por vencimiento e id y sin duplicar |
| `linkOnceFrom` / `dismissLinkOnce` | el enlace del 201 mientras se muestra; `null` si el servidor no lo mandó y para siempre al descartarlo |
| `LINK_ONCE_NOTE` | `Este enlace se muestra una sola vez` |
| `linkDeliveryNotice` | «Te enviamos el enlace por correo.» / «No pudimos enviar el correo: compartí el enlace por WhatsApp o a mano.» (el envío es best-effort) |
| `copyInvitationLink` | `navigator.clipboard.writeText`; `false` si el navegador lo niega o no existe (el enlace se selecciona a mano); un enlace vacío no toca el portapapeles |
| `invitationLandingPath` | profesional → `/panel/mi-agenda`; el resto → `/panel` |
| `invitationErrorMessage` | la frase del servidor verbatim (sin reescribir el 403) |
| `normalizePendingInvitations` | lee `{invitations:[…]}`, descarta filas sin id y copia **solo** id, email, rol y vencimiento: un `link` o un `token` filtrado por el servidor jamás llega a la pantalla |
| `normalizeCreatedInvitation` / `normalizeInvitationLookup` | lectura de las respuestas del contrato; `lookup` sin nombre de salón es `null` |

### 20.3 `/panel/equipo` (S-F2g1-6)

- Bloque **«Invitar a alguien»** debajo del alta por correo existente (que no
  cambió): correo y rol, con la ayuda de que el enlace vence a los 7 días. Si el
  correo ya es de alguien del equipo aparece «Esa persona ya es parte del equipo:
  no hace falta invitarla.» (`role="status"`) sin bloquear el envío: decide el
  servidor. Con 50 pendientes el botón se deshabilita y se explica
  «Máximo 50 invitaciones pendientes».
- Al crear, el aviso confirma la creación y aparece el bloque del enlace:
  campo de solo lectura con el enlace, **«Copiar enlace»** y la nota
  **«Este enlace se muestra una sola vez»**. Al copiar se anuncia «Enlace
  copiado.» en una región `role="status"` (polite); si el navegador lo niega, el
  campo recibe el foco y el texto queda **seleccionado** con la explicación
  visible. «Ya lo guardé» descarta el enlace para siempre.
- **Invitaciones pendientes**: una fila por invitación con correo, píldora del
  rol, «Vence el 29 de noviembre» y «Revocar» con confirmación en línea
  («¿Revocar la invitación?» → «Sí, revocar» / «Volver»). La fila desaparece
  antes de la respuesta (optimista) y vuelve a su lugar si el `DELETE` falla, con
  la frase del servidor en `role="alert"`. Lista vacía: «No hay invitaciones
  pendientes.»; si la lista no se pudo leer se muestra el error y **no** se
  afirma que no hay ninguna.
- El formulario no manda `attends`: el valor por defecto del rol lo decide el
  servidor (S-F2a-4). Un `POST` rechazado imprime la frase del contrato tal cual
  («Esa persona ya es parte del equipo», «No podés invitar con ese rol»,
  «Email inválido»).

### 20.4 `/invitacion` (pública)

- Ruta nueva fuera de `RequireAuth`. Estados: «Revisando tu invitación»
  (lookup), «Invitación a <salón>» con **rol**, `email_hint` y vencimiento, la
  pantalla única de inválida/vencida y el error de conexión con «Reintentar».
- **Sin cuenta** (`has_account: false`): «Creá tu cuenta» con nombre y contraseña
  (misma validación que `POST /auth/register`), «Crear cuenta y entrar». Al
  aceptar se guarda la sesión devuelta por el `POST /public/invitations/accept`,
  se lee `/me` para fijar salón y rol y se entra al panel: un profesional
  aterriza en **Mi agenda**.
- **Con cuenta** (`has_account: true`): «Iniciá sesión para aceptar». Si el
  navegador ya tiene sesión, un botón llama al `POST /invitations/accept`
  autenticado (y «Cerrar sesión y entrar con otra cuenta» da salida a una cuenta
  equivocada); si no, el correo y la contraseña se escriben en la misma página y
  luego se acepta con la sesión nueva en el mismo paso. Se hace así porque el
  token solo existe en memoria de este componente: navegar a `/login` lo
  perdería. Un `403 {"error":"Esta invitación es para otra cuenta"}` se lee
  verbatim.
- `UnauthorizedRedirect` (`App.tsx`) **no** navega fuera de `/invitacion` ante un
  401: una contraseña equivocada en el login de la invitación muestra la frase
  del servidor y conserva el token.

### 20.5 Accesibilidad y estilos

`app.css` suma el bloque «F2g1»: `.invite-link` (caja dorada con el enlace y sus
acciones), `.invite-list`/`.invite-list__item` (fila que envuelve a 390 px con
objetivo de 44 px), `.invite` (fondo crema con degradado) y `.invite__card` (una
sola tarjeta de 34 rem como máximo, el mismo marco de tokens que el panel),
`.invite__meta` (rol, correo enmascarado y vencimiento) y `.invite__form`. Todos
los botones nuevos son de 44 px (los `btn--sm` incluidos) y el foco es el anillo
de 3 px de `base.css`. Etiquetas `<label for>` reales en todos los campos.

### 20.6 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (332 tests, node --test; 296 eran de
  la entrega anterior, 36 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` de
  `src/lib/invitations.ts`): `invitations.test.ts` cubre el token del fragmento
  (formas válidas, URL sin fragmento, fragmentos malformados, token fuera del
  primer lugar, longitud y alfabeto), nombre y contraseña con los límites del
  registro incluidos los bytes de una letra acentuada, el `email_hint` tal como
  llega, las etiquetas de rol del servidor y locales, el vencimiento en la zona
  del salón (cruce de medianoche, año distinto, fecha inusable), las once
  combinaciones de `AddableBy`, la lista de roles invitables, la coincidencia de
  correos del equipo, la baja optimista con vuelta atrás exacta y sin duplicar,
  el estado del enlace de una sola vez, los avisos de entrega, la copia con
  portapapeles disponible/negado/ausente, el destino del panel, la lectura de los
  tres payloads (incluido que un `link`/`token` ajeno no sobreviva a la
  normalización) y el cliente de API (rutas, cuerpos con el token solo en el
  body, `attends` opcional, ausencia de `Authorization` en las rutas públicas y
  las frases del servidor tal cual en 400/403/404/409).
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2g1-shot` (dos invitaciones pendientes, un `POST` con
  `email_sent: false`, un `DELETE` 204, lookup de cuenta nueva, de cuenta
  existente y 404 genérico) y Chromium headless por protocolo DevTools (el flag
  `--screenshot` se cuelga en este entorno) a **390 y 1280 px**, con 22 capturas
  revisadas una por una: `/panel/equipo` (formulario de invitación, aviso de
  correo ya miembro, enlace de una sola vez, fallback visible al negarse el
  portapapeles, confirmación de revocación y lista después de revocar) y
  `/invitacion` (cuenta nueva, contraseña corta, cuenta existente, contraseña
  equivocada sin perder la invitación, token inválido y panel de destino tras
  aceptar). Comprobado en el navegador:
  - `scrollWidth === clientWidth` en las dos páginas y en los dos anchos.
  - Tras cargar `/invitacion#<token>`: `location.hash` vacío, la URL sin el
    token, el token ausente del DOM y de `performance.getEntriesByType`, cero
    requests con el token en la URL, y el token presente en los cuerpos del
    lookup y del accept.
  - `type="password"` + `autocomplete="new-password"`, etiquetas reales, `Tab`
    del correo al selector de rol y de ahí a «Crear invitación» con
    `outline: solid 3px`, y del nombre a la contraseña igual; los controles
    nuevos miden 44 px (los `input` 44 y los botones 47).
  - El aceptar con la cuenta nueva deja `salon.token` de la sesión del servidor y
    aterriza en `/panel/mi-agenda` (rol profesional); la contraseña equivocada
    en el login existente se queda en `/invitacion` con «credenciales inválidas».
- **Desviaciones conscientes:**
  - No existe formulario de registro en el frontend (las cuentas se crean solo
    por API), así que «las mismas reglas del registro» son las de
    `POST /auth/register` en el dominio: nombre recortado de 1 a 120 caracteres y
    contraseña de 8 a 72 **bytes**, espejadas en `validateSignup`.
  - El camino de «cuenta existente» inicia sesión en la propia página en lugar de
    navegar a `/login`: el token vive solo en la memoria del componente y una
    navegación lo destruiría.
  - A 390 px el enlace completo no entra en el campo (un `input` no envuelve): se
    puede desplazar, se copia con el botón y, si el portapapeles falla, queda
    seleccionado para copiarlo a mano.

## 21. Fase 6 — F2g2: dar de baja en vez de borrar (equipo)

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F2g2» (formas de S-F2g2-4 y
S-F2g2-5). Sin dependencias nuevas. El backend que implementa el contrato en
paralelo no se tocó: la UI consume `GET /salons/:id/members` (cada item con
`active` y `ended_at`, activos Y dados de baja),
`DELETE /salons/:id/members/:uid` (204, ahora da de baja) y
`POST /salons/:id/members/:uid/reactivate` (200 con el miembro). Las frases del
servidor se muestran verbatim.

### 21.1 Tipo y lectura (`src/lib/members.ts`)

| campo | lectura |
| --- | --- |
| `SalonMember.active` | `memberships.ended_at IS NULL`; un payload **sin** el flag cuenta como activo, así un servidor anterior sigue funcionando |
| `SalonMember.ended_at` | la fecha ISO de la baja, o `null`; un texto que no es fecha se descarta (no se imprime un día inventado) |
| `deactivateMember(salonId, uid)` | `DELETE`, 204; el `409` de turnos pendientes y el `400` del último dueño se leen tal cual |
| `reactivateMember(salonId, uid)` | `POST …/reactivate` → miembro normalizado; el `409` «Esa persona ya está en el equipo» se lee tal cual |

Regla de consistencia: una fecha de baja siempre gana sobre un `active`
contradictorio, así un payload a medio escribir no devuelve a nadie a la agenda.
`GET /members` sigue devolviendo a los dados de baja: el filtro es del cliente.

### 21.2 Reglas puras (`src/lib/teamStatus.ts`)

| helper | regla |
| --- | --- |
| `isActiveMember` | `active !== false` **y** `ended_at === null`; la fecha manda sobre el flag |
| `activeMembers` / `formerMembers` | el equipo en el orden del servidor, y el ex equipo con la baja más reciente primero (una baja sin fecha va última) |
| `formerTeamLabel` | «Ex equipo (3)» con el conteo, nunca negativo |
| `endedOnLabel` | «Dio de baja el 29 de noviembre» en la zona del salón (con el año solo si no es el actual; reutiliza `expiryDay` de F2g1); `null` con una fecha inusable, para omitir el día en vez de inventarlo |
| `deactivateConfirmQuestion` / `DEACTIVATE_CONFIRM_NOTE` | «¿Dar de baja a Ana?» + «Conserva su historial y puede volver a entrar cuando quieras» |
| `reactivateConfirmQuestion` / `REACTIVATE_CONFIRM_NOTE` | «¿Reactivar a Ana?» + «Vuelve a tener acceso al salón con el mismo rol.» |
| `DEACTIVATE_SUCCESS` | «La persona ya no tiene acceso al salón. Su historial se conserva.» (texto del contrato) |
| `reactivatedSuccess` | «Ana vuelve al equipo.» |
| `applyMemberEndedAt` | cambio optimista sobre una copia: `null` reactiva; la lista recibida no se muta |
| `restoreMember` | vuelta atrás **exacta**: repone la fila que el llamador guardó antes (mismo `active`, `ended_at`, oficio y servicios) |

Filtro de todos los selectores: `bookableMembers` (`src/lib/tradeServices.ts`)
ahora exige `isActiveMember`, `professionalsForService` se apoya en él (y con eso
el conteo por servicio y los avisos de F2c), y `nobodyAttends`
(`src/lib/attends.ts`) ignora a quien ya salió, para que el aviso «Nadie atiende
todavía» no quede tapado por una baja. `ServicesPage` (selector de profesionales
del alta de servicios), las tarjetas de oficio, ausencias y horario, y
`TeamInvitations` (para el aviso de correo ya miembro) reciben solo activos.

### 21.3 `/panel/equipo` (S-F2g2-5)

- «Quitar» pasa a **«Dar de baja»** (`aria-label` «Dar de baja a <nombre>») con
  confirmación en línea: la pregunta con el nombre, la nota de lo que se
  conserva, «Sí, dar de baja» y «Volver». La fila sale de la tabla antes de la
  respuesta (optimista) y vuelve **idéntica** si el servidor rechaza; el `409`
  se imprime en la propia fila dentro de `role="alert"`
  («Tiene 2 turnos pendientes: reasignalos o cancelalos antes de darla de baja»).
  El aviso de éxito es el del contrato.
- Las reglas de quién puede dar de baja a quién (el dueño a cualquiera, la
  administración solo a profesional, recepción y cliente; el último dueño activo
  no se da de baja) son **del servidor**: no se agregó ninguna guarda nueva en el cliente y su frase se
  muestra tal cual.
- La tabla principal («Quiénes tienen acceso», con su conteo) lista **solo
  activos**.
- Bloque plegable **«Ex equipo (N)»** debajo de la tabla, con
  `aria-expanded`/`aria-controls` y sufijo «Ver a quiénes salieron»/«Ocultar»:
  una fila por baja con nombre, correo, píldora del rol, «Dio de baja el 29 de
  noviembre» y «Reactivar» (confirmación en línea «¿Reactivar a Ana?», que al
  aceptar devuelve la persona al equipo con el mismo rol). Si la reactivación
  falla, la fila no se mueve y la frase del servidor aparece en la fila. El
  bloque no se dibuja cuando no hay nadie dado de baja.
- El ex equipo no aparece en oficios, servicios, ausencias ni horarios: esas
  tarjetas se arman con `bookableMembers`, que ya excluye a los dados de baja.

### 21.4 Accesibilidad y estilos

`app.css` suma el bloque «F2g2»: `.team-confirm` (columna con la pregunta, la
nota, los botones y el error), `.team-confirm__buttons` y `.ex-team__*`
(`__toggle` de 44 px con chevrón `aria-hidden`, `__list`, `__item` que envuelve a
390 px, `__meta`, `__date`, `__actions`). Todos los botones nuevos son de 44 px y
el foco es el anillo de 3 px de `base.css`; etiquetas reales y `role="group"` con
nombre en cada confirmación. Medido en el navegador: `scrollWidth ===
clientWidth` a 390 y 1280 px en todas las capturas, sin desborde del correo ni de
las frases largas del servidor (`overflow-wrap`).

### 21.5 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (**351 tests**, `node --test`; 332
  eran de la entrega anterior, 19 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` de
  `src/lib/teamStatus.ts`, `members.ts` sin exportar `deactivateMember`/
  `reactivateMember`, y `bookableMembers`/`nobodyAttends` sin filtrar bajas —
  5 tests en rojo): `teamStatus.test.ts` cubre quién está activo (incluida la
  fecha que contradice al flag), el corte activos/ex equipo con su orden, la
  etiqueta del bloque con el conteo, «Dio de baja el 29 de noviembre» (cruce de
  medianoche en la zona, año distinto, fecha inusable), las copias de la
  confirmación y del aviso de éxito, el cambio optimista de baja y de
  reactivación sin mutar la lista, y la vuelta atrás exacta. `members.test.ts`
  cubre la lectura de `active`/`ended_at` (sin flag, flag basura, fecha de baja
  sin flag, fecha inusable, los dados de baja siguen en la lista), el `DELETE`
  204 con el `409` verbatim de turnos pendientes y el `400` del último dueño, y el
  `reactivate` (ruta, método, miembro devuelto y «Esa persona ya está en el
  equipo»). `tradeServices.test.ts` y `attends.test.ts` cubren que los
  selectores y el aviso de «nadie atiende» ignoren a los dados de baja.
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f2g2-shot` (tres activos y dos dados de baja, un `DELETE` que
  responde `409` con la frase del contrato, otro `204`, y un `reactivate` 200) y
  Chromium headless por protocolo DevTools (el flag `--screenshot` se cuelga en
  este entorno) a **390 y 1280 px**, con 12 capturas revisadas una por una:
  `/panel/equipo` (lista activa con «Dar de baja», confirmación de baja,
  `409` de turnos pendientes en la fila, «Ex equipo (2)» plegado, desplegado con
  las dos fechas, confirmación de reactivación y el aviso de baja exitosa).
  Comprobado en el navegador: ningún `scrollWidth > clientWidth`, los botones
  nuevos de 44 px y el foco del anillo de 3 px al tabular.
- Fuera de alcance de esta entrega: borrar definitivamente, reasignación masiva
  de turnos al dar de baja y columnas del calendario para los dados de baja
  (el contrato los excluye).

## 22. Fase 6 — F3.2: modelo de pago, nómina y mis comisiones

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F3» (formas de D3/D5/D7 y
S-F3.2-5). Sin dependencias nuevas. El backend de F3.1/F3.2 se implementa en
paralelo y no se tocó: la UI consume `GET /salons/:id/pay-models`,
`PUT /salons/:id/members/:uid/pay-model`,
`POST /salons/:id/payroll/preview {kind,date}` y
`GET /salons/:id/payroll/me?kind=&date=` con las formas del contrato, y muestra
las frases del servidor verbatim en los errores de esos endpoints.

### 22.1 Lectura defensiva (`src/lib/payroll.ts`)

| payload | regla |
| --- | --- |
| `GET /pay-models` | lee `{pay_models:[…]}` o una lista suelta y descarta filas sin `user_id`; `fixed_cents` negativo, fraccionario, infinito o que no es número → `0`; `fixed_period` desconocido → `month` (D3); `commission_enabled` ausente → `true` (sin fila = solo comisión); `active` ausente → `true`; `role` desconocido → `professional`; sin nombre se usa el id, nunca una persona vacía |
| `PUT …/pay-model` | lee el objeto devuelto, suelto o bajo `pay_model`; si no trae `user_id` es un error explícito, no un guardado mudo |
| `POST /payroll/preview` | `period` exige `kind` válido y dos fechas locales reales (el round-trip por `Date.UTC` rechaza `2026-02-30`), `closed` es `true` solo si vino `true`; `lines` descarta filas sin `user_id` y normaliza conteos y centavos; un `total_cents` ausente es `commission + fixed` en enteros; los `totals` del servidor se usan solo si son enteros no negativos y, si no, se recalculan de las líneas para que la tabla y su pie nunca se contradigan |
| `GET /payroll/me` | `{period, sales_count, sales_total_cents, commission_cents, estimated}`; todo campo ausente o inválido cae a `0`/`false`; la pantalla no pide ni muestra datos de otra persona |

### 22.2 Reglas puras (`src/lib/payroll.ts`)

| helper | regla |
| --- | --- |
| `canManagePayroll` | solo dueño/administración (D7): la entrada «Nómina» del menú y la página |
| `canViewOwnCommissions` | dueño, administración y profesional sí; recepción no (D7) |
| `isPayPeriodKind` / `isValidPayrollDate` / `checkPayrollInput` | `week`/`fortnight`/`month` y un día de calendario `YYYY-MM-DD` real; si alguno falla, la frase del servidor `Período inválido` y no se hace la request |
| `payPeriodLabel` | «Semana del 3 al 9 de noviembre», «Quincena del 1 al 15 de noviembre», «Noviembre de 2026»; una semana que cruza mes nombra los dos bordes («Semana del 30 de noviembre al 6 de diciembre») y si cruza año lleva los dos años; una fecha inusable imprime «Período» en vez de inventar un día |
| `payrollTotals` / `payrollSalesTotals` | sumas de centavos y conteos en enteros, sin pasar por coma flotante; lista vacía → ceros |
| `amountInputFromCents` | `150000` → `1500.00` con aritmética entera (nunca `cents / 100`); el monto se escribe con `parseAmountToCents` |
| `applyPayModel` / `restorePayModel` | guardado optimista de una fila sin mutar la lista y vuelta atrás **exacta** al objeto que el llamador guardó |
| `payModelSummary` | lectura del modelo para el ex equipo: «Fijo $1,500.00 por mes + comisión», «Fijo $500.00 por semana, sin comisión», «Solo comisión», «Sin pago fijo ni comisión» |

Copias del contrato exportadas y usadas por las pantallas: `ESTIMATED_BADGE`
(«estimado»), `ESTIMATED_NOTICE` («Incluye ventas anteriores a la nómina: su
comisión se calcula con el porcentaje actual del servicio»), `OPEN_PERIOD_NOTICE`
(«Período en curso: las cifras pueden cambiar»), `FORMER_MEMBER_LABEL` («Dado de
baja») y `PAY_MODEL_EXPLANATION` (mixto = fijo + comisión; el fijo se paga solo
cuando el período liquidado coincide con el período fijo de la persona).

### 22.3 `/panel/equipo` (S-F3.2-5)

- Bloque **«Modelo de pago de cada persona»** con una tarjeta por miembro activo
  (`PayModelCard`): monto fijo (se guarda al salir del campo o con Enter; `0` =
  sin fijo), selector del período fijo (Semana, Quincena, Mes) y casilla
  «También gana comisión sobre sus ventas». Cada cambio se pinta antes de la
  respuesta y, si el servidor rechaza, la fila vuelve **idéntica** y su frase se
  lee en `role="alert"` (probado con un `400` de monto máximo: el campo vuelve a
  `5000.00`). Un monto ilegible se rechaza en el cliente con su propio mensaje y
  no viaja.
- El **ex equipo** muestra el modelo **solo para consulta** junto a la píldora
  «Dado de baja» («Fijo $900.00 por quincena + comisión»); no hay campos
  editables ahí. Si `/pay-models` falla, la tarjeta dice que no se pudo leer y no
  se edita a ciegas (nunca se envía un modelo inventado).

### 22.4 `/panel/nomina` (S-F3.2-5)

- Dueño/administración. Selector «Período» (Semana, Quincena, Mes) y «Fecha
  dentro del período» (`type="date"`); el servidor deriva el período en la zona
  del salón, así que cualquier día del período sirve. El rótulo del período
  llegado del servidor se imprime tal cual lo arma `payPeriodLabel`.
- Tabla «Por profesional»: profesional, ventas, total vendido, comisión, fijo y
  total, con una fila de **Totales** en `tfoot` (conteo y montos en enteros). Las
  personas dadas de baja llevan «Dado de baja»; las líneas estimadas llevan la
  píldora «estimado» y, cuando hay alguna, la explicación llana del contrato.
- `period.closed === false` muestra «Período en curso: las cifras pueden cambiar».
- Estados: carga y actualización (`LoadingState`), error con «Reintentar»
  (`ErrorNotice`), aviso de copia guardada por el service worker («Sin conexión:
  mostrando datos guardados…») y vacío honesto («No hay ventas ni montos fijos en
  este período»). Al cambiar de período se limpian las líneas anteriores: un
  error del período nuevo nunca deja las cifras viejas bajo el selector nuevo.
- Recepción y profesionales que escriben la URL ven la misma explicación de rol
  que Equipo o Equipamiento («Esta sección es de la administración»).

### 22.5 `/panel/mis-comisiones` (S-F3.2-5, D7)

- Solo dueño, administración y profesional; recepción ve la explicación de su rol
  y no la página. Consume **solo** `GET /payroll/me` y muestra las cifras propias
  (ventas, total vendido, comisión) con el rótulo del período, el aviso de
  período en curso, la píldora «estimado» con su explicación y los mismos estados
  de carga/error/sin conexión. Nunca pide, dibuja ni enlaza datos de otra persona
  ni el modelo de pago de nadie.

### 22.6 Menú y navegación móvil

- `NAV` suma «Nómina» (`managerOnly`, entre Equipo y Ajustes) y «Comisiones»
  (`commissionsOnly`: dueño/administración/profesional; recepción no la ve).
- Doce entradas para el dueño en la grilla móvil de cinco columnas: tres filas,
  cada enlace con 44 px de alto y `scrollWidth === clientWidth` a 390 px. El
  profesional ve ocho (sin Nómina, Equipo, Puestos ni Ajustes) y recepción siete.
  **Desviación consciente:** la etiqueta del menú es «Comisiones» y no «Mis
  comisiones» porque la etiqueta larga no entra en una columna de ~69 px a 390 px
  sin desbordar; el `h1` de la página mantiene «Mis comisiones».

### 22.7 Accesibilidad y estilos

`app.css` suma el bloque «F3»: `.payroll-filters` (período + fecha en `.filters`,
rótulo del período en la display), `.payroll-badge` (píldora dorada del
«estimado»), `.payroll-table` (nombre que envuelve, pie con filete fuerte),
`.pay-model*` (tarjetas en una columna en el teléfono y dos desde 760 px, casilla
con 44 px de objetivo) y `.ex-team__pay`. La fila de totales se apila en el
teléfono con el mismo patrón `data-label` de `.table--stack`, sin repetir el
rótulo «Totales». Etiquetas `<label for>` reales en todos los campos, `fieldset`
con `legend` por tarjeta, `aria-label` en la casilla con el nombre de la persona
y el anillo de foco de 3 px de `base.css` (medido al tabular: el campo «Monto
fijo» recibe `outline: 3px solid`). Los campos y selectores nuevos miden 44 px o
más; los únicos controles menores son los `btn--sm` preexistentes de otras
secciones.

### 22.8 Verificación

- `npx tsc --noEmit` sin errores, `npm test` (**374 tests**, `node --test`; 351
  eran de la entrega anterior, 23 nuevas) y `npm run build` en verde.
- TDD en `src/lib` (primero en rojo: `ERR_MODULE_NOT_FOUND` de
  `src/lib/payroll.ts`): `payroll.test.ts` cubre la lectura de los tres payloads
  (campos ausentes, tipos equivocados, negativos, fraccionarios, filas sin id,
  flag de comisión y período por defecto, totales recalculados), el período
  (`kind`/fechas reales, `closed`), las etiquetas en español de las tres formas y
  de una semana que cruza mes y año, la validación de `kind`/`date` con «Período
  inválido», las sumas en enteros (`payrollTotals`, `payrollSalesTotals`), el
  valor del campo de monto, la matriz de roles de D7 (las ocho combinaciones), el
  guardado optimista con vuelta atrás exacta, la copia del modelo y el cliente de
  la API (rutas, cuerpos exactos, query de `/me`, frases del servidor en 400).
- Revisión visual con el bundle de `vite build` servido junto a una API simulada
  en `/tmp/f32-shot` (miembros activos y uno dado de baja, tres modelos de pago,
  vista previa semanal/mensual/vacía, un `500` con frase en español, un `PUT`
  rechazado con monto máximo, `payroll/me` y `/me` por rol) y Chromium headless
  por protocolo DevTools (el flag `--screenshot` se cuelga en este entorno) a
  **390 y 1280 px**, con **20 capturas revisadas una por una**:
  - `/panel/nomina`: cargada con «estimado» y «Período en curso», mes cerrado,
    vacío, error y el estado de error móvil.
  - `/panel/mis-comisiones`: dueño (390/1280) y profesional (390).
  - `/panel/equipo`: editor del modelo de pago (390/1280), error del `PUT` con la
    frase del servidor y el campo devuelto a su valor anterior (390/1280), ex
    equipo desplegado con «Dado de baja» y el modelo en solo lectura (390/1280) y
    foco de teclado en «Monto fijo».
  - Guardas: profesional en `/panel/nomina` y recepción en `/panel/nomina` y
    `/panel/mis-comisiones` (explicación de rol, sin datos), y el menú móvil sin
    «Nómina»/«Comisiones» para recepción. Comprobado en el navegador:
    `scrollWidth === clientWidth` en todas las capturas y en la barra móvil
    (doce enlaces de 44 px), ningún control nuevo por debajo de 44 px y el anillo
    de foco de 3 px al tabular.
- Fuera de alcance de esta entrega: las liquidaciones (F3.3), los ajustes por
  línea y la exportación CSV (D6/D8).

## 23. Fase 6 — F3.3: liquidaciones, ajustes y exportación

Contrato `odd/tasks/fase6-roadmap.md`, «Contrato F3» (D6, D8, D9) y «Formas de
F3.3», que están congeladas. Sin dependencias nuevas y sin tocar `backend/**`: la
UI consume las rutas de ese bloque con sus formas exactas y muestra las frases del
servidor (400/409) verbatim. Entrega de S-F3.3-5; S-F3.3-1..4 son el backend que
se implementa en paralelo.

### 23.1 Lectura defensiva (`src/lib/payrollRuns.ts`)

| payload | regla |
| --- | --- |
| `GET /payroll/runs/:rid` | se descarta la corrida sin `id`, con `kind` fuera de `week`/`fortnight`/`month` o con `from`/`to` que no son días reales (`2026-02-30` rebota por el round-trip de `Date.UTC`); un `status` ausente o desconocido se lee como `paid` (lado seguro: solo lectura); las líneas sin `user_id` se descartan; el nombre vacío cae al id; `active` ausente es `true`; conteos y centavos negativos, fraccionarios o que no son números bajan a `0`; el ajuste conserva su signo; el `total_cents` de una línea o los `totals` inutilizables se recalculan en enteros desde los conceptos (nunca se muestra un total inventado) |
| `GET /payroll/runs` | lee `{runs:[…]}` o una lista suelta, descarta filas sin `id`/`kind`/fechas y ordena por `from` desc (desempate por `created_at`) para que la más nueva quede arriba aunque el orden llegue mal; una fila sin totales confiables muestra ceros, nunca un número adivinado |
| mutaciones | `POST /payroll/runs`, `PATCH …/lines/:uid`, `POST …/pay`: si la respuesta no se puede leer como corrida se lanza un error explícito en vez de un guardado mudo |

### 23.2 Reglas puras (`src/lib/payrollRuns.ts`)

| helper | regla |
| --- | --- |
| `canManagePayrollRuns` | dueño/administración; profesional y recepción no (D7). La lista y el detalle comparten la explicación de rol con Nómina |
| `canEditRunLines` / `canDeleteRun` / `canPayRun` | solo `status === 'open'`; una pagada es inmutable (D6) y la pantalla no ofrece ni edición ni borrado |
| `liquidateBlockReason` | `null` solo con un período terminado (`period.closed`); el período en curso devuelve «El período todavía no terminó: liquidá cuando haya pasado» y sin período «Elegí un período para liquidar» |
| `runStatusLabel` / `runKindLabel` / `runLabel` | «Abierta»/«Pagada», «Semana»/«Quincena»/«Mes» y el rótulo del período en español («Semana del 3 al 9 de noviembre»), reutilizando `payPeriodLabel` para las fechas |
| `lineTotalCents` / `runTotalsFromLines` | total de la línea = comisión + fijo + ajuste con signo; sumas por concepto en centavos enteros, sin coma flotante y sin mutar la entrada |
| `applyRunLineAdjustment` / `restorePayrollRun` | pinta el ajuste y recalcula el total de la línea y los totales de la corrida para la vista optimista, sin mutar la corrida recibida; la vuelta atrás devuelve **exactamente** el objeto guardado por quien llama |
| `parseSignedAmountToCents` / `signedAmountInputFromCents` | ajuste con signo (`-500`, `+12.5`, `$1,234.55`), delegando la parte numérica en `parseAmountToCents`; lo ilegible es `null` y no viaja; el valor del campo se arma con aritmética entera (nunca `cents / 100`) |
| `csvDownloadFilename` / `runCsvFilename` | nombre desde `Content-Disposition` (`filename*` RFC 5987 primero), lista blanca `[A-Za-z0-9._-]`, sin rutas, con `.csv`, tope de 120 caracteres y vuelta al nombre de la corrida (`nomina-<kind>-<from>-<to>.csv`) o a `nomina.csv` |

### 23.3 Bloque «Liquidaciones» en `/panel/nomina` (S-F3.3-5)

- Debajo de la vista previa. Botón «Liquidar este período» habilitado **solo** con
  `period.closed`; con el período en curso queda deshabilitado y el motivo se lee
  al lado (también `aria-describedby`). La creación envía `{kind, date}` del
  período previsualizado y navega al detalle; un 400/409 se muestra verbatim y
  recarga la lista (la corrida rechazada suele dejar la lista vieja).
- Lista de corridas nuevas primero: rótulo del período en español, píldora
  «Abierta» (dorada) o «Pagada» (verde), cantidad de líneas, comisión, fijo,
  ajuste con signo, total y «Ver detalle». Vacío honesto («Todavía no hay
  liquidaciones…»), carga, error con «Reintentar».

### 23.4 Detalle `/panel/nomina/liquidaciones/:rid`

- Misma guarda de rol que el resto de Nómina: recepción y profesionales ven «Esta
  sección es de la administración» y no se pide ninguna corrida.
- Cabecera con estado, líneas y total; botones «Exportar CSV», «Marcar como
  pagada» y «Borrar liquidación» solo con la corrida abierta.
- Tabla con las columnas congeladas (profesional, ventas, vendido, comisión, fijo,
  ajuste, nota, total), las píldoras «estimado» y «Dado de baja», y un pie de
  totales. Cada fila abierta ofrece «Editar ajuste», que despliega un formulario
  con monto con signo y nota; el monto ilegible se rechaza en el cliente con su
  propio mensaje y no viaja. Al guardar se pinta el cambio (línea y totales)
  antes de la respuesta; si el servidor rechaza, la corrida vuelve **idéntica**,
  el formulario conserva lo escrito y la frase del servidor (p. ej. «El ajuste
  dejaría el total en negativo») se lee en `role="alert"`. La respuesta del
  servidor reemplaza la vista optimista.
- «Marcar como pagada» y «Borrar liquidación» usan el bloque de confirmación
  inline de Equipo (`team-confirm`, 44 px): la de pago dice que la liquidación
  queda congelada y que no se podrá editar ni borrar; la de borrado avisa que se
  liberan los días y no se puede deshacer. Pagar deja la corrida en solo lectura
  con el aviso «Esta liquidación está pagada: no se puede editar ni borrar.»;
  borrar vuelve a `/panel/nomina`. El 409 de una pagada se lee verbatim.
- `Content-Disposition` con nombre inseguro o ausente no escribe archivos fuera de
  descargas: el nombre pasa por la lista blanca.

### 23.5 Exportación CSV autenticada (D8)

`Exportar CSV` **no** es un enlace: `fetchPayrollRunCsv` hace
`GET /salons/:id/payroll/runs/:rid/export.csv` con `Authorization: Bearer <token>`
(401 limpia la sesión como el resto de la API), recibe un `Blob`, toma el nombre
de `Content-Disposition` con la lista blanca y `savePayrollRunCsv` lo entrega con
un `<a download>` sobre `URL.createObjectURL`, revocando la URL en el `finally`.
Un fallo de red se lee con el mensaje cortés «No pudimos descargar el CSV. Revisa
tu conexión e intenta de nuevo.»; un error del servidor conserva su frase.

### 23.6 Rutas y navegación

- `App.tsx` suma `nomina/liquidaciones/:rid`; la entrada «Nómina» del menú ya usa
  `end: false`, así que sigue marcada en el detalle. No hay entradas nuevas: el
  detalle se alcanza desde la lista y desde la creación, y el menú móvil conserva
  sus doce entradas de 44 px para el dueño.

### 23.7 Accesibilidad y estilos

`app.css` suma el bloque «F3.3»: `.runs`/`.run-list`/`.run-card` (tarjetas de
corrida), `.run-head*` (estado y acciones), `.run-table*` (persona que envuelve,
nota que envuelve, pie con filete), `.run-line-form` (formulario del ajuste de dos
campos y botones) y la fila del editor como bloque en el teléfono (sin el rótulo
heredado de `.table--stack`). Los totales móviles no repiten «Totales» y los
botones de acción no parten el texto. Etiquetas `<label for>` reales, `role="alert"`
para los errores, `aria-describedby` del botón deshabilitado a su motivo, 44 px o
más en todos los controles nuevos y el anillo de foco de 3 px de `base.css`.

### 23.8 Verificación

- `npx tsc --noEmit` sin errores; `npm test` (**396 tests**, `node --test`; 374
  eran de la entrega anterior y 22 son nuevos de `payrollRuns.test.ts`); `npm run
  build` en verde.
- TDD en `src/lib`: primero en rojo (`ERR_MODULE_NOT_FOUND` de
  `src/lib/payrollRuns.ts`), después en verde los 22 tests: lectura del detalle y
  de la lista (campos ausentes, tipos equivocados, negativos, fraccionarios,
  `kind`/fechas inválidas, estado desconocido, totales recalculados, orden nuevo
  primero), ajuste con signo y valor del campo en enteros, total de línea y sumas,
  guardado optimista con vuelta atrás exacta y persona desconocida que no toca
  nada, compuertas abierta/pagada, motivo del botón de liquidar, matriz de roles
  (cuatro roles y `null`), rótulos en español, nombre seguro del CSV (RFC 5987,
  ruta `../../etc/passwd`, encabezado vacío, `=` inicial, fallback), y el cliente
  de la API (verbos, rutas, cuerpo exacto del `PATCH`, 409 verbatim, descarga con
  `Authorization: Bearer`, nombre del archivo desde `Content-Disposition`, fallback
  y errores de red/servidor).
- Revisión visual con el bundle de `vite build` servido con una API simulada en
  `/tmp/f33-shot` (dueña, lista vacía y profesional) y Chromium headless por
  protocolo DevTools (el flag `--screenshot` se cuelga en este entorno), cada
  invocación bajo `timeout 60`, a **390 y 1280 px**, con **20 capturas revisadas
  una por una**:
  - `/panel/nomina` con las dos corridas («Abierta» y «Pagada»), el botón de
    liquidar deshabilitado con su motivo y la lista vacía (390/1280).
  - Detalle abierto (390/1280), ajuste `-500.00` guardado con su nota, total de la
    línea y de la corrida actualizados (390/1280), y el `400` del servidor con la
    fila devuelta a `-$500.00` y el mensaje bajo el formulario (390/1280).
  - Confirmaciones de «Marcar como pagada» y «Borrar liquidación» (390/1280).
  - Corrida pagada en solo lectura, sin edición ni borrado, con el aviso y el
    export (390/1280).
  - Profesional en `/panel/nomina` y en el detalle: la explicación de rol, sin
    datos y sin «Nómina» en el menú (390/1280).
  - Comprobado en el navegador: `scrollWidth === clientWidth` en las 20 capturas;
    la exportación produce un Blob `text/csv` con el nombre
    `nomina-week-2026-11-03-2026-11-09.csv` y revoca la URL de objeto una vez; y
    «Sí, marcar como pagada» deja la píldora «Pagada», el aviso de congelada y
    desaparecen los botones de editar y borrar.
- Fuera de alcance de esta entrega: el backend de las corridas (S-F3.3-1..4) y la
  exportación de ventas, cierres y clientes (F4).

## 24. Fase 6 — F3b: gastos y ganancia neta (frontend)

Contrato: `odd/tasks/fase6-roadmap.md`, sección «Contrato F3b (gastos y ganancia
neta)». Esta entrega es **solo el frontend**: el backend de las cinco rutas corre
en paralelo, así que la API todavía puede no existir y todo se probó con `fetch`
simulado sobre las formas congeladas.

### 24.1 Rutas, menú y permisos

- `App.tsx` suma `gastos` y `ganancias` dentro de `/panel`; ninguna otra ruta
  cambia. `AppShell.tsx` suma dos entradas `managerOnly`: «Gastos» y «Ganancias»
  (13 entradas en el menú; las cinco columnas de 375/390 px siguen entrando sin
  desborde, medido en el navegador).
- Las dos páginas se guardan con `canManageExpenses` / `canViewProfit` (dueño y
  administración, D5) y muestran `ManagerOnlyNotice` al resto, igual que Nómina.
  El servidor sigue siendo la autoridad: un 403 suyo se explica con el mismo
  mensaje cortés de siempre.

### 24.2 Lógica pura

- `src/lib/expenses.ts`: `parseExpenseAmountToCents` convierte el texto escrito a
  centavos con aritmética entera (acepta `12`, `12,5`, `12.50`, `$1250`; rechaza
  vacío, negativo, `abc`, `1,2,3`, más de dos decimales y separador de miles en
  lugar de adivinar un factor de 1000). `validateExpenseDraft` valida con las
  frases EXACTAS del contrato (monto cero, tope de 2000000000, categoría, fecha
  mal formada, futura contra el día del salón, anterior a 2000-01-01, descripción
  de más de 200 caracteres contados por carácter) y devuelve el campo culpable.
  `expenseCreateBody` deja `description` opcional; `expenseUpdateBody` manda
  SIEMPRE las cuatro claves. `normalizeExpense(s)` lee la respuesta de forma
  defensiva (fila sin id, sin categoría o con fecha imposible se descarta; el
  orden por `spent_on` y `created_at` descendente se recalcula) y
  `checkExpenseRange` / `defaultExpenseRange` cubren el rango de 366 días y los
  últimos 30 días.
- `src/lib/profit.ts`: `normalizeProfit` completa con ceros el día que el payload
  dejó vacío, deriva el neto cuando no es entero, ordena los días ascendente,
  descarta fechas imposibles, deja solo categorías con total > 0 y, si los
  totales no son enteros, los suma desde los días. `profitNetSign`,
  `formatProfitNet` (el signo de la pérdida siempre visible), `profitPeriodLabel`
  (día propio, semana y mes con el rótulo de la nómina), `profitDayBars` /
  `barShare` (altura en porcentaje, nunca dinero) y `profitCategoryRows` (rótulos
  Alquiler, Insumos, Servicios, Otros; un código desconocido se muestra tal cual).
- `src/lib/datetime.ts`: se AGREGA `todayInTimeZone(zone, now)`, el día de hoy en
  la zona IANA del salón vía `Intl` con vuelta a `TIME_ZONE` si la zona no sirve.
  Ninguna función existente cambia.

### 24.3 Pantallas

- `/panel/gastos`: rango Desde/Hasta con atajo «Últimos 30 días», alta y edición
  en el mismo formulario, lista del rango con Editar y Borrar. Borrar pide
  confirmación («¿Borrar este gasto?» + «Se borra del todo y esta acción no se
  puede deshacer.»). **No hay pintado optimista del dinero**: después de cada
  alta, cambio o borrado la lista se vuelve a pedir al servidor, y el 400/404 se
  muestra verbatim bajo el formulario (o en la lista).
- `/panel/ganancias`: selector Día/Semana/Mes con fecha, cuatro tarjetas
  (ingresos, gastos, nómina pagada y ganancia neta), barras por día local y
  desglose de gastos por categoría, más el aviso fijo «Incluye la nómina ya
  pagada; las liquidaciones abiertas y las comisiones sin liquidar no cuentan».
  La ganancia negativa se pinta en rojo y conserva su signo (`-$150.00`), y el
  pie de la tarjeta lo dice con palabras («Pérdida: …»), no solo con color.
- La fecha por defecto del formulario y del reporte sale de `GET
  /salons/:id/settings` (la zona del SALÓN, hoy `America/Bogota` en el mock),
  nunca de la constante `America/Mexico_City`: si la persona elige otra fecha, la
  respuesta tardía de ajustes no la mueve.
- Si el service worker devuelve una respuesta guardada, la pantalla dice «Sin
  conexión: mostrando datos guardados» (misma regla que E-10).

### 24.4 Accesibilidad y estilos

No hay cambios de hoja de estilo en esta entrega: se reutilizan las clases
existentes (`panel-card`, `filters`, `field`, `table--stack`, `kpi`, `rank`,
`notice`, `team-confirm`, `btn`) y la geometría de las barras va en `style`
en línea con los tokens del tema (`--wine`, `--gold`, `--rose-tint`) y el rojo de
`.field__error` para la pérdida. Las barras son CSS puro (sin dependencias), cada
una es enfocable con teclado y lleva su nombre accesible («5 de noviembre:
ingresos $850.00, ganancia neta -$50.00»), y debajo hay una lista con el mismo
detalle para lectores de pantalla. Todos los controles nuevos miden 44 px o más
(el único control de menos de 44 px de la página es la campana del shell, que ya
existía) y `scrollWidth === clientWidth` a 390 y 1280 px.

### 24.5 Verificación

- `npx tsc --noEmit` sin errores; `npm test` (**450 tests**, `node --test`; 396
  eran de la entrega anterior y 54 son nuevos de `expenses.test.ts` y
  `profit.test.ts`); `npm run build` en verde.
- TDD en `src/lib`: primero en rojo (`ERR_MODULE_NOT_FOUND` de `expenses.ts` y
  `profit.ts`, y del export `profitDayBarLabel`), después en verde los 54 tests:
  monto sin float (coma y punto, dos decimales, tope, rechazos, `10.10` = 1010
  centavos), mensajes del contrato verbatim, cuerpo del POST (descripción
  opcional) y del PUT (cuatro claves), orden y descarte de la lista, rango de 366
  días y últimos 30, confirmación que avisa que no se puede deshacer, matriz de
  roles, verbos y rutas del cliente con `fetch` simulado, 400 y 404 verbatim,
  reporte con días en cero, neto derivado con signo, totales recalculados,
  categorías > 0 y ordenadas, barras proporcionales con días sin ingresos,
  rótulos en español, aviso fijo y `todayInTimeZone` con dos zonas y una zona
  inválida.
- Revisión visual con el bundle de `vite build` servido con una API simulada en
  `/tmp/f3bf` y Chromium headless por protocolo DevTools, a **390 y 1280 px**:
  Gastos (dueña) con el formulario y la lista de cuatro gastos, la confirmación
  de borrado, Ganancias con la semana del mock (neto negativo en rojo, días en
  cero, desglose por categoría) y las dos páginas con rol profesional (aviso de
  administración y sin entradas en el menú). En las siete capturas
  `scrollWidth === clientWidth`; el detalle de 44 px y el corte de texto del menú
  se midieron en el navegador.
- Fuera de alcance de esta entrega: el backend de gastos y ganancia (S-F3b
  completo), gastos recurrentes, adjuntos, categorías personalizadas, exportar a
  CSV y comparar con el período anterior.

## 25. Fase 6 — F1: link de un solo uso para cancelar o reprogramar (frontend)

Contrato: `odd/tasks/fase6-roadmap.md`, sección «Contrato F1 (link firmado de un
solo uso para cancelar o reprogramar desde el email)», con las formas CONGELADAS
de los cuatro `POST` públicos. Esta entrega es **solo el frontend**: el backend
corre en paralelo sobre `backend/**` y la API todavía puede no existir, así que
todo se probó con `fetch` simulado sobre esas formas y, después, en el navegador
con una API simulada. Sin dependencias nuevas y sin tocar ningún archivo fuera de
las superficies asignadas.

### 25.1 Ruta y privacidad del token

- `App.tsx` suma **una** ruta pública, `/turno`, fuera de `RequireAuth` y sin
  entrada de menú (nadie llega ahí navegando: llega desde el email). Ninguna otra
  ruta ni el `UnauthorizedRedirect` cambian (las cuatro rutas del contrato son
  anónimas y mandan `auth: 'none'`, así que nunca devuelven 401).
- El token viaja en el FRAGMENTO (`/turno#<token>`), igual que `/invitacion`
  (F2g1): `takeBookingLinkToken` lee `location.hash` en el primer render de
  `BookingLinkPage` y lo borra con `history.replaceState` **antes de cualquier
  pedido**, y lo deja solo en un `ref` del componente. Nunca va a
  `localStorage`, `sessionStorage`, cookies, consola, mensajes de error, query
  string ni path: solo en el CUERPO de los cuatro `POST`.
- Solo el PRIMER fragmento es el slot del link: un token en otro fragmento se
  borra y se IGNORA (nunca se usa), y un fragmento sin token se descarta entero
  para no dejar rastro.

### 25.2 Reglas puras (`src/lib/bookingLinks.ts`)

| helper | regla |
| --- | --- |
| `isBookingLinkToken` | exactamente 43 caracteres base64url (`crypto/rand` de 32 bytes, D1): nada de padding, espacios ni `%`-encoding |
| `readBookingLinkHash` / `takeBookingLinkToken` | token del primer fragmento + URL limpia; un fragmento inservible deja la dirección sin rastro. `null` = link inválido y **ningún pedido** |
| `normalizeLinkBooking` | lee las ocho claves del contrato (sin teléfono, email ni ids: D6); `can_change` solo es `true` si el servidor lo dice; `min_notice_hours` cae a la constante 2 |
| `salonTimeZone` | la zona IANA que viene con el turno; si falta o no existe, la del VISITANTE. Nunca `America/Mexico_City` |
| `normalizeReschedule` | `{booking, new_link_sent}`; el token nuevo no viene en la respuesta y la UI no lo espera |
| `formatLinkDay` / `formatLinkWhen` / `formatLinkZone` | día largo y rango de horas en la zona del salón (`sábado, 14 de marzo de 2026, 11:00–13:00`), con la zona impresa al lado |
| `defaultPickDate` | hoy en la zona del salón (el selector arranca en el día del turno, que ya es futuro) |
| `bookingLinkErrorMessage` | 404 → **la única** frase genérica de la página; 409/400/429 → la frase del servidor verbatim |
| `canCancel` / `canReschedule` | la acción está disponible solo con `can_change` y sin pedido en curso; reprogramar exige además un horario elegido (doble clic) |
| `initialLinkChange` / `reduceLinkChange` / `isLinkTerminal` | el flujo `view → slots/confirm_cancel → done_*`. `cancelled`, `booking` y `newLinkSent` se escriben SOLO con el evento de éxito: no hay estado optimista, y un `failed` conserva el paso y el error (la página sigue usable) |
| `cutOffNotice`, `cancelConfirmQuestion`, `rescheduleConfirmQuestion`, `newLinkNotice`, `CANCEL_DONE`, `RESCHEDULE_DONE` | textos en español del contrato, incluida la confirmación que dice que no se puede deshacer y «Te mandamos un email con el nuevo enlace» |

Los horarios reutilizan `normalizeAvailability`, `formatSlotTime`/`formatSlotRange`
y `groupSlotsByTime` de `publicBooking.ts` (misma forma de ítem que la
disponibilidad pública; ese archivo no se toca).

### 25.3 `/turno` (pública)

- Estados: «Revisando tu turno» (lookup), el turno con salón, servicio,
  profesional, día y rango en la zona del salón y la zona impresa, y las dos
  acciones («Cambiar horario» / «Cancelar turno»).
- `can_change:false`: el turno se muestra igual y aparece el aviso «Faltan menos
  de 2 horas para el turno: comunicate con el salón para cambiarlo»; las dos
  acciones NO se renderizan.
- Link muerto (sin token, fragmento inválido, 404, turno cancelado/atendido o
  fuera de la ventana): «Este enlace ya se usó o el turno ya no está vigente»,
  sin detalle, sin botones y con la instrucción de pedir un enlace nuevo. Un
  fallo de conexión es el único estado reintentable («Reintentar»).
- «Cancelar turno» pide confirmación («¿Cancelar tu turno del …?» + «No se puede
  deshacer: …») y solo muestra «Tu turno quedó cancelado» cuando el `POST`
  respondió 200. «Cambiar horario» abre un selector de UN día (`<input
  type="date">`, `date` = `YYYY-MM-DD` de la zona del salón, mínimo hoy en esa
  zona) con un botón por horario y «Confirmar cambio» deshabilitado hasta elegir
  uno; al confirmar muestra el horario NUEVO que devolvió la API en la zona del
  salón y, si `new_link_sent`, «Te mandamos un email con el nuevo enlace» (si es
  `false`, dice que el enlace hay que pedírselo al salón).
- Un 409 se muestra verbatim y NO consume nada: tras «Ese horario ya no está
  disponible: elegí otro» la lista se vuelve a pedir y se puede elegir otro
  horario; tras el 409 de la ventana de 2 h el mensaje queda a la vista. Los dos
  éxitos son terminales (el token se consumió: el nuevo solo llega por email).
- Ningún pedido se dispara dos veces: además de `disabled` mientras hay una
  request en vuelo, un `ref` sincrónico corta el segundo clic del mismo tick.

### 25.4 Accesibilidad y estilos

No hay cambios de hoja de estilo (los CSS no estaban en las superficies
asignadas): se reutilizan `.invite`/`.invite__card`/`.invite__actions`,
`.booking__summary`, `.booking__slots`, `.slot-grid`, `.slot-btn`,
`.booking__slots-empty`, `.notice--*`, `.field`, `.input` y `.btn--wine`/
`.btn--quiet`. Todos los controles miden 44 px o más (botones 47, horarios 48,
input de fecha 46), el foco es el anillo de 3 px de `base.css`, hay `<label for>`
real, `role="alert"` en los errores, `role="status"` en avisos y resultados, el
resultado se enfoca (`tabIndex={-1}`) y la primera hora libre recibe el foco al
cargar la lista. Sin desborde horizontal: `scrollWidth === clientWidth` a 390 y
1280 px en los siete estados.

### 25.5 Verificación

- `npx tsc --noEmit` sin errores; `npm test` (**482 tests**, `node --test`; 450
  eran de la entrega anterior y 32 son nuevos de `bookingLinks.test.ts`);
  `npm run build` en verde.
- TDD en `src/lib`: primero en rojo (`ERR_MODULE_NOT_FOUND` de
  `src/lib/bookingLinks.ts`), después en verde los 32 tests: token del fragmento
  (43 caracteres, solo el primer `#`, formas malformadas, fragmentos extra), el
  barrido de la dirección ANTES del primer pedido (orden `replace` → `fetch`
  verificado en un `history` falso), un fragmento inválido sin ningún pedido,
  cuerpo exacto de los cuatro `POST` (`{token}`, `{token,date}`,
  `{token,starts_at}`) sin `Authorization`, token ausente de la URL y de
  `localStorage`/`sessionStorage`/cookie (stubs que registran escrituras),
  lectura defensiva del lookup y del reschedule, zona del salón (y respaldo a la
  del visitante) sin la constante fija, fecha del selector en la zona del salón,
  frases del contrato, 404 genérico contra 409/400 verbatim, acciones ocultas con
  `can_change:false`, botones deshabilitados con una request en vuelo,
  confirmación de cancelar que dice que no se deshace, resultado con la hora
  nueva y el aviso del email, y el flujo sin estado optimista (un 409 conserva el
  paso y el error, un éxito es terminal).
- Revisión visual con el bundle de `vite build` y una API simulada en `/tmp/f1f`
  (lookup válido, `can_change:false`, 404, `slots` de un día, `reschedule` 409 y
  200, `cancel` 200) y Chromium headless por protocolo DevTools a **390 y
  1280 px**, 20 capturas (9 estados × 2 anchos + 2 del recorrido de teclado)
  revisadas una por una; el token malformado produce exactamente la misma
  pantalla que el 404 (dos archivos idénticos byte a byte), así que sus capturas
  repiten un layout ya mirado: turno válido, confirmación de cancelar, resultado
  cancelado, selector de horarios, 409 con la lista recargada, resultado
  reprogramado, `can_change:false`, link inválido y el recorrido de teclado.
  Comprobado en el navegador:
  - `location.hash` vacío y la URL sin token tras cargar `/turno#<token>`; el
    token no aparece en el DOM, ni en `performance.getEntriesByType`, ni en
    `localStorage`/`sessionStorage`/`document.cookie`; las 20 requests del
    recorrido no llevan token ni `?` en la URL y sus cuerpos tienen exactamente
    `token`, `date+token` o `starts_at+token`.
  - Un token malformado y un 404 no disparan **ningún** pedido de `slots`; el 404
    muestra la frase genérica sin botones y con «Ir al sitio del salón».
  - Dos clics en el mismo tick sobre «Sí, cancelar el turno» dejan **una** sola
    request de `cancel` (el botón además se ve deshabilitado con «Cancelando…»).
  - Teclado: `Tab` llega a «Cambiar horario» y «Cancelar turno» (anillo sólido de
    3 px), `Enter` real abre el selector, la primera hora libre queda enfocada,
    `Espacio` la selecciona (`aria-pressed=true`) y habilita «Confirmar cambio»;
    `Tab` sigue por los horarios, «Confirmar cambio» y «Volver».
- **Desviaciones conscientes:**
  - El runner del frontend es `node --test src/**/*.test.ts` y no hay entorno
    DOM, así que no se agregan tests de componente: las reglas de la página
    (orden barrido→pedido, botones deshabilitados, ausencia de estado optimista)
    viven en funciones puras de `bookingLinks.ts` con tests propios, y lo que es
    DOM (foco, 44 px, desborde, doble clic real) se verificó en el navegador.
  - La frase del 404 de la PÁGINA no es la del servidor: el contrato pide «Este
    enlace ya se usó o el turno ya no está vigente» para el visitante, aunque la
    API responda «Enlace inválido o vencido».
  - El selector abre en el día del turno actual (no en hoy): es el día que la
    persona conoce, y el mínimo del input ya impide fechas pasadas en la zona del
    salón.
  - No se agrega `<meta name="robots" content="noindex">`: ninguna página pública
    existente lo hace y `index.html` está fuera de esta entrega.

## 26. Fase 6 — F4: exportar ventas, cierres de caja y clientes en CSV (frontend)

Contrato: `odd/tasks/fase6-roadmap.md`, sección «Contrato F4 (exportar CSV de
ventas, cierres de caja y clientes)». Esta entrega es **solo el frontend**: el
backend de las tres rutas corre en paralelo, así que la API todavía puede no
existir y todo se probó con `fetch` simulado sobre las formas congeladas.

### 26.1 Ruta, menú y permisos

- `App.tsx` suma `exportar` dentro de `/panel`; ninguna otra ruta cambia.
  `AppShell.tsx` suma UNA entrada `managerOnly`, «Exportar» (14 entradas en el
  menú; las cinco columnas de 375/390 px siguen entrando sin desborde, medida en
  el navegador).
- La página se guarda con `canExportCsv` (dueño y administración, D1) y muestra
  `ManagerOnlyNotice` al resto, igual que Nómina, Gastos y Ganancias. Recepción y
  profesional no ven la entrada del menú ni un solo botón de descarga. El
  servidor sigue siendo la autoridad: su 403 se muestra **verbatim**.

### 26.2 Lógica pura (`src/lib/exports.ts`)

- Sin código de descarga nuevo: `fetchExportCsv` hace el `fetch` con `Bearer` y
  `Accept: text/csv` (mismo patrón que `fetchPayrollRunCsv`, que no se toca),
  limpia el token y avisa al shell en un 401 (igual que la nómina), y el nombre
  del archivo sale de `Content-Disposition` con `csvDownloadFilename` (el helper
  de la nómina, importado). `downloadExportCsv` delega en `savePayrollRunCsv`, que
  crea el object URL, hace clic en el ancla y lo revoca.
- **El CSV no se parsea nunca**: `ExportCsvFile` solo lleva `{kind, blob,
  filename}`; el cuerpo viaja como Blob hasta la descarga y ninguna fila se
  convierte en estado, tabla ni texto de la pantalla.
- Las tres URLs son las del contrato y se arman en un solo lugar:
  `/api/v1/salons/:id/export/sales.csv?from=&to=`,
  `/export/cash-closes.csv?from=&to=` y `/export/clients.csv` **sin query**.
- `checkExportRange` reutiliza la regla y la frase del rango de gastos (mismo
  contrato, `Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días`);
  un rango inválido se rechaza ANTES de cualquier pedido, y el botón queda
  deshabilitado con la frase bajo las fechas. `defaultExportRange` son los
  últimos 30 días terminando hoy; `loadExportContext` lee `GET
  /salons/:id/settings` y calcula ese «hoy» en la zona del SALÓN (`America/Bogota`
  en el mock, no la constante `America/Mexico_City`), con la zona conocida como
  respaldo si la lectura falla.
- `createExportGate` / `runExport`: la guarda de un solo archivo a la vez es
  SÍNCRONA (el estado de React solo pinta el botón deshabilitado), así que un
  doble clic en el mismo tick manda **una** request; el resultado de cada pulsación
  es `done` (nombre guardado), `failed` (frase del servidor verbatim) o `busy`, y
  la guarda se libera siempre, también después de un 400 o un 403.

### 26.3 Pantalla `/panel/exportar`

Cabecera «Exportar datos del salón», una tarjeta de rango (Desde/Hasta, atajo
«Últimos 30 días», la pista con la zona del salón o la frase de error) y TRES
tarjetas de descarga: «Ventas», «Cierres de caja» y «Clientes». Las dos primeras
usan el rango y comparten sus botones; la de clientes no lleva rango y muestra el
aviso del contrato: «Incluye teléfonos, emails y notas de tus clientas, que
pueden contener datos de salud. Guardá el archivo con cuidado.». Cada botón dice
«Descargando…» mientras corre, y al terminar la pantalla avisa «Guardado como
<nombre del archivo>» con el nombre saneado que vino en `Content-Disposition`.
Un 400 o un 403 se muestran en un `notice--error` **verbatim** y los botones
vuelven a estar disponibles.

### 26.4 Accesibilidad y estilos

No hay cambios de hoja de estilo: se reutilizan `panel-card`, `filters`, `field`,
`field__hint`, `field__error`, `input`, `btn`, `notice`, `empty-state` y
`page-head`. Los botones miden 47 px y los campos 46 px (≥ 44) y
`scrollWidth === clientWidth` a 390 y 1280 px. Todo es `button`/`input` nativo, así
que el recorrido con `Tab` llega a «Exportar» y a los tres botones.

### 26.5 Verificación

- `npx tsc --noEmit` sin errores; `npm test` (**505 tests**, `node --test`; 482
  eran de la entrega anterior y 23 son nuevos de `exports.test.ts`);
  `npm run build` en verde.
- TDD en `src/lib`: primero en rojo (`ERR_MODULE_NOT_FOUND` de `exports.ts`),
  después en verde los 23 tests: las tres URLs exactas con y sin query, `Bearer` +
  `Accept: text/csv` (y sin `Authorization` sin sesión), nombre desde
  `Content-Disposition` saneado (`../../etc/passwd` → `etc-passwd.csv`), los
  respaldos `ventas.csv`/`cierres.csv`/`clientes.csv`, el Blob intacto (BOM
  incluido) y solo `{kind, blob, filename}`, el object URL creado y revocado, 401
  que limpia el token, 400/403 verbatim, un doble clic = una request, los botones
  usables después de un 400 y de un 403, el rango inválido sin ningún pedido, el
  rango por defecto de 30 días y el «hoy» en la zona leída de `/settings` (dos
  zonas al mismo instante), la frase del rango, el aviso de privacidad exacto y la
  matriz de roles.
- Revisión visual con el bundle de `vite build` y una API simulada en `/tmp/f4f`
  (dueño con `timezone: America/Bogota`, ventas/cierres/clientes con
  `Content-Disposition`, un 400 de tope de filas, un 403 y una sesión de
  recepción) y Chromium headless por protocolo DevTools a **390 y 1280 px**, 11
  capturas revisadas una por una: estado por defecto, rango inválido, ventas y
  clientes descargados, 400 del servidor (1280 y 390), 403 verbatim y recepción
  (aviso de administración y menú sin «Exportar»). Comprobado en el navegador:
  - Los dos archivos llegaron a la carpeta de descargas con el nombre del
    servidor (`ventas-2026-10-25_2026-11-23.csv`, `clientes-2026-11-10.csv`), BOM
    y CRLF intactos, y **ninguna** fila del CSV aparece en la pantalla.
  - Rango por defecto `2026-10-25 … 2026-11-23` con «hoy en el salón es
    2026-11-23» en la zona del mock (Bogotá), no en la fija.
  - Rango inválido: la frase del contrato bajo las fechas y los dos botones de
    rango deshabilitados; el de clientes sigue activo (no depende del rango).
  - 400 y 403 verbatim, y después del error los tres botones vuelven a estar
    habilitados.
  - `Tab` recorre el menú (incluida «Exportar») y los tres botones.
- **Desviaciones conscientes:**
  - El runner es `node --test src/**/*.test.ts` y no hay entorno DOM, así que no
    se agregan tests de componente: las reglas de la página (guarda de un pedido,
    rango validado antes de pedir, frases verbatim, nombre guardado) viven en
    funciones puras de `exports.ts` con tests propios, y lo que es DOM (44 px,
    desborde, descarga real) se verificó en el navegador.
  - El contrato pide «tres tarjetas» y esta pantalla tiene cuatro bloques: la
    tarjeta de rango es compartida por ventas y cierres (los dos exports llevan
    las mismas dos fechas), y así no se duplican los campos ni sus ids.
  - `ManagerOnlyNotice` conserva su texto actual («no puede ver la nómina ni las
    comisiones del equipo») también en Exportar: es el hallazgo H20(g) del
    roadmap y el componente queda fuera de las superficies de esta entrega.

## 27. Fase 6 — F5: ficha de la clienta con historial, gasto y alergias (frontend)

Contrato: `odd/tasks/fase6-roadmap.md`, sección «Contrato F5 (historial de la
clienta: visitas, gasto, última visita, alergias)». Esta entrega es **solo el
frontend**: el backend corre en paralelo (`bookings.client_id`,
`sales.client_id`, migración 028, `GET`/`PUT` de la ficha y
`GET /clients/:cid/history`), así que la API todavía puede no existir y todo se
probó con `fetch` simulado sobre las formas congeladas.

### 27.1 Lógica pura

- `src/lib/clientRecord.ts` — la ficha (`client` de D5): `normalizeClientRecord`
  (con `allergies`, `""` cuando la API no lo manda), `clientRecordBody` (las
  **cinco** claves `name, phone, email, notes, allergies` siempre, recortadas y
  con el correo en minúsculas), `validateClientRecordDraft` (nombre obligatorio,
  largos 120/40/2000/500 contados por CARÁCTER y no por byte, `Email inválido`,
  NUL en cualquiera de los cinco campos), `fetchClientRecord` /
  `updateClientRecord` y `clientApiMessage` (la frase del servidor **verbatim**,
  también el 403 `sin permiso` y el 404 `Ficha no encontrada`: F5 los congela por
  estado, por eso no pasa por `describeApiError`, que reemplaza el 403).
- `createClientSaveGate` / `saveClientRecord`: la guarda de una escritura a la vez
  es SÍNCRONA (el estado de React solo pinta el botón deshabilitado), así que un
  doble clic en el mismo tick manda **un** `PUT`; la segunda pulsación responde
  `busy`. `saved` NO se pinta: la página vuelve a pedir la ficha (y el historial)
  al servidor, así que la pantalla nunca muestra un dato sin guardar.
- `src/lib/clientHistory.ts` — el historial (D6/D7): `normalizeClientHistory`
  lee la forma congelada, PINEA el orden más reciente primero, descarta filas sin
  id o con fecha ilegible y convierte un `total_cents` con decimales en «sin
  dinero» (nunca se pinta un monto inventado). `clientHistoryMoney` devuelve
  `null` cuando `spend` es `null` — un profesional no puede pintar gasto ni
  ventas aunque la API mandara la lista por error. `summaryCards` arma las cuatro
  tarjetas (visitas, no-shows, próxima y última visita); la última es el día
  `YYYY-MM-DD` **tal como vino** y la próxima se lee en la zona del salón que
  trae el propio historial (`2026-11-25T16:00:00Z` → `2026-11-25 10:00` en
  America/Mexico_City). `formatCents` es el único formateador de dinero.
- `src/lib/permissions.ts` (aditivo): `canEditClient` delega en `canCreateClient`
  (D5: `puedeGestionarClientes`) y `canSeeClientMoney` en `puedeVerPlata`; ningún
  helper existente cambia.
- `src/lib/posCart.ts` (aditivo): `saleWithClient(body, picked)` — sin ficha
  elegida devuelve el MISMO objeto (la clave `client_id` no existe, no es
  `undefined`) y con ficha completa el nombre si el campo está vacío y agrega
  `client_id` (D3c). El cuerpo sigue viajando igual por la cola offline.

### 27.2 Pantalla `/panel/clientes/:id`

- La ficha sale de `GET /salons/:id/clients/:cid` (ya no busca en el listado) y
  las fotos de su endpoint de siempre. El historial es un pedido aparte: si falla,
  la ficha se sigue viendo y el error se muestra solo en su sección.
- Bloque «Alergias» destacado ARRIBA de los datos, con `notice notice--error` y la
  etiqueta «Dato de salud», para todos los roles; cuando la ficha no tiene
  alergias el bloque no se renderiza y la fila de datos dice «Sin alergias
  registradas.».
- «Historial»: cuatro tarjetas (visitas, no asistió, próxima, última), la lista de
  turnos recientes (estado con `statusLabel`) y, **solo si `spend` no es null**,
  la tarjeta de gasto y las ventas vinculadas con `formatCents`. Para el resto, en
  lugar de un monto, la frase «Tu rol ve las visitas y los turnos, no los importes
  de las ventas.». Sin turnos vinculados se muestra el estado vacío con el texto
  EXACTO del contrato.
- Formulario «Editar ficha» solo para owner/admin/recepción (un profesional no
  tiene ningún campo de edición): cinco campos, `PhoneInput` del alta, errores en
  el campo con las frases de la API, y tras guardar se vuelve a pedir todo al
  servidor. Las tablas usan `table--stack`, así que a 390 px cada fila es una
  tarjeta y no hay scroll lateral.

### 27.3 POS: selector OPCIONAL de clienta

- `POSPanel` gana tres props opcionales (`clients`, `clientsError`,
  `clientsLoading`) y `PosPage` pide `GET /salons/:id/clients` en su propio
  pedido: si falla, se avisa junto al selector y el catálogo, las ventas y el
  cobro con nombre a mano siguen funcionando.
- `ClientPicker`: buscador que filtra la lista **ya descargada** (por nombre o por
  dígitos del teléfono), 8 filas visibles con el resto acotado por la búsqueda,
  botones de 44 px con `aria-pressed`, «Quitar» para desvincular. Elegir una ficha
  completa el nombre del ticket y agrega `client_id`; sin ficha, el cuerpo es el
  de siempre. Verificado en el navegador: el `POST /sales` con ficha lleva
  `{"…","client_name":"Ana Pérez","client_id":"c1"}` y sin ficha NO tiene la clave.

### 27.4 Pruebas

- TDD en `src/lib` con `node --test` y `fetch` simulado: primero en rojo (los dos
  módulos nuevos no existían), después en verde. 33 tests nuevos en
  `clientRecord.test.ts` (18) y `clientHistory.test.ts` (15): la URL exacta del
  `GET` de la ficha y del historial, el `PUT` con las cinco claves en orden y sin
  sobrantes, el `POST /sales` con y sin `client_id` (cuerpo exacto), 400/403/404
  verbatim, la validación por campo (incluidos NUL y 500/501 de alergias), la
  matriz de roles de edición y de dinero, el doble clic = un `PUT`, las tarjetas
  del resumen, la última visita tal como vino y la próxima en dos zonas, el estado
  vacío exacto, el dinero que desaparece con `spend: null` o con centavos no
  enteros, y el buscador del POS que no vuelve a pedir la lista.
- `npx tsc --noEmit` limpio, `npm test` 538/538 (505 previos sin tocar) y
  `npm run build` correcto.
- Revisión visual con el bundle de `vite build` y una API simulada en `/tmp/f5f`
  (Chromium del caché de Playwright por protocolo DevTools) a **1280 y 390 px**:
  dueña con historial, gasto y alergias; profesional sin dinero ni formulario;
  ficha sin alergias y con historial vacío; POS con el selector y con una ficha
  elegida. `document.documentElement.scrollWidth === innerWidth` en las cinco
  pantallas medidas (sin desborde horizontal). Guardado verificado en el
  navegador: `PUT` con las cinco claves y, después, `GET` de la ficha, de las
  fotos y del historial (sin estado optimista).
- **Desviaciones conscientes:**
  - El runner no tiene entorno DOM, así que no hay tests de componente: las
    reglas de la pantalla viven en funciones puras con tests y lo que es DOM
    (44 px, desborde, teclado, guardado real) se verificó en el navegador.
  - Las cuatro tarjetas del resumen usan el estilo `.kpi` pero el valor en tamaño
    de texto normal: `.kpi__value` (2.4 rem) no entra en las cuatro columnas de
    una sección a ancho completo con una fecha `AAAA-MM-DD` y no hay CSS propio
    en las superficies de esta entrega. La tarjeta de gasto sí usa `KPICard`.
  - La página ya no usa el grid `.client-detail` (dos columnas, 22 rem a la
    izquierda): con las secciones nuevas la columna angosta cortaba las tablas y
    las tarjetas. Ahora las secciones se apilan a ancho completo, como Gastos y
    Ganancias.
  - `describeApiError` sigue reemplazando el 403 en las CARGA de la página
    (convención del panel); las escrituras de la ficha usan `clientApiMessage`
    porque el contrato F5 congela el 403 `sin permiso`.

## 28. Fase 6 — F6: productos e inventario en el POS (frontend)

Cierre del frente de «Contrato F6 (productos e inventario en el POS: stock y
alerta de stock bajo)». Sin dependencias nuevas y sin CSS nuevo: la pantalla y
el POS reusan `panel-card`, `table table--stack`, `service-form`,
`page-head__actions`, `pos-services`/`pos-service`, `notice`, `pill` y
`service-count`. El backend de F6 corre en paralelo, así que todo se probó con
`fetch` simulado y con una API simulada en el navegador.

### 28.1 Rutas, menú y permisos

- Ruta nueva `/panel/inventario` (`App.tsx`) con la entrada «Inventario» en el
  menú (`shell__nav`): visible para owner, admin y recepción; el profesional NO
  ve la entrada y, si entra por URL, ve `ManagerOnlyNotice`. La regla vive en
  `lib/permissions.ts` (`canViewInventory`, `canManageInventory`) y la aplica el
  servidor: `GET /products` responde 403 al profesional.
- Recepción ve el listado con precio, stock y la marca «Stock bajo», sin un solo
  control de escritura: ni alta, ni edición, ni ajuste, ni movimientos (D7).

### 28.2 Lógica pura (`src/lib/products.ts`, `src/lib/productCart.ts`)

- `products.ts`: precio tipado → centavos con el MISMO parser entero de los
  gastos (`parseExpenseAmountToCents`), así que `12`, `12,5` y `12.50` dan 1200,
  1250 y 1250 y nunca pasa por un float; stock y umbral como unidades enteras;
  validación del formulario con las frases del contrato verbatim (nombre
  obligatorio, 120 caracteres, precio 0..10000000, stock y umbral 0..1000000,
  NUL, «Faltan datos del producto…»); `productUpdateBody` manda las CUATRO claves
  del `PUT` y NUNCA `stock`; `validateAdjustDraft` con el signo por motivo
  (`restock` > 0, `loss` < 0, `correction` cualquiera), cero, ±1000000 y nota de
  200; `lowStockMark` marca solo con el `low` que devuelve el servidor;
  `insufficientStockMessage`/`isInsufficientStockMessage` para el 409 de stock; y
  el cliente de las cinco rutas (URLs y cuerpos congelados).
- `productCart.ts`: carrito de productos con clave `product_id`, cantidad entera
  acotada por el stock conocido y por 1000 (`addProduct` recorta en el tope en
  vez de armar una venta que el servidor rechazaría), totales en centavos y
  `saleWithProducts`, que agrega `products: [{product_id, qty}]` SOLO cuando hay
  líneas de producto (sin precio) y deja el cuerpo idéntico cuando no las hay.
- `createInventoryGate` + `writeProduct`: un `guardado` a la vez (el doble clic
  no manda dos `POST/PUT`), la frase del servidor llega tal cual y el resultado
  no se pinta como verdad: la lista se vuelve a pedir.

### 28.3 Pantalla `/panel/inventario`

- Listado por nombre con precio (`formatCents`), stock, umbral («Sin alerta»
  cuando es 0), estado y la marca «Stock bajo» del campo `low`. Un producto no se
  borra: se desactiva.
- Owner/admin: «Nuevo producto» (`ProductForm`: nombre, precio, stock inicial
  opcional, umbral y, al editar, el interruptor de activo), «Ajustar stock»
  (`ProductAdjustForm`: motivo, unidades con signo y nota) y «Movimientos»
  (`ProductMovements`: motivo, cambio con signo, nota, venta y fecha en la zona
  del salón vía `formatHistoryInstant`, con `limit` 20/50/100).
- Sin stock optimista: después de CADA escritura (aceptada o rechazada) se pide
  `GET /products` de nuevo; verificado en el navegador: el `POST /adjust` va
  seguido inmediatamente del `GET` y el listado muestra lo que devolvió el
  servidor, nunca una suma local.

### 28.4 POS: sección «Productos»

- `PosPage` pide `GET /products` en su propio pedido: si falla, se avisa en la
  sección y el catálogo, las ventas y el cobro siguen funcionando. La sección
  lista SOLO los activos con su stock («N en stock» / «Sin stock»); el botón se
  apaga al llegar al stock conocido.
- `POSPanel` gana props OPCIONALES (`productCart`, `products`,
  `onProductCartChange`, `onStockConflict`): las líneas de producto se editan en
  el mismo carrito (44 px), el total de la interfaz suma servicios + productos y
  el `POST /sales` lleva `products: [{product_id, qty}]` sin precios. Verificado
  en el navegador: una venta solo de servicios manda exactamente el cuerpo de
  hoy (sin la clave `products`) y una mixta agrega `products` sin
  `unit_price_cents`.
- El 409 `No hay stock suficiente de «…»` se muestra verbatim, el carrito queda
  intacto, se recarga el catálogo y aparece «Reintentar la venta». El doble clic
  en «Cobrar» está frenado por un guardián síncrono, no solo por el estado de
  React.

### 28.5 Cola offline (sin cambios)

La cola no se toca: un 4xx definitivo se descarta como `server_rejected` y el
POS sigue mostrando «Esta venta no se registró.» con el motivo del servidor. Una
venta de productos encolada sin conexión y rechazada después por falta de stock
termina con la frase existente más la del contrato; el cuerpo encolado lleva las
líneas de producto sin precios, igual que el confirmado. La fila «pendiente de
sincronizar» suma las unidades de producto y avisa «+ productos»: el precio del
producto lo decide el servidor y el dispositivo no lo guarda.

### 28.6 Pruebas

- TDD con `node --test` y `fetch` simulado: primero en rojo (los dos módulos no
  existían), después en verde. 54 tests nuevos — `products.test.ts` (41) y
  `productCart.test.ts` (13): las cinco URLs y sus cuerpos exactos, el `PUT` con
  las cuatro claves y sin `stock`, el ajuste con signo y nota, el `limit` del
  listado, los cuerpos de la venta (solo servicios, mixta y solo productos) sin
  precios, el recorte por stock y por 1000, los 400/403/404/409 verbatim, el
  doble clic en alta/edición/ajuste (un solo pedido), la frase del 409 de stock y
  el descarte de la cola, la marca «Stock bajo» nacida del `low` del servidor y
  la matriz de roles de inventario.
- `npx tsc --noEmit` limpio, `npm test` 592/592 (538 previos sin tocar) y
  `npm run build` correcto.
- Revisión visual con el bundle de `vite build`, `vite preview` y una API
  simulada en `/tmp/f6f` (Chromium del caché de Playwright por protocolo
  DevTools) a **1280 y 390 px**: dueña con dos productos en stock bajo, los
  movimientos, el formulario de ajuste, recepción sin controles, profesional con
  `ManagerOnlyNotice`, POS con la sección «Productos» y el 409 con el carrito
  intacto. `document.documentElement.scrollWidth === innerWidth` en las cinco
  pantallas medidas (sin desborde horizontal).
- **Desviaciones conscientes:**
  - El runner no tiene entorno DOM: las reglas viven en funciones puras con
    tests y lo que es DOM (44 px, desborde, teclado, guardado real) se verificó
    en el navegador. El guardián del doble clic sí está probado como función.
  - El aviso para el profesional es `ManagerOnlyNotice` (como pide el contrato),
    pero su texto compartido habla de «la nómina ni las comisiones del equipo».
    Copiarlo bien exige tocar `components/ManagerOnlyNotice.tsx`, que quedó fuera
    de las superficies de esta entrega: queda anotado para el dueño del archivo.
  - La fila deshabilitada de un producto sin stock usa `opacity: .55` en línea,
    el mismo valor que `.btn:disabled`, porque no hubo CSS nuevo disponible.

## 29. Fase 6 — F7: lista de espera (frontend)

Cierre del frente de «Contrato F7 (lista de espera: aviso por email cuando se
libera un hueco)»: las formas congeladas de D4 (unión pública), D5 (baja por
enlace) y D8 (lista del personal) y el frontend de D9. Sin dependencias nuevas y
sin CSS nuevo: las tres pantallas reusan `panel-card`, `table table--stack`,
`team-confirm`, `empty-state`, `notice`, `pill`, `field`, `invite`/`invite__card`,
`booking__step`/`booking__fields` y `btn`. El backend de F7 corre en paralelo, así
que todo se probó con `fetch` simulado y con una API simulada en el navegador.
No hay dinero en la función: ningún módulo, pantalla ni prueba de esta entrega
toca centavos ni un flotante.

### 29.1 Rutas, menú y permisos

- Rutas nuevas en `App.tsx`: `/lista-de-espera` (pública, sin sesión, al lado de
  `/turno`, sin entrada de menú) y `/panel/lista-de-espera` dentro del panel.
- Menú: la entrada «Espera» aparece para owner, admin y recepción. La regla vive
  en `lib/permissions.ts` (`canViewWaitlist`) y la aplica el servidor: el
  profesional recibe 403 en `GET /salons/:id/waitlist`.
- El profesional no ve la entrada y, si entra por URL, ve `WaitlistNotice`
  (mismo patrón que `ManagerOnlyNotice`) y no la tabla. La etiqueta del menú es
  corta a propósito: la rejilla de tres columnas a 390 px recortaba «Lista de
  espera», el título de la página sí lo dice completo.

### 29.2 Lógica pura (`src/lib/waitlist.ts`)

- Copy del contrato verbatim: «Avisame si se libera un turno», «Listo, te
  avisamos por email si se libera un turno.» y el botón «Dejar de recibir
  avisos».
- `validateWaitlistDraft`: nombre 1..120 sin NUL, email obligatorio (es el ÚNICO
  canal del aviso, a diferencia de la reserva donde es opcional) con el mismo
  validador de `lib/email.ts`, y teléfono opcional con `lib/phone.ts`; una fecha
  tiene que ser un día real (`2026-02-31` no pasa). `waitlistPayload` manda solo
  las claves del contrato: sin profesional la clave `professional_id` NO viaja
  (queda «cualquiera») y sin teléfono tampoco `phone`.
- Cliente de las cuatro rutas con sus URLs y cuerpos congelados; la unión y la
  baja van sin sesión (`auth: 'none'`) y el token viaja solo en el cuerpo.
- `describeWaitlistError`: los tres 409 y el 404 se muestran verbatim, el 429 usa
  la frase que ya existe en el flujo público de reserva (`RATE_LIMIT_MESSAGE`) y
  una caída de red conserva el mensaje del cliente.
- `normalizeWaitlistEntries` lee la forma de D8 (una fila sin `id` no se muestra
  porque no se podría quitar) y `sortWaitlist` ordena por día y después por
  `created_at`, el mismo orden del servidor.
- El token del fragmento se lee con `readBookingLinkHash` de F1 (`/turno#<token>`
  y `/lista-de-espera#<token>` comparten mecanismo: 32 bytes en base64url).

### 29.3 Landing: «Avisame si se libera un turno»

- `BookingCta` gana el formulario (`WaitlistForm.tsx`) DEBAJO del mensaje «No
  quedan horarios libres ese día…», y solo ahí: con horarios, cargando, con error
  de disponibilidad o sin fecha no aparece. Usa el servicio y la fecha ya
  elegidos y no cambia nada del flujo de reserva.
- Estados: enviando, éxito verbatim, los 409 del servidor verbatim («Todavía hay
  horarios libres ese día», «Ya tenés el máximo de avisos activos», «La lista de
  espera de este salón está completa»), el 429 con la frase existente y el 400
  «Email inválido» junto al campo.
- El bloque NO es un `<form>`: vive dentro del formulario de la reserva y HTML
  prohíbe anidar formularios. Medido en el navegador antes de corregirlo: el
  submit nativo navegaba con `?name=…&email=…&phone=…` y el aviso nunca llegaba.
  Queda como `role="group"` con botón `type="button"` y Enter manejado a mano
  (el `Enter` del selector de país del teléfono sigue siendo suyo).

### 29.4 Página pública `/lista-de-espera`

- El token se lee del fragmento y se borra de la barra de direcciones con
  `history.replaceState` ANTES de cualquier pedido; vive solo en memoria (nunca
  storage, nunca query, nunca ruta, nunca consola) y viaja solo en el cuerpo.
- Se lee UNA vez por documento (estado de módulo, no `useRef`): en `npm run dev`
  React 18 remonta el componente y el segundo pase encontraba la URL ya limpia,
  de modo que con un token válido se veía «El enlace no trae el código del aviso».
  Medido y corregido con esa memoria por documento.
- Con token ofrece «Dejar de recibir avisos» y la baja es idempotente; el éxito
  no promete nada más y aclara que el horario sigue libre. El 404 «Aviso no
  encontrado» se muestra verbatim, y sin token hay una explicación con enlace al
  sitio (el `#` se pierde al reenviar el correo).

### 29.5 Panel: «Lista de espera»

- Tabla con servicio, profesional (o «Cualquiera»), día, nombre, correo,
  teléfono, estado («Esperando»/«Avisada») y «Quitar». El día se imprime con la
  fecha que mandó el salón (UTC mediodía), así ninguna zona horaria corre la fila
  un día.
- «Quitar» pide confirmación (`team-confirm`) y dice qué deja de pasar. Después
  del DELETE la lista se vuelve a pedir al servidor: no hay quita optimista, y un
  404 (otra persona ya lo quitó) también refresca. Estados de carga, error con
  «Reintentar», vacío honesto y «N avisos».

### 29.6 Pruebas

- TDD con `node --test` y `fetch` simulado: primero en rojo (`waitlist.ts` no
  existía), después en verde. 35 tests nuevos en `waitlist.test.ts`: la copy
  verbatim del contrato, la validación (nombre vacío/121/NUL, email obligatorio e
  inválido, teléfono opcional y mal formado, día real), el cuerpo exacto de la
  unión con y sin profesional/teléfono, las URLs y los métodos de las cuatro
  rutas (`auth` ausente en las públicas, `Bearer` en las del panel), los tres 409
  verbatim, el 429 con la frase existente, el 404 «Aviso no encontrado» de la
  baja y del DELETE, la idempotencia de la baja, los normalizadores, el orden por
  día y creación, las etiquetas («Cualquiera», «Esperando»/«Avisada», el día sin
  corrimiento) y la matriz de roles de `canViewWaitlist`.
- `npx tsc --noEmit` limpio, `npm test` 627/627 (592 previos sin tocar) y
  `npm run build` correcto.
- Revisión visual con el bundle de `vite build`, `vite preview` y una API
  simulada en `/tmp/f7f` (Chromium del caché de Playwright por protocolo
  DevTools) a **1280 y 390 px**: landing con el formulario bajo el mensaje, el
  409 verbatim en pantalla, el éxito verbatim (enviado con Enter desde el campo
  de correo, sin navegar), `/lista-de-espera` antes y después de la baja, panel
  con tres avisos, la confirmación de «Quitar», la quita real (3 → 2 filas con el
  aviso de éxito), la lista vacía y el profesional con el aviso en lugar de la
  tabla. `document.documentElement.scrollWidth === innerWidth` en las diez
  capturas.
- **Desviaciones conscientes:**
  - El runner no tiene entorno DOM: las reglas viven en funciones puras con
    tests y lo que es DOM (estados, Enter, confirmación, 44 px, desborde) se
    verificó en el navegador. No hay `.test.tsx`: `npm test` es
    `node --test src/**/*.test.ts` y Node no carga `.tsx`
    (`ERR_UNKNOWN_FILE_EXTENSION` medido).
  - La entrada del menú se llama «Espera» por el ancho de la rejilla a 390 px; el
    CSS del panel (`src/styles/app.css`) quedó fuera de las superficies.
  - El aviso del rol sin acceso es `WaitlistNotice` (nuevo, en
    `components/Waitlist*.tsx`) y no `ManagerOnlyNotice`, porque el copy
    compartido habla de «la nómina ni las comisiones del equipo».
  - Hallazgo fuera de las superficies de esta entrega: `/turno` (F1) e
    `/invitacion` (F2g1) leen su token con un `useRef` del montaje y en
    `npm run dev` (StrictMode remonta) pierden el token igual que perdía esta
    página; quedan como están y se anotan para su dueño.
