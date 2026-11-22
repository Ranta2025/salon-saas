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
