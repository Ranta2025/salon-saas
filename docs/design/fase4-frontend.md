# Fase 4 — frontend: tiempo real, notificaciones, fotos y PWA offline

Documento de diseño de la unidad de frontend (T3 y T4 de
`odd/tasks/fase4.md`). Stack sin dependencias nuevas: React 18 + TypeScript +
Vite, `react-router-dom`, CSS plano con custom properties y un service worker
vanilla (sin workbox).

## 1. Alcance

| T | entrega |
| --- | --- |
| T3 | `src/lib/realtime.ts`, centro de notificaciones (campana + panel + página), clientes con ficha, galería y subida de fotos |
| T4 | service worker + manifest + iconos, cola offline con idempotencia por `client_op_id`, banner y contadores de sincronización, POS que encola cuando la API no responde |

Rutas nuevas del panel:

| ruta | página | endpoints |
| --- | --- | --- |
| `/panel/clientes` | `ClientsPage` | `GET /salons/:id/clients` |
| `/panel/clientes/:clientId` | `ClientDetailPage` | `GET /salons/:id/clients/:cid/photos`, `POST …/photos`, `DELETE …/photos/:pid` |
| `/panel/avisos` | `NotificationsPage` | `GET /salons/:id/notifications`, `POST /notifications/:nid/read` |

`/clients` (ruta vieja del esqueleto) ahora redirige a `/panel/clientes`.

## 2. Tiempo real (`src/lib/realtime.ts`)

- Conexión a `WS {API_BASE}/api/v1/ws?token=<jwt>`; `http(s)://` se convierte en
  `ws(s)://` con `buildWebSocketUrl` (token URL-encoded).
- API tipada: `subscribe(type, handler)` para `booking.created`,
  `booking.updated`, `sale.created`, `cash_closed` y `notify`; `notify` entrega
  la notificación ya normalizada (`src/lib/notifications.ts`).
- Reconexión con backoff exponencial (500 ms → 30 s, con jitter) y reset del
  contador al abrir de nuevo. Los handlers registrados sobreviven al corte: al
  reconectar el cliente vuelve a entregar eventos a las mismas suscripciones
  ("re-subscribe" del lado cliente). No se envía ningún frame cliente→servidor
  porque el contrato solo define eventos servidor→cliente; el hub autoriza por
  token y aísla por salón.
- Eventos `offline`/`online` del navegador: al ir a offline se cierra el socket y
  se cancelan reintentos; al volver online se reconecta de inmediato (sin
  esperar el backoff).
- Degradación: sin `WebSocket` o si la fábrica devuelve `null`, el cliente queda
  en estado `unavailable` y no lanza; si no hay token queda `idle`. Los frames
  inválidos o de tipo desconocido se ignoran.
- `onStatus` expone `idle | connecting | open | reconnecting | offline |
  unavailable | stopped` para la UI (`REALTIME_STATUS_LABEL`).

## 3. Notificaciones

- `NotificationsProvider` (dentro de `AppShell`) carga la lista al entrar al
  panel, abre el websocket del salón y antepone los eventos `notify` (dedupe por
  `id`, se ignoran eventos de otro `salon_id`).
- La campana (`NotificationCenter`) vive en la barra superior: badge de no
  leídas (máx. `9+`), `aria-expanded`, panel con las últimas 5, estado del canal
  en vivo y enlace a `/panel/avisos`. `Escape` y el botón de cerrar cierran el
  panel.
- Marcar leída es optimista: se actualiza la lista y se revierte si
  `POST /notifications/:nid/read` falla.
- `notifications.ts` normaliza las dos formas del contrato (`read` booleano del
  websocket y `read_at` de la tabla), ordena por fecha descendente y descarta
  filas que no se pueden renderizar.

## 4. Clientes y fotos

- `ClientsPage`: lista con búsqueda local por nombre, teléfono o correo; tarjetas
  enlazadas a la ficha. Sin endpoint de alta en la interfaz (la ficha nace del
  flujo público de reservas).
- `ClientDetailPage`: nombre resuelto desde la lista (no existe
  `GET /clients/:id` en el contrato), fotos, galería y borrado con confirmación
  de dos pasos.
- `PhotoUploader`: validación antes de cualquier request
  (`src/features/clients/lib/photos.ts`): tipo JPG/PNG/WebP y máximo 8 MB; mensajes en español
  para formato inválido, archivo vacío y exceso de peso. Preview local con
  `URL.createObjectURL` (revocado al descartar o desmontar), y error legible con
  el mensaje del servidor si rechaza la subida. El request es
  `multipart/form-data` con el campo **`photo`**.
- Cada `<img>` lleva `alt` descriptivo ("Foto N de <cliente>"); los botones de
  borrar usan `aria-label` con el mismo texto.
- Las URLs de foto se resuelven contra `API_BASE` cuando el servidor devuelve
  una ruta relativa.

## 5. PWA offline

### Service worker (`public/sw.js`, vanilla)

| petición | estrategia |
| --- | --- |
| `GET /api/v1/**` | network-first; si falla la red, copia en caché; si no hay copia, `503` JSON legible |
| navegación (`request.mode === 'navigate'`) | network-first con fallback a `index.html` cacheado |
| assets mismo origen (JS/CSS/fuentes/imágenes) | cache-first |
| todo lo que no sea `GET` | no se intercepta ni se cachea (las mutaciones las maneja la cola) |
| `/api/v1/ws` | se ignora |

- `install` precachea el shell (`/`, `index.html`, manifest e iconos) y los
  assets con hash referenciados por el `index.html` construido.
- `activate` borra cachés viejas y hace `clients.claim()`.
- La clave de caché de la API incluye un hash del `Authorization`, así una
  cuenta no lee respuestas cacheadas de otra en el mismo navegador. Al cerrar
  sesión o recibir un 401 la app manda `salon:clear-api-cache` y la caché de API
  se borra.
- El SW se registra solo en build de producción (`registerServiceWorker`) con
  `updateViaCache: 'none'`; en desarrollo Vite serviría el archivo sin
  optimizar y una caché vieja ocultaría los cambios. La versión de caches es
  `salon-v2` y debe subirse en cada deploy que cambie el worker (ver
  `docs/design/fase5-frontend.md` §4).

### Manifest, iconos y meta tags

- `public/manifest.webmanifest`: `display: standalone`, `start_url: /panel`,
  colores de la paleta (`#3B1E33` / `#FAF5EF`) e iconos PNG 192/512 `any` +
  512 `maskable` (`icon-192.png`, `icon-512.png`, `icon-maskable-512.png`) más
  `apple-touch-icon.png` de 180, generados desde los SVG de
  `public/icons/`.
- `index.html`: `link rel=manifest`, favicon SVG, `apple-touch-icon`,
  `theme-color`, `mobile-web-app-capable`, tags de iOS.

## 6. Cola de operaciones offline (`src/lib/offlineQueue.ts`)

Modelo persistido en `localStorage['salon.offline_queue']` (con fallback en
memoria):

```ts
type QueuedOperation = {
  client_op_id: string   // uuid v4, idempotencia
  kind: 'sale' | 'booking'
  salon_id: string
  payload: SaleInput | BookingInput
  created_at: string
  attempts: number
  next_attempt_at: number
  last_error: string | null
}
```

- `enqueue(kind, salonId, payload, clientOpId?)` deduplica por `client_op_id`:
  encolar dos veces el mismo id devuelve la operación existente.
- `flush(send)` envía en orden FIFO. Un `4xx` definitivo (salvo 408/429) se
  mueve a `dropped` de inmediato y la cola sigue con la operación siguiente; un
  `401` pausa la cola sin gastar intentos; el resto incrementa `attempts` y
  programa el reintento con backoff exponencial (5 s → 5 min). A los 8 intentos
  la operación se mueve a `dropped` y la UI avisa con su `client_op_id` y el
  último error. La cola se aísla por usuario (`salon.offline_queue:<user.id>`).
- Si el navegador está offline (`canSend`), el flush no consume intentos: espera
  el evento `online`. Los reintentos y el flush del montaje sobreviven a un
  reload porque el estado está en `localStorage`.
- `flush` concurrente comparte la misma promesa: nunca se envía dos veces la
  misma operación. La idempotencia final la garantiza el servidor con
  `client_op_id` en el body (ver §8).
- `snapshot()` devuelve una referencia estable para `useSyncExternalStore`.

### Dónde ve el usuario el estado pendiente

1. **Banner bajo la barra superior** (`OfflineBanner`): "Estás sin conexión…" con
   el número de operaciones pendientes, o "Sincronización pendiente" con botón
   **Sincronizar ahora** cuando hay red y trabajo pendiente. Si la cola descartó
   operaciones, lista cada `client_op_id` con su último error y se descarta con
   "Entendido".
2. **Chip contador** (`SyncStatusChip`) en la barra superior: "N operaciones
   pendientes" (y "Sin conexión" cuando aplica), visible en todo el panel.
3. **POS**: la venta encolada aparece en "Ventas de hoy" como fila
   **Pendiente de sincronizar** con su total estimado (calculado en la
   interfaz), y el ticket muestra "Venta guardada en este dispositivo. Todavía
   no está en el servidor…". Al confirmarse el servidor, la fila pasa a
   "Registrada" y el total mostrado es el del servidor.

### POS (`PosPage` + `POSPanel`)

- Cada cobro genera un `client_op_id` antes de llamar a la API: si el request
  directo falla con `NetworkError` (o el navegador ya está offline), la venta se
  encola y el panel devuelve el resultado `queued`.
- Si el servidor responde con un error HTTP (400/403/…), no se encola: se muestra
  el error y no se inventa un éxito local.
- El carrito se limpia también cuando la venta queda pendiente, para no cobrar
  dos veces la misma venta en el dispositivo.

## 7. CSS y accesibilidad

- Todo en `src/styles/app.css` con los tokens de `theme.css`; panel más denso
  que la landing (`--radius-sm/md`, `--font-mono` para ids y montos).
- Controles alcanzables por teclado, foco visible heredado de `base.css`,
  `aria-label` en botones de icono (campana, cerrar panel, borrar foto),
  `role="status"` en contadores/banners y `role="alert"` en errores.
- Fotos con `alt` descriptivo y `loading="lazy"`.

## 8. Contrato de API: precisión y supuestos

Respetado tal cual: `GET /salons/:id/notifications`,
`POST /notifications/:nid/read`, `WS /api/v1/ws?token=`,
`POST/GET /salons/:id/clients/:cid/photos` (campo `photo`),
`DELETE /salons/:id/clients/:cid/photos/:pid`, `GET /salons/:id/clients`, y el
payload `{type, salon_id, data, ts}` con `notify → {notification:{id,title,body,kind,read}}`.

Supuestos/adendas que el backend debe confirmar:

1. **`client_op_id` en el body** de `POST /sales` y `POST /bookings`. No está en
   el bloque de contrato de la fase, pero T4 exige idempotencia por
   `client_op_id`. El backend debe aceptarlo y deduplicar con él (mismo id →
   misma venta, no una segunda).
2. **`POST /salons/:id/bookings`**: la cola soporta reservas (`kind: 'booking'`)
   y las envía a ese endpoint, que no aparece en el contrato de la fase 4
   (supuesto: endpoint existente de la fase 3). Hoy la app no tiene formulario de
   alta de citas, así que la cola de reservas queda lista pero sin UI que la
   alimente.
3. **Campos de cliente**: se leen `id`, `name` (o `full_name`), `phone`, `email`
   y `created_at`; lo que falte se muestra como "Sin teléfono/ correo" y nunca
   rompe la ficha.
4. **Fotos**: el POST responde `{photo:{id,url,w,h,bytes}}`; el GET puede usar
   `w/h` o `width/height`. La `url` puede ser absoluta o relativa a la API.
5. **URL de la foto y auth**: `<img>` no envía el header `Authorization`; la URL
   devuelta por el backend debe ser accesible sin JWT (estática del adapter
   local). Si no, la galería mostrará imágenes rotas.
6. **Notificaciones**: se acepta `read: boolean` o `read_at: string|null`; el
   listado puede venir como `{notifications:[…]}`.
7. El cliente websocket no envía frames (ver §2): el contrato no define
   mensajes cliente→servidor.

## 9. Pruebas

`npm test` corre con el runner nativo (`node --test src/**/*.test.ts`, sin
dependencias nuevas):

| archivo | cubre |
| --- | --- |
| `realtime.test.ts` | URL ws/wss con token, entrega tipada, backoff 500/1000/2000/4000 cap, re-suscripción tras reconectar, reset del backoff, offline/online, arranque offline, socket no disponible sin excepciones, `stop` |
| `photos.test.ts` | tipos aceptados, rechazo de otros formatos, límite de 8 MB (borde incluido), archivo vacío, formato de tamaños |
| `offlineQueue.test.ts` | encolar sin red y persistir, sobrevivir reload, flush en orden, backoff y espera, cap de intentos con descarte, dedupe por `client_op_id` y flush concurrente, id uuid, estado corrupto tolerado |
| `notifications.test.ts` | normalización de ambas formas, descarte de filas inválidas, orden y no leídas, formato de fecha local |
| `api.test.ts`, `money.test.ts`, `datetime.test.ts`, `metrics.test.ts`, `posCart.test.ts` | sin cambios (PRESERVE) |

Verificación de tipos y build: `npx tsc --noEmit` y `npm run build`.

## 10. Estructura de archivos

```
frontend/
  index.html                     manifest, iconos, meta tags PWA
  public/sw.js                   service worker vanilla (no workbox)
  public/manifest.webmanifest    app installable
  public/icons/*.svg             iconos SVG (any + maskable + favicon)
  src/lib/realtime.ts            websocket tipado, backoff, offline/online
  src/lib/notifications.ts       normalización, no leídas, fecha local
  src/features/clients/lib/photos.ts              validación de tipo/tamaño antes de subir
  src/lib/offlineQueue.ts        cola FIFO persistente con client_op_id
  src/lib/connectivity.ts        navigator.onLine + eventos online/offline
  src/lib/pwa.ts                 registro del SW y purga de caché de API
  src/lib/api.ts                 notificaciones, clientes, fotos, bookings, cola
  src/app/NotificationsContext.tsx  lista + stream en vivo
  src/app/OfflineContext.tsx     cola singleton, conectividad, flush
  src/features/notifications/components/NotificationCenter.tsx  campana, badge y panel
  src/app/components/SyncStatus.tsx          chip y banner offline
  src/app/components/PhotoUploader.tsx       preview + validación + upload
  src/features/clients/pages/ClientsPage.tsx        lista de clientes
  src/features/clients/pages/ClientDetailPage.tsx   ficha, galería, subida y borrado
  src/features/notifications/pages/NotificationsPage.tsx  listado completo
```
