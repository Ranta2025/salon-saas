# Salón SaaS — Fase 4: realtime, notificaciones, adjuntos y PWA offline

## Specs
- S1 (usuario): "usa sub agentes ya pueden correr bien" — dos unidades
  independientes en paralelo (backend / frontend), con contrato de API fijado
  por adelantado para que no se pisen.
- S2 (usuario): "rdd a todo" — assess/revisión por unidad antes de integrar.
- S3 (usuario): "siempre buenas practicas respetando principios solid y patrones
  de diseno" — hexagonal igual que las fases anteriores: dominio, ports, app,
  adapters; el dominio sin dependencias externas.

## Contexto ya construido (no rehacer)

- Backend Go/Gin/Postgres/pgx hexagonal: `internal/domain`, `internal/ports`,
  `internal/app`, `internal/adapters/{http,postgres,security}`. (ahora: un
  módulo por funcionalidad en `internal/<funcionalidad>/{,postgres,httpapi}`,
  con el núcleo en `internal/{domain,ports,kernel}` y los adaptadores
  compartidos en `internal/adapters/`; ver `docs/ARQUITECTURA.md`.)
- Migraciones `backend/migrations/001..006`, idempotentes y re-ejecutables en
  cada boot (`migrate()` aplica todos los archivos en orden). Regla dura: dentro
  de bloques `DO` con guardas de `pg_catalog`; nunca referenciar una columna que
  otra migración renombra sin guarda.
- Auth JWT (24 h) + bcrypt; roles `owner|admin|professional|reception|client`.
  El dinero (ventas, cierres, dashboard) solo lo ven `owner|admin|reception`
  (`puedeVerPlata` en `internal/pos/sales.go`).
- Reservas con anti-solape garantizado por `EXCLUDE USING gist` (migración 002).
- Frontend React+TS+Vite: `src/features/landing/**` (público), `src/app/**` (interno),
  `src/lib/**` (api, money, metrics, posCart, datetime, session).
- Docker: postgres 5432, redis 6379, mailpit 8025 (arriba); MinIO **no** se
  puede bajar en este entorno (su imagen da 401 en quay.io y en Docker Hub).
  Para adjuntos se usa un adapter de filesystem local con la misma interfaz que
  usaría S3 (así el cambio real es una línea en el composition root).

## API contract (acordado; ambos writers lo respetan)

```
GET    /api/v1/salons/:id/notifications          -> {notifications:[...]}
POST   /api/v1/notifications/:nid/read            -> 204
WS     /api/v1/ws?token=<jwt>                    -> eventos del salón
POST   /api/v1/salons/:id/clients/:cid/photos    -> 201 {photo:{id,url,w,h,bytes}}
GET    /api/v1/salons/:id/clients/:cid/photos    -> {photos:[...]}
DELETE /api/v1/salons/:id/clients/:cid/photos/:pid -> 204
GET    /api/v1/salons/:id/clients                -> {clients:[...]}
POST   /api/v1/salons/:id/clients                -> 201
```

Eventos del websocket (payload `{type, salon_id, data, ts}`):

```
booking.created | booking.updated | sale.created | cash_closed
notify          -> {notification:{id,title,body,kind,read}}
```

Notificación persistida en tabla `notifications` (id, salon_id, user_id, title,
body, kind, read_at, created_at). El `user_id` puede ser nulo para avisos de
todo el salón.

Adjuntos: tabla `client_photos` (id, salon_id, client_id, path, width, height,
bytes, content_type, created_at). Se aceptan jpg/png/webp de hasta 8 MB, se
rechaza con 400 lo que no sea imagen. Se genera una variante WebP de hasta
1280 px de lado mayor.

## Tasks

- [ ] T1 (backend): notificaciones persistidas + hub de websocket + eventos de
  dominio. `domain.Notification`, `ports.NotificationRepository`,
  `ports.EventPublisher`, `app.NotificationService`, `pgstore.Notifications`,
  `internal/adapters/realtime` (hub con rooms por salon_id y auth por token),
  migración 007, HTTP de listar/marcar leída, y los casos de uso que ya
  existen (venta, reserva, cierre) publican su evento. Tests: unit del hub
  (aislamiento entre salones: un cliente del salón A nunca recibe eventos de B)
  + httptest + IT.
- [ ] T2 (backend): clientes y adjuntos. `domain.Client`, `domain.ClientPhoto`,
  `ports.ClientRepository`, `ports.PhotoStorage` (interfaz; adapter local en
  `internal/adapters/storage`), `app.ClientService`, migración 008, HTTP de
  clientes y fotos, validación de tipo/tamaño, variante WebP con `cwebp` o
  `sharp` si está (si no, Go con `image/jpeg`+`image/png` y webp solo si hay
  encoder). Tests: unit + httptest + IT.
- [ ] T3 (frontend): cliente de websocket + centro de notificaciones + ficha de
  cliente con subida de fotos. `src/lib/realtime.ts` (reconexión con backoff,
  token por query como define el contrato), badge y panel de notificaciones,
  ficha de cliente con galería y subida con preview. Tests de `src/lib`.
- [ ] T4 (frontend): PWA offline — service worker + manifest + cola de operaciones
  pendientes (crear venta o reserva sin red, se sincroniza al volver) con
  idempotencia por `client_op_id`. Reusa lo que ya existe de `posCart`/`session`.
  Tests de la cola en `src/lib`.

## Allowed edit surfaces

Writer A (backend):
```
backend/**
docs/api/**
```

Writer B (frontend):
```
frontend/**
docs/design/**
```

## Reglas

- SOLID + hexagonal: el dominio no importa nada externo; los repos y el storage
  detrás de interfaces; un caso de uso por archivo en `internal/app` (ahora: en
  `internal/<funcionalidad>/*.go`).
- Todo scoped por `salon_id`. Sin excepción.
- El servidor es la autoridad del dinero y de los totales.
- Migraciones idempotentes y válidas sobre DB ya migrada y sobre DB vacía.
- TDD: test rojo antes de la implementación, y reportar RED y GREEN.
- `gofmt`, `go vet` limpios, `go test ./...` en verde, `SALON_IT=1` en verde
  (Postgres en 127.0.0.1:5432, user salon, db salon, password salon).
- Frontend: `tsc --noEmit` limpio, `npm test` en verde, `npm run build` OK.
- Nada de commit ni push: deja el árbol con los cambios.

## Log

- L1 (orquestador): el writer de la fase 3 murió al final por modelos
  inexistentes (`opencode-go/deepseek-*`); su trabajo igual quedó completo y
  verificado por el orquestador (typecheck, 30 tests, build, captura visual).
- L2 (orquestador): fotos convertidas a WebP (6.8 MB -> 1.17 MB) y se descartó
  una foto de 240x320 que quedaba mal en la galería.
