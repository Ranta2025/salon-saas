# API — Fase 5: hardening, CORS y endpoints nuevos (ronda 2 incluida)

Base: `/api/v1`. Este documento cubre lo que cambió en la fase 5. El contrato de
catálogo/POS sigue en [`pos-servicios.md`](./pos-servicios.md) y el de avisos en
[`notificaciones-clientes.md`](./notificaciones-clientes.md).

## Variables de entorno del backend

| Variable | Obligatoria | Default | Qué hace |
|---|---|---|---|
| `DATABASE_URL` | no | `postgres://salon:salon@localhost:5432/salon?sslmode=disable` | Conexión a Postgres. |
| `JWT_SECRET` | **sí** | — | Firma y valida los tokens. **Sin ella el proceso no arranca.** Debe tener **≥ 32 caracteres**; solo `GIN_MODE=debug`/`test` admiten un secreto corto. |
| `CORS_ORIGINS` | no | `http://localhost:5173,http://localhost:4173` | Allowlist de orígenes del navegador, separada por comas. Se usa para CORS **y** para el `Origin` del websocket. |
| `MEDIA_DIR` | no | `./media` | Directorio de las fotos adjuntas cuando se usa el adaptador de disco local. |
| `S3_ENDPOINT` | no | — (vacío) | Si está definido, las fotos se guardan en S3/MinIO en vez del disco local. Admite `host:puerto` o una URL `http(s)://host:puerto` (el esquema ajusta TLS). |
| `S3_ACCESS_KEY` | solo con S3 | — | Access key de S3/MinIO. |
| `S3_SECRET_KEY` | solo con S3 | — | Secret key de S3/MinIO. |
| `S3_BUCKET` | solo con S3 | `salon-media` | Bucket donde viven las fotos. Con `S3_ENDPOINT` definido no puede quedar vacío. |
| `S3_USE_SSL` | no | `false` | `true` para hablar con el bucket por HTTPS (también lo activa un `S3_ENDPOINT` con `https://`). Un valor que no sea booleano impide arrancar. |
| `PORT` | no | `8080` | Puerto HTTP. |
| `GIN_MODE` | no | `release` | `debug`/`test` para desarrollo (habilitan secretos JWT cortos y logs verbose de Gin). |

El proceso aplica las migraciones al arrancar (con lock global, ver abajo),
escucha en `:$PORT`, y ante `SIGINT`/`SIGTERM` deja de aceptar conexiones, espera
a las que están en curso (15 s) y se cierra.

## CORS

El navegador necesita preflight para el login. Ahora la API lo responde:

```
OPTIONS /api/v1/auth/login
Origin: http://localhost:5173
Access-Control-Request-Method: POST
→ 204
  Access-Control-Allow-Origin: http://localhost:5173
  Access-Control-Allow-Methods: GET, POST, PUT, PATCH, DELETE, OPTIONS
  Access-Control-Allow-Headers: Authorization, Content-Type
  Access-Control-Max-Age: 600
  Vary: Origin
```

- Un origen **de la allowlist** recibe también `Access-Control-Allow-Origin` en
  las respuestas reales (sin eso el navegador descarta el body).
- Un origen **ajeno** no recibe headers CORS y su preflight responde `403`.
- Un pedido **sin** `Origin` (curl, server-to-server) no se toca.
- El websocket `GET /api/v1/ws` valida el `Origin` con la **misma** allowlist.

## Límites transversales

| Límite | Valor | Respuesta al excederse |
|---|---|---|
| Body JSON | 1 MB | `400 {"error":"body inválido"}` |
| Login / registro | 20 intentos por IP por minuto | `429 {"error":"demasiados intentos, esperá un minuto y volvé a probar"}` |
| Reserva pública (`POST /public/salons/:slug/bookings`) | 20 intentos por IP por minuto | `429` (mismo mensaje). El `GET` de disponibilidad y la vidriera **no** se limitan. |
| Venta: unidades por línea | 1000 | `400` |
| Venta: precio unitario | ≤ 10.000.000 centavos | `400` |
| Venta: líneas | 50 | `400` |
| Venta: total | ≤ 2.000.000.000 centavos | `400` |
| Turno: duración | ≤ 12 h | `400` |
| Turno: inicio | no en el pasado (±30 s de tolerancia de reloj) | `400` |
| Textos | nombre 120, teléfono 40, notas 2000, email 254, título 120, cuerpo 500 caracteres | `400` |
| Clave | 8 a 72 bytes | `400` |

`500` siempre responde `{"error":"error interno"}`; el detalle (SQL, pgx) queda
solo en el log del servidor.

## Endpoints nuevos

### `GET /me`

Requiere `Authorization: Bearer <jwt>`. Devuelve quién es el dueño del token y en
qué salones está (con nombre y rol): es lo que permite elegir el salón activo sin
que la persona pegue un ID a mano.

```json
{
  "user": { "id": "u_...", "name": "Ana", "email": "ana@mail.com" },
  "salons": [
    { "id": "s_...", "slug": "navajas-oro", "name": "Navajas de Oro", "role": "owner" }
  ]
}
```

- `salons` es **siempre** un array (nunca `null`), aunque esté vacío.
- Token válido de un usuario que ya no existe → `401`.
- Sin token → `401`.

### `GET /salons/:id/staff`

Requiere `Authorization: Bearer <jwt>` y ser **miembro** del salón (cualquier
rol). Es la fuente del select de profesional de la agenda.

```json
{ "staff": [ { "id": "u_...", "name": "Bruno", "role": "professional" } ] }
```

- Roles incluidos: `owner`, `admin`, `professional`. `reception` y `client` no
  atienden, así que no aparecen.
- Orden: por nombre.
- `staff` es siempre un array.
- No miembro → `403`. Sin token → `401`.

### `GET /public/salons/:slug`

**Sin autenticación.** Es la vidriera de la landing.

```json
{
  "salon": { "id": "s_...", "slug": "navajas-oro", "name": "Navajas de Oro" },
  "services": [
    { "id": "svc_...", "name": "Corte de dama", "duration_min": 45, "price_cents": 150000 }
  ]
}
```

- Solo servicios **activos**, ordenados por nombre.
- `services` es siempre un array.
- Slug inexistente → `404 {"error":"no encontrado"}`.
- Es la **única** ruta sin scope de membership; no expone precios facturados,
  clientes ni personal.

## Ronda 2 (T6): configuración, reserva pública, turnos, equipo y fotos

Todo lo de esta sección se agregó en la ronda 2 del backend. Los cuerpos de
error siguen el contrato general: `{"error":"<mensaje en español>"}`.

### `GET /salons/:id/settings` y `PUT /salons/:id/settings`

Configuración operativa del salón. **Leer:** cualquier miembro. **Escribir:**
`owner` o `admin`. El `PUT` lleva el cuerpo **completo** (los cuatro campos;
no es un patch parcial).

```json
{ "timezone": "America/Mexico_City", "open_time": "09:00", "close_time": "19:00", "slot_step_min": 30 }
```

- `timezone`: nombre IANA. Define el **día del salón** (ventana del cierre de
  caja) y el horario de la reserva pública.
- `open_time` / `close_time`: `HH:MM` local (`00:00`–`23:59`), con
  `open_time < close_time`. El salón no ofrece turnos fuera de esa ventana.
- `slot_step_min`: cada cuántos minutos arranca un turno ofrecido (5–240).
- Defaults: `America/Mexico_City`, `09:00`, `19:00`, `30` (también para salones
  creados antes de la migración). Una base sin la fila de settings responde
  igual esos defaults.
- Validación (zona inexistente, hora mal formada, ventana invertida, paso fuera
  de rango): `400` con mensaje claro. Cuerpo inválido: `400 {"error":"body inválido"}`.
- No miembro: `403`. Sin token: `401`.

### `GET /public/salons/:slug/availability` (sin auth)

```
?date=YYYY-MM-DD&service_id=<id>[&professional_id=<id>]
```

```json
{
  "date": "2026-12-01",
  "slots": [
    { "professional_id": "u_...", "starts_at": "2026-12-01T15:00:00Z", "ends_at": "2026-12-01T15:45:00Z" }
  ]
}
```

- `date` se interpreta en la **zona del salón**; `starts_at`/`ends_at` salen en
  UTC RFC3339. La duración de cada turno es la del servicio.
- Solo aparecen arranques **en el futuro** que entren completos dentro de
  `open_time`–`close_time`, sin pisar un turno `booked` de ese profesional (las
  canceladas liberan el hueco).
- Se ofrecen los cuatro pasos (`slot_step_min`) aunque dos huecos consecutivos
  se solapen cuando el paso es menor que la duración: cada hueco es válido por
  separado y, al reservar uno, el listado siguiente ya refleja el solape.
- `professional_id` opcional: limita el resultado a esa persona. Un id que no
  atiende en el salón → `404`. Los profesionales salen del equipo con rol
  `owner`, `admin` o `professional`.
- `service_id` obligatorio; servicio inexistente, de otro salón o inactivo →
  `400`. `date` inválida → `400`. Slug inexistente → `404`. `slots` siempre es
  un array (`[]` si no hay huecos).

### `POST /public/salons/:slug/bookings` (sin auth, rate limit por IP)

```json
{ "service_id": "svc_...", "professional_id": "u_...", "client_name": "Lía",
  "client_phone": "555", "starts_at": "2026-12-01T15:00:00Z", "client_op_id": "op-1" }
```

```json
201 { "booking": { "id": "b_...", "professional_id": "u_...",
                    "starts_at": "2026-12-01T15:00:00Z", "ends_at": "2026-12-01T15:45:00Z",
                    "status": "booked" } }
```

- `ends_at` lo calcula el servidor con la duración del servicio (el cuerpo no
  lleva `ends_at`).
- `professional_id` opcional: sin él se elige el primer miembro del equipo
  (rol `owner`/`admin`/`professional`) sin turno en ese hueco.
- `client_op_id` opcional: reintento idempotente (misma reserva, no duplica).
- El turno debe entrar completo dentro del horario del salón (en su zona) y no
  ser pasado: `400` si no.
- `409` si el hueco ya no está libre o no queda ningún profesional libre. La
  garantía final es la restricción `EXCLUDE` de Postgres: dos reservas
  simultáneas del mismo hueco dejan pasar una y la otra recibe `409`.
- `400` por datos faltantes (`client_name`, `starts_at`, `service_id`),
  `starts_at` no RFC3339, servicio inexistente/inactivo o profesional ajeno.
  Slug o profesional que no atiende: `404`.
- Publica el evento websocket `booking.created` y deja un aviso persistido
  (`kind: "booking"`, título `Nuevo turno`).

### `PATCH /salons/:id/bookings/:bid`

```json
{ "status": "cancelled" }
{ "starts_at": "2026-12-04T15:00:00Z", "ends_at": "2026-12-04T16:00:00Z", "professional_id": "u_..." }
```

```json
200 { "booking": { "id": "b_...", "professional_id": "u_...", "client_name": "Lía",
                    "starts_at": "...", "ends_at": "...", "status": "cancelled" } }
```

- `status` acepta `cancelled`, `no_show` o `done` (cualquier otro valor: `400`).
- Se puede mandar estado y reprogramación en el mismo pedido. Los campos
  ausentes no cambian. `professional_id` vacío no cambia el profesional.
- Permisos: `owner`, `admin` y `reception` sobre cualquier turno; un
  `professional` **solo** sobre los suyos (`403` en los demás). Un turno de otro
  salón no existe para esta ruta: `404`.
- Reprogramar respeta el anti-solape: si el hueco está ocupado, `409` (y el
  turno original no cambia). También aplica duración máxima de 12 h y «no en el
  pasado» (`400`).
- Solo se reprograma un turno `booked`: mover uno cancelado/hecho/no-show da
  `400` (su hueco ya se liberó; reabrirlo tiene que ser una decisión explícita).
- Emite el evento websocket `booking.updated` con el turno completo
  (`id`, `professional_id`, `client_name`, `starts_at`, `ends_at`, `status`) y
  deja un aviso: `Turno cancelado`, `Cliente no se presentó`, `Turno atendido`
  o `Turno reprogramado` (`kind: "booking"`).
- `GET /salons/:id/bookings` y `POST /salons/:id/bookings` no cambian.

### Equipo: `GET/POST/DELETE /salons/:id/members`

Solo `owner` y `admin` (cualquier otro rol, incluido `professional`: `403`).

```json
GET -> 200 { "members": [ { "id": "u_...", "name": "Ana", "email": "ana@mail.com", "role": "owner" } ] }

POST {"email":"beto@mail.com","role":"professional"} -> 201
     { "id": "u_...", "name": "Beto", "email": "beto@mail.com", "role": "professional" }

DELETE /salons/:id/members/:uid -> 204 (sin body)
```

- `members` es siempre un array, ordenado por nombre.
- Roles que se pueden asignar por la API: `admin`, `professional`, `reception`.
  El `owner` suma `admin`, `professional` y `reception`; un `admin` solo suma
  `professional` y `reception` (crear otro admin es escalar privilegios: `403`).
  El rol `owner` no se reparte por la API (`403`).
- La persona **ya debe tener cuenta**: email sin cuenta → `400` con mensaje que
  lo explica. Email ya miembro → `409`. Rol desconocido → `400`.
- `DELETE`: no se puede quitar al **último owner** del salón (`400`); quitar a
  alguien que no es miembro de ese salón → `404`.
- Aislamiento: el id del salón manda; un miembro de otro salón no se lista ni se
  borra desde acá.

### Fotos con URL firmada

La `url` de cada foto (subida, galería) ahora es una URL firmada con
vencimiento de **24 h**:

```
/api/v1/photos/<id>?exp=<unix>&sig=<hmac-sha256-url-safe>
```

- `GET` de esa URL funciona **sin** `Authorization` (es lo que hace un
  `<img src>`); la firma es la capability temporal.
- Sin `exp`/`sig`, con firma alterada, con otro id o vencida: `403 {"error":"firma de la foto inválida o vencida"}`.
  El frontend usa la `url` tal cual y, si vence, vuelve a pedir el listado.
- La clave HMAC se deriva de `JWT_SECRET`; rotar el secreto invalida todas las
  URLs emitidas (las fotos siguen en el storage).
- El resto de las rutas de fotos (subir, listar, borrar) no cambia y sigue
  scopeado por salón y cliente.
- Almacenamiento: con `S3_ENDPOINT` las fotos van al bucket (`S3_BUCKET`); la
  key persistida es `<salon_id>/<client_id>/<photo_id>.webp`. Sin
  `S3_ENDPOINT` van a `MEDIA_DIR` con la misma estructura.

### Otros arreglos de la ronda 2

- **Subida de fotos ilegible**: si `cwebp` no puede decodificar el archivo
  (imagen truncada o corrupta), la respuesta es `400 {"error":"...imagen a webp (¿está dañada?)..."}`
  en vez de un `500`. Si falta el binario `cwebp` sigue siendo un `500`
  (problema del servidor, no del archivo).
- **`Content-Type` sin distinguir mayúsculas** al subir fotos: `IMAGE/PNG`,
  `Multipart/Form-Data` y similares se aceptan igual que en minúsculas.
- **`GET /salons/:id/notifications`**: si falla la consulta del contador de no
  leídas, el listado responde igual `200` con `"unread": 0` y el detalle queda
  en el log del servidor. Los avisos nunca se dejan de mostrar por el badge.
- **Ventana del cierre de caja**: `POST /salons/:id/cash-closes` calcula el
  inicio del turno en la **medianoche local del salón** (`salons.timezone`), no
  en la medianoche UTC. El lock por salón y la regla de «el segundo cierre ve al
  primero» no cambian. `timezone` inválida o ilegible → el cierre falla en vez de
  inventar una ventana.

## Ronda 3 (T9): ficha desde la reserva, email de la ficha, hora del salón y mensajes

Hallazgos E-03, E-04 y E-07 de la auditoría en navegador real. Los códigos HTTP
de estos endpoints no cambian; cambia el cuerpo (mensajes) y lo que se persiste.

### `POST /public/salons/:slug/bookings`: deja la ficha del cliente (E-03)

- Después de crear el turno, la reserva pública busca la ficha del visitante en
  **ese** salón (por teléfono normalizado: solo dígitos, así que `11 5555-1234` y
  `1155551234` son la misma persona) y, si no existe, la crea con el nombre y el
  teléfono de la reserva (sin email ni notas).
- La misma persona reservando de nuevo **no duplica** la ficha. Sin teléfono, la
  búsqueda es por nombre exacto y también dentro del salón.
- Si la ficha ya existe no se toca: una reserva web no pisa notas, fotos ni un
  nombre corregido en el mostrador.
- Todo va scopeado por `salon_id`: el mismo teléfono en otro salón es otra ficha
  y no se lee ni se modifica.
- Si la ficha falla (DB caída, dato raro), **la reserva igual entra**: la
  respuesta `201` no cambia y el detalle queda en el log del servidor. La ficha
  se puede crear a mano desde el mostrador.
- El cuerpo de la respuesta es el mismo de la ronda 2.

### `POST /salons/:id/clients`: email opcional (E-03)

```json
{ "name": "Lía", "phone": "11 5555-1234", "email": "lia@mail.com", "notes": "" }

201 { "id": "c_...", "salon_id": "s_...", "name": "Lía", "phone": "11 5555-1234",
      "email": "lia@mail.com", "notes": "", "created_at": "2026-11-21T23:00:00Z" }
```

- `email` es opcional; se normaliza a minúsculas y sin espacios. Vacío se guarda
  como texto vacío (nunca `null`) y el campo aparece siempre, tanto en el alta
  como en `GET /salons/:id/clients` y en la ficha de un turno.
- Email sin forma de email o de más de 254 caracteres →
  `400 {"error":"Email inválido"}` (misma regla de forma que el email de una
  cuenta). El mensaje no lleva el prefijo interno del error de dominio.
- Permisos y códigos no cambian: `owner`, `admin` y `reception` escriben; el
  `professional` solo lee.

### Avisos de turno en la hora del salón (E-04)

El **texto** de los avisos pasa a la hora local del salón (`salons.timezone`) y
nombra la zona:

```
Nuevo turno
Lía · 26/11 11:00 (America/Mexico_City) (reserva web)
```

- Aplica a `booking.created` (reserva pública **y** alta de staff) y a
  `booking.updated` (cancelar, no-show, atendido, reprogramar).
- Zona ilegible (fila de settings rota): el aviso sale igual, con la hora UTC y
  la etiqueta `(UTC)`. Un aviso con hora explícita es mejor que ninguno.
- **El payload del websocket no cambia**: `starts_at`/`ends_at` siguen en UTC
  RFC3339 y las pantallas siguen formateando por su cuenta.
- Los títulos (`Nuevo turno`, `Turno cancelado`, `Cliente no se presentó`,
  `Turno atendido`, `Turno reprogramado`) y el `kind: "booking"` no cambian.

### Mensajes del equipo en español limpio (E-07)

`GET/POST/DELETE /salons/:id/members` mantiene los mismos códigos HTTP, pero el
cuerpo ya no lleva el prefijo interno del error (`validación:`, `ya existe:`) ni
nombres de rol en inglés:

| Caso | HTTP | `error` |
|---|---|---|
| La persona ya es miembro con ese rol | `409` | `Ya tiene ese rol` |
| Ya es miembro con otro rol | `409` | `Ya es parte del equipo como administración` |
| El email no tiene cuenta | `400` | `La persona todavía no tiene cuenta` |
| Rol desconocido | `400` | `Rol desconocido: usá dueño, administración, profesional, recepción o cliente` |
| Email vacío | `400` | `El email es obligatorio` |
| Quitar al último dueño | `400` | `No se puede quitar al último dueño` |

Los roles válidos para `POST` siguen siendo `admin`, `professional` y
`reception` (valores de la API); lo que cambia es cómo se **nombran** en un
mensaje y en el detalle de «ya es parte del equipo». `owner`/`admin` siguen
siendo los roles que administran el equipo, y los permisos no cambian.

## Cambios de comportamiento en endpoints existentes

- **`POST /salons/:id/sales`**: el precio unitario **lo pone el catálogo**. El
  `unit_price_cents` del body se usa solo para validar el pedido (fuera de rango →
  `400`); el que se cobra y se persiste es el `price_cents` del servicio. Un
  servicio inactivo ya no se vende (`400`). El `professional_id` tiene que ser
  personal del salón con rol `owner`, `admin` o `professional` (`400` si no).
- **`POST /salons/:id/bookings`**: mismo requisito de `professional_id`; además el
  turno no puede durar más de 12 h ni empezar en el pasado.
- **`POST /salons/:id/cash-closes`**: el cierre corre en una transacción con lock
  por salón. Dos cierres concurrentes se ordenan: el segundo ve al primero como
  cierre anterior y su ventana arranca donde terminó el otro, así el efectivo se
  contabiliza **una sola vez**. Sin cierre previo, la ventana arranca en la
  medianoche **local del salón** (ronda 2, ver arriba).
  `counted_cents` y `system_cash_cents` tienen tope de 2.000.000.000 centavos.
- **`GET /salons/:id/sales` / `GET /salons/:id/dashboard`**: las sumas se calculan
  con `SUM::bigint`, así que ventas grandes ya no rompen el panel con un error de
  rango de Postgres.
- **`POST /auth/register`**: email normalizado a minúsculas (el duplicado con
  mayúsculas da `409`) y clave de 8 a 72 bytes (`400`). Email duplicado
  concurrente → `409`, no `500`.
- **`POST /auth/login`**: email normalizado; usuario inexistente y clave
  incorrecta responden igual (`401`) y en el mismo tiempo (comparación bcrypt
  falsa cuando el usuario no existe).
- **`GET /api/v1/ws?token=...`**: el token ya no queda escrito en los logs de
  acceso (`token=***`), y el `Origin` se valida contra `CORS_ORIGINS`.

## Migraciones

Al arrancar se aplican los `.sql` de `migrations/` en orden léxico, **una sola
vez cada uno**, con registro en:

```sql
CREATE TABLE schema_migrations (version TEXT PRIMARY KEY, applied_at TIMESTAMPTZ NOT NULL DEFAULT now());
```

El runner toma `pg_advisory_lock` antes de mirar/ aplicar nada: dos instancias
arrancando a la vez se serializan y ambas terminan bien (la segunda no hace
nada). Todas las migraciones son re-ejecutables por sí mismas (`IF NOT EXISTS` /
`DO` con guardas de catálogo), así que una base ya migrada sin
`schema_migrations` también se pone al día sin errores.

Migraciones de la ronda 2:

- `011_salon_settings.sql`: columnas `salons.timezone`, `open_time`, `close_time`
  y `slot_step_min` con defaults, más el `CHECK` del paso (5–240). Idempotente
  con guardas de `information_schema` / `pg_constraint`.
- `012_indexes.sql`: `bookings (salon_id, starts_at)` y `memberships (salon_id)`
  (finding B-19). `CREATE INDEX IF NOT EXISTS`.
- `013_clients_contact.sql` (ronda 3): `clients.email` (default `''`, con
  `CHECK` de 254), `clients.phone_norm` como columna **generada** (`STORED`, solo
  dígitos: la misma regla que `domain.NormalizePhone`) e índice parcial
  `clients (salon_id, phone_norm) WHERE phone_norm <> ''` para la búsqueda de la
  reserva pública. Guardas de `information_schema` / `pg_constraint` /
  `IF NOT EXISTS`: una base con fichas existentes calcula su `phone_norm` sola.

Las tres se probaron aplicando todos los `.sql` en orden **dos veces** con
`psql -v ON_ERROR_STOP=1` sobre una base limpia y sobre una ya migrada, y el
binario arranca contra esas bases (el runner registra las 13 versiones en
`schema_migrations`).
