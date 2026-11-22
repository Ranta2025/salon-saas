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
- `panelHomePath(role)` devuelve `/panel/calendario` para `professional` y
  `null` para el resto. El índice de `/panel` (`PanelHome`) redirige antes de
  montar `DashboardPage`, así que un professional **nunca** pide `/dashboard` y
  nunca ve el 403. Mientras `/me` carga se muestra un estado de carga, no el
  panel.
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
  `bookingCountLabel` (`bookingActions.test.ts`), `panelHomePath`,
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
