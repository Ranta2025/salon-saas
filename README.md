# Salón SaaS — plataforma multi-tenant para salones y peluquerías

SaaS web donde cada salón tiene su espacio aislado: agenda de turnos, clientes
con fotos, catálogo de servicios, POS y cierre de caja, notificaciones en tiempo
real y una PWA offline-first para el mostrador. El objetivo del proyecto es
mostrar decisiones de ingeniería reales (multi-tenancy, concurrencia, offline,
puertos/adaptadores) y no un CRUD de juguete.

## Arquitectura (por funcionalidad, hexagonal por dentro)

El código se ordena por **funcionalidad**: cada una es un paquete Go con sus
reglas, sus casos de uso y sus adaptadores. La regla de dependencia sigue yendo
de afuera hacia adentro: el núcleo (`domain`, `ports`, `kernel`) no conoce HTTP
ni SQL, y `cmd/api` es el único composition root que conoce adaptadores
concretos. Mapa completo, regla de dependencias y paso a paso para agregar una
funcionalidad: [docs/ARQUITECTURA.md](docs/ARQUITECTURA.md).

```
        HTTP / WS (navegador, PWA)
                 │
                 ▼
┌────────────────────────────────────────────────────────────────────────────┐
│ backend/internal/                                                          │
│                                                                            │
│  <funcionalidad>/  ──►  kernel/  ──►  ports/  ──►  domain/                 │
│    postgres/  ────────┘   (permisos, ajustes,   (interfaces)  (reglas)     │
│    httpapi/               avisos, eventos)                                 │
│                                                                            │
│  adapters/http (middleware + rutas)   adapters/postgres (pool, migraciones) │
│  adapters/realtime · storage · imaging · mail    testutil (fakes)          │
│                                                                            │
│  cmd/api = composition root: main.go + wire_<funcionalidad>.go             │
└────────────────────────────────────────────────────────────────────────────┘
                 │                                    │
                 ▼                                    ▼
        Postgres 16 (fuente de verdad)      media/ (fotos, detrás de
                                            ports.PhotoStorage)
```

Frontend: `frontend/src/features/<nombre>/{pages,components,lib,index.ts}` por
funcionalidad, con `src/lib/` para lo transversal (api, sesión, dinero, fechas,
realtime) y `src/app/` para el shell y los contextos.

Reglas transversales:

- **Todo dato cuelga de `salon_id`.** Sin membership en el salón, la API
  responde `403`; un recurso de otro salón responde `404` (no filtra existencia).
- **Dinero en centavos enteros**, nunca floats.
- **Reservas sin solape garantizado por la DB** (`EXCLUDE USING gist`), no solo
  por un chequeo previo: dos requests concurrentes al mismo hueco terminan en
  `409`, no en dos filas.
- **Fotos fuera de la DB**: hoy un volumen local detrás de `ports.PhotoStorage`
  (misma interfaz que usaría S3/MinIO; ver `docs/ops/minio.md`).

## Agregar una funcionalidad (resumen)

1. Migración SQL idempotente en `backend/migrations/`.
2. Interfaces en `internal/ports/ports_<funcionalidad>.go`.
3. Caso de uso en `internal/<funcionalidad>/` con sus tests.
4. Adaptador de persistencia en `internal/<funcionalidad>/postgres/`.
5. Handler en `internal/<funcionalidad>/httpapi/`.
6. Rutas en `internal/adapters/http/routes_<funcionalidad>.go` (+ una línea en
   `routes.go` y el handler en `Deps`).
7. Cableado en `backend/cmd/api/wire_<funcionalidad>.go` (+ su lugar en
   `buildRouter`).
8. Tests de integración (`*_it_test.go`) y, si hay UI, la carpeta en
   `frontend/src/features/<funcionalidad>/`.
9. Docs: [docs/ARQUITECTURA.md](docs/ARQUITECTURA.md), este README y
   `docs/DECISIONES.md`.

## Stack

| Capa | Tecnología |
|---|---|
| Backend | Go + Gin, `pgx` (Postgres), JWT (`golang-jwt`), WebSocket (`gorilla/websocket`) |
| Base de datos | Postgres 16 (migraciones SQL idempotentes en `backend/migrations/`) |
| Frontend | React 18 + TypeScript + Vite, PWA offline-first |
| Imágenes | `cwebp` (libwebp) detrás de `ports.ImageProcessor` |
| Infra local | Docker Compose: api, web (nginx), postgres, redis, mailpit; MinIO opcional (`files`) |
| Tests | `go test -race`, tests de integración con `SALON_IT=1`, tests del frontend con Node |
| CI | GitHub Actions (`.github/workflows/ci.yml`) |

## Cómo correrlo

### Con Docker (stack completo)

Requisitos: Docker + Docker Compose v2. Las imágenes del backend y del frontend
se construyen localmente.

Primero creá el archivo de variables con un secreto real (ver
[docs/ops/environment.md](docs/ops/environment.md), incluye la versión para fish
y para bash) y después:

```bash
make up          # docker compose up -d --build
```

Servicios y puertos por defecto (todos configurables en `.env`):

| Servicio | URL / puerto host | Notas |
|---|---|---|
| `web` | http://localhost:8081 | build estático servido por nginx en `:80` |
| `api` | http://localhost:8080 | `GET /health`, API en `/api/v1` |
| `postgres` | `localhost:5432` | usuario/clave/base `salon` |
| `redis` | `localhost:6379` | reservado para cola/realtime futura |
| `mailpit` | http://localhost:8025 | SMTP en `localhost:1025` |
| `minio` (opcional) | http://localhost:9001 | solo con `--profile files` |

Comandos útiles: `make logs`, `make down`, `make test`, `make test-it`,
`make lint`, `make web-build`.

MinIO es **opcional** y no lo usa la app todavía (las fotos van al volumen
local). Se levanta con:

```bash
docker compose --profile files up -d minio
```

Si la imagen no se puede bajar, `docs/ops/minio.md` lista las alternativas que
sí resuelven y cómo apuntar `MINIO_IMAGE` a la que funcione.

### Sin Docker (API + Vite en el host)

Requisitos: Go (la versión de `backend/go.mod`), Node 22, Postgres 16 y `cwebp`
(`apt install webp` o `brew install webp`).

```bash
# 1. Postgres (si no hay uno local, alcanza con el contenedor)
docker compose up -d postgres redis

# 2. API
export DATABASE_URL='postgres://salon:salon@localhost:5432/salon?sslmode=disable'
export JWT_SECRET='un-secreto-de-al-menos-32-caracteres'
export MEDIA_DIR='./media'
cd backend && go run ./cmd/api        # :8080, aplica migraciones al arrancar

# 3. Frontend (otra terminal)
cd frontend && npm ci && npm run dev  # :5173
```

La API lee la configuración del entorno; ver la tabla de variables más abajo.

## Variables de entorno

La plantilla con todas las variables está en
[docs/ops/environment.md](docs/ops/environment.md); el archivo real lo creás
vos y está ignorado por git.
Compose arma `DATABASE_URL` solo con las piezas `POSTGRES_*`; las variables de
"API" son las que lee el binario Go.

| Variable | Default | Quién la usa | Para qué |
|---|---|---|---|
| `JWT_SECRET` | — (**obligatoria**) | api | firma de los JWT; mínimo 32 caracteres en `GIN_MODE=release` |
| `DATABASE_URL` | `postgres://salon:salon@localhost:5432/salon?sslmode=disable` (código) | api | conexión a Postgres; en Compose se arma con las `POSTGRES_*` |
| `CORS_ORIGINS` | `http://localhost:5173,http://localhost:4173` (código) | api | orígenes permitidos, separados por coma |
| `MEDIA_DIR` | `./media` (código) / `/media` (Compose) | api | carpeta de fotos del adaptador local |
| `PORT` | `8080` | api | puerto HTTP dentro del contenedor |
| `GIN_MODE` | `debug` | api | `debug` \| `release` \| `test` |
| `VITE_API_URL` | `http://localhost:8080` | frontend (build) | origen de la API que se hornea en el build (sin `/api/v1`: el frontend ya lo agrega) |
| `API_PORT`, `WEB_PORT`, `POSTGRES_PORT`, `REDIS_PORT`, `MAILPIT_PORT`, `MAILPIT_SMTP_PORT` | `8080`, `8081`, `5432`, `6379`, `8025`, `1025` | Compose | puertos del host |
| `POSTGRES_USER`, `POSTGRES_PASSWORD`, `POSTGRES_DB` | `salon` | Compose | credenciales de la base local |
| `POSTGRES_IMAGE`, `REDIS_IMAGE`, `MAILPIT_IMAGE` | `postgres:16`, `redis:7`, `axllent/mailpit:latest` | Compose | imágenes de servicios |
| `MINIO_IMAGE` | `pgsty/minio:latest` | Compose (`files`) | imagen de MinIO/S3 (ver `docs/ops/minio.md`) |
| `MINIO_ROOT_USER`, `MINIO_ROOT_PASSWORD` | `salonminio` / placeholder | Compose (`files`) | credenciales de MinIO |
| `MINIO_PORT`, `MINIO_CONSOLE_PORT` | `9000`, `9001` | Compose (`files`) | API S3 y consola web |
| `SALON_IT` | sin definir | tests | `1` habilita los tests de integración |

## Tests

```bash
# Backend: unitarios + detector de carreras (no necesitan DB)
make test                       # cd backend && go test -race -count=1 ./...

# Backend: integración real contra Postgres (anti-solape, POS, clientes, señas)
# La base tiene que existir con las migraciones aplicadas.
docker compose up -d postgres
export DATABASE_URL='postgres://salon:salon@localhost:5432/salon?sslmode=disable'
make test-it                    # SALON_IT=1 contra el postgres/ de cada funcionalidad

# Frontend: typecheck, tests y build
cd frontend && npm ci && npx tsc --noEmit && npm test && npm run build
```

Los tests unitarios viven al lado del paquete que prueban
(`internal/<funcionalidad>/*_test.go`, fakes compartidos en `internal/testutil`)
y los de integración en el `postgres/` de cada funcionalidad (`*_it_test.go`,
solo con `SALON_IT=1`).

Los tests del adaptador de imágenes necesitan `cwebp` instalado (paquete `webp`
en Debian/Ubuntu). El CI instala las dependencias y aplica las migraciones
antes de correr la integración.

## API (v1, prefijo `/api/v1`)

Contrato detallado en `docs/api/`:

- `docs/api/pos-servicios.md` — servicios, ventas, cierre de caja, dashboard.
- `docs/api/notificaciones-clientes.md` — notificaciones, websocket, clientes,
  fotos y subida de adjuntos.

Resumen de grupos de rutas:

| Grupo | Rutas |
|---|---|
| Auth | `POST /auth/register`, `POST /auth/login` |
| Tenants | `POST /salons`, `GET /me`, `GET /salons/:id/staff` |
| Reservas | `GET/POST /salons/:id/bookings` |
| Servicios | `GET/POST /salons/:id/services`, `PUT /salons/:id/services/:service_id` |
| POS | `GET/POST /salons/:id/sales` |
| Caja | `GET/POST /salons/:id/cash-closes` |
| Dashboard | `GET /salons/:id/dashboard` |
| Clientes | `GET/POST /salons/:id/clients`, fotos por cliente |
| Público | `GET /public/salons/:slug`, `GET /photos/:pid` |
| Realtime | `GET /ws?token=<jwt>` (eventos `booking.*`, `sale.created`, avisos) |

Convenciones de error: `{"error":"<mensaje>"}` con `400` validación, `401` sin
token, `403` sin permiso, `404` no existe, `409` duplicado/carrera.

## Estado del proyecto

Implementado y con tests:

- Auth con JWT, multi-tenancy por membership y roles (`owner/admin/reception/professional/client`).
- Reservas con anti-solape garantizado por Postgres (constraint `EXCLUDE` + transacción).
- Catálogo de servicios, POS (ventas idempotentes) y cierre de caja con diferencia contable.
- Notificaciones persistidas + hub realtime por websocket.
- Clientes con ficha e historial, fotos normalizadas a WebP en almacenamiento local.
- PWA offline-first con cola de operaciones e idempotencia (`client_op_id`).
- Landing pública del salón con fotos, dashboard del día y shell responsive.
- Docker Compose completo, CI y documentación de operación.

Próximo (roadmap):

1. Reserva pública real (`availability` + `POST /public/salons/:slug/bookings`).
2. Editar / cancelar / marcar no-show de un turno desde el calendario.
3. Gestión de equipo (altas de miembros y roles desde la UI).
4. MinIO/S3 como adaptador de `ports.PhotoStorage` (hoy volumen local).

## Licencia y atribución

- Las **fotos de la landing** son de Wikimedia Commons, verificadas y con
  licencia libre (CC0, CC BY 2.0 o CC BY-SA 4.0). Autor, licencia y URL de cada
  archivo están en `docs/assets/images.md`; la landing muestra la atribución en
  el pie, como exigen CC BY / CC BY-SA.
- El **código** de este repositorio no declara una licencia todavía; hasta que
  exista un archivo `LICENSE`, vale el derecho de autor por defecto.
