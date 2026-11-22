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
| `SMTP_HOST` | no | vacío | Servidor SMTP de los correos a clientes. **Vacío = email deshabilitado** (no-op: no se manda nada ni se reclaman recordatorios). |
| `SMTP_PORT` | no | `25` | Puerto SMTP (`1025` con el Mailpit del compose). |
| `SMTP_USER` / `SMTP_PASS` | no | — | Autenticación SMTP PLAIN (opcional). |
| `SMTP_FROM` | no | `turnos@salon.local` | Remitente de los correos. |
| `SMTP_TLS` | no | `none` | `none`, `starttls` o `tls` (implícito). Un valor desconocido impide arrancar. |
| `REMINDER_INTERVAL` | no | `60s` | Cada cuánto la tarea mira la cola de recordatorios (duración de Go, > 0). |
| `TRUSTED_PROXIES` | no | vacío | De qué peers se acepta `X-Forwarded-For` (la IP del cliente para el rate limit y el log). **Vacío = no se confía en nadie**: la IP del cliente es el peer directo y la cabecera se ignora. `private` = los rangos privados históricos (`127.0.0.1`, `::1`, `10.0.0.0/8`, `172.16.0.0/12`, `192.168.0.0/16`). O una lista de IPs/CIDR separada por comas (`172.18.0.0/16,203.0.113.7`). Un valor que no sea IP, CIDR ni `private` impide arrancar. |

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
| Body JSON (todas las rutas salvo la subida de fotos) | 1 MB | `413 {"error":"body demasiado grande"}`. Se aplica **por ruta**, no por `Content-Type`: un `multipart/form-data` o `image/png` falseado en un endpoint JSON no lo apaga. Si el body no declara `Content-Length`, el tope corta la lectura y la ruta responde `400 body inválido`. |
| Subida de foto (`POST /salons/:id/clients/:cid/photos`) | 8 MB (archivo) + 1 MB de overhead del multipart | `400` (no lleva el tope JSON: tiene el suyo) |
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

### IP del cliente y proxies confiables (`TRUSTED_PROXIES`)

La IP que usan el rate limit y el log de acceso sale del peer TCP, salvo que ese
peer esté en `TRUSTED_PROXIES`: recién ahí se lee `X-Forwarded-For`.

- `TRUSTED_PROXIES` **vacío** (default): `X-Forwarded-For` se ignora siempre. Un
  cliente que inventa la cabecera no puede elegir su cubo del rate limit.
- **Detrás de un reverse proxy hay que setearla** (por ejemplo la IP del nginx o
  el CIDR de la red Docker, `172.18.0.0/16`), si no todas las personas llegan
  con la IP del proxy y comparten el mismo límite de intentos.
- `TRUSTED_PROXIES=private` mantiene el comportamiento de la fase 5 (loopback +
  rangos privados).
- Un valor inválido impide arrancar y el error nombra la variable.

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
{ "staff": [ { "id": "u_...", "name": "Bruno", "role": "professional", "attends": true } ] }
```

- Incluye **solo a quien atiende** (F2a, ver «Quién atiende»): rol
  `professional`, o `owner`/`admin` con `attends = true`. `reception` y `client`
  no aparecen nunca, aunque la fila tenga la marca encendida.
- `attends` explica por qué está en la lista: el profesional atiende por rol y
  el dueño/administración por la marca.
- Orden: por nombre.
- `staff` es siempre un array.
- No miembro → `403`. Sin token → `401`.
- Si la consulta de la membresía falla por un problema del servidor (DB caída),
  la respuesta es `500 {"error":"error interno"}` y el detalle queda en el log:
  no se disfraza de `403` (que era el bug de la revisión de la fase 5).

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
  atiende en el salón → `404`. Los profesionales salen de quien atiende (F2a):
  rol `professional`, o `owner`/`admin` con `attends = true`.
- Un salón donde **nadie** atiende responde `200` con `"slots": []`, no un
  error: no hay agenda que ofrecer.
- `service_id` obligatorio; servicio inexistente, de otro salón o inactivo →
  `400`. `date` inválida → `400`. Slug inexistente → `404`. `slots` siempre es
  un array (`[]` si no hay huecos).

### `POST /public/salons/:slug/bookings` (sin auth, rate limit por IP)

```json
{ "service_id": "svc_...", "professional_id": "u_...", "client_name": "Lía",
  "client_phone": "555", "starts_at": "2026-12-01T15:00:00Z", "client_op_id": "op-1",
  "client_email": "lia@mail.com" }
```

```json
201 { "booking": { "id": "b_...", "professional_id": "u_...",
                    "starts_at": "2026-12-01T15:00:00Z", "ends_at": "2026-12-01T15:45:00Z",
                    "status": "booked" } }
```

- `ends_at` lo calcula el servidor con la duración del servicio (el cuerpo no
  lleva `ends_at`).
- `professional_id` opcional: sin él se elige el primer miembro del equipo que
  atiende (la misma regla de «Quién atiende») y está libre en ese hueco. Sin
  gente que atienda: `409`.
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
- `client_email` **opcional** (ronda 3, T12): ver «Email a clientes» más abajo.
  No aparece en el JSON de la reserva.

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
GET -> 200 { "members": [ { "id": "u_...", "name": "Ana", "email": "ana@mail.com",
                            "role": "owner", "attends": true } ] }

POST {"email":"beto@mail.com","role":"professional"} -> 201
     { "id": "u_...", "name": "Beto", "email": "beto@mail.com",
       "role": "professional", "attends": true }

PATCH {"attends": false} -> 200 (el miembro, con la marca nueva)

DELETE /salons/:id/members/:uid -> 204 (sin body)
```

- `members` es siempre un array, ordenado por nombre.
- `attends` (F2a) dice si esa persona **atiende clientes**; sale en el listado,
  en el alta y en el `PATCH`. Ver «Quién atiende» más abajo.
- `POST` acepta `attends` **opcional**: sin él rige el default del rol —
  `professional` nace atendiendo, `admin` y `reception` no
  (`{"role":"admin","attends":true}` da de alta una administración que también
  atiende). `attends` explícito que contradice el rol es `400` con los mismos
  mensajes del `PATCH`.
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

### Quién atiende (F2a): `attends` y `PATCH /salons/:id/members/:uid`

Hasta la fase 5 el rol alcanzaba: `owner`, `admin` o `professional` aparecían en
la agenda y en el select de profesional. Un dueño que solo administra inflaba la
disponibilidad y recibía turnos. Ahora:

| Rol | Atiende |
|---|---|
| `professional` | **siempre** (es su oficio) |
| `owner` / `admin` | solo con `attends = true` (el dueño-barbero) |
| `reception` / `client` | **nunca** |

La misma regla decide la agenda (`GET /salons/:id/staff`), la disponibilidad
pública, la asignación automática de la reserva pública y la validación del
`professional_id` de un turno o de una venta. Un salón donde nadie atiende no
ofrece turnos (`"slots": []`) y la reserva pública responde `409`.

`PATCH /salons/:id/members/:uid` enciende o apaga la marca de un miembro. Solo
`owner`/`admin` del salón; el cuerpo es obligatorio y la respuesta es el miembro.

```json
PATCH {"attends": true} -> 200 { "id": "u_...", "name": "Ana", "email": "ana@mail.com",
                                 "role": "owner", "attends": true }
```

| Caso | HTTP | `error` |
|---|---|---|
| `professional` con `attends: false` | `400` | `Las profesionales siempre atienden` |
| `reception` / `client` (cualquier valor) | `400` | `Ese rol no atiende clientes` |
| Miembro de **otro** salón (o inexistente) | `404` | `no encontrado` |
| Quien pide no es owner/admin del salón | `403` | `sin permiso` / `sin acceso al salón` |

- El dueño puede apagarse a sí mismo: deja de atender sin dejar de administrar.
- Desde F2c el `PATCH` también acepta `trade`; sin **ninguno** de los dos campos
  → `400 {"error":"Indicá qué cambiar"}` (antes: `body inválido`). Ver «Oficio y
  servicios por profesional».
- El alta de un salón deja al `owner` con `attends = true` (salón de una sola
  persona) y agregar un `professional` lo deja atendiendo.

## Fase 6 — F2c: oficio y servicios por profesional

Hasta acá la agenda asumía que **todo el equipo hacía todos los servicios**: la
disponibilidad ofrecía a cualquiera que atendiera y la reserva pública asignaba
al primero libre. Ahora un turno público solo se ofrece y se asigna a una
persona reservable (F2a) **que hace ese servicio**. El oficio (barbero,
estilista, colorista...) es un texto visible, no un permiso: la clienta sigue
eligiendo solo el servicio.

### `PATCH /salons/:id/members/:uid` — ahora con `trade`

```json
PATCH {"trade": "Barbero"}          -> 200 (el miembro, con oficio y servicios)
PATCH {"attends": true, "trade": "Estilista"} -> 200 (los dos a la vez)
PATCH {}                            -> 400 {"error":"Indicá qué cambiar"}
```

- Cuerpo: `attends` y `trade` son **opcionales**, pero al menos uno tiene que
  venir. Los campos ausentes no cambian.
- `trade`: 1–40 caracteres, se guarda recortado y **sin saltos de línea**
  (`400 {"error":"El oficio no puede tener saltos de línea"}` / `400 {"error":"El oficio no puede superar los 40 caracteres"}`).
  Vacío borra el oficio.
- `trade` solo en quien atiende: recepción/cliente, o un dueño/administración
  con `attends = false` → `400 {"error":"Ese rol no tiene oficio"}`.
- Permisos, `404` de un miembro de otro salón y `403` sin permiso no cambian.
- La respuesta es el miembro completo (`id`, `name`, `email`, `role`,
  `attends`, `trade`, `service_ids`).

### `PUT /salons/:id/members/:uid/services`

Reemplaza la **lista completa** de servicios que hace esa persona.

```
PUT {"service_ids":["svc_...","svc_..."]} -> 200 {"service_ids":["svc_..."]}
PUT {"service_ids":[]}                    -> 200 {"service_ids":[]}
```

- Solo `owner`/`admin` del salón (`403`). Tiene que ser un miembro reservable:
  si no, `400 {"error":"Ese rol no hace servicios"}`.
- Un `service_id` inexistente **o de otro salón** → `400 {"error":"Servicio inválido"}`
  y la lista anterior queda intacta (la escritura es un reemplazo atómico: se
  valida todo antes de tocar la tabla y el `UPDATE` corre en una transacción).
- Lista vacía permitida: esa persona no hace ninguno. Se devuelve sin repetidos
  y ordenada por id.
- Miembro de otro salón → `404`. Sin token → `401`.

### Campos nuevos en las lecturas

```json
GET /salons/:id/members -> { "members": [
  { "id": "u_...", "name": "Bruno", "email": "bruno@mail.com", "role": "professional",
    "attends": true, "trade": "Barbero", "service_ids": ["svc_..."] } ] }

GET /salons/:id/staff -> { "staff": [
  { "id": "u_...", "name": "Bruno", "role": "professional", "attends": true, "trade": "Barbero" } ] }
```

- `members` lleva `trade` y `service_ids` (siempre array; vacío si no hace
  ninguno). Es la fuente del conteo «N profesionales lo hacen» de la pantalla de
  servicios (owner/admin).
- `staff` lleva solo `trade`: lo consulta cualquier miembro del salón y **no**
  expone qué hace cada quien.
- Los JSON de turno de staff (`POST /salons/:id/bookings`, `GET .../bookings` y
  el `PATCH`) agregan `service_id`, que puede ser `null`: un turno agendado a
  mano desde el calendario no exige servicio.

### Alta de un servicio: `professional_ids` opcional

```json
POST /salons/:id/services
{ "name": "Keratina", "duration_min": 60, "price_cents": 250000,
  "professional_ids": ["u_..."] }
```

- Sin `professional_ids` el servicio **nace sin nadie que lo haga**: a partir de
  F2c eso significa que no se ofrecen turnos para ese servicio hasta asignarlo
  desde Equipo (la pantalla de servicios avisa «Nadie hace este servicio»). Es
  una excepción consciente: el aviso es lo que evita que el salón se quede sin
  turnos sin darse cuenta.
- `professional_ids`: máximo 50 (`400 {"error":"No se pueden asignar más de 50 profesionales"}`),
  cada id tiene que ser alguien que **atiende** en ese salón
  (`400 {"error":"Profesional inválido"}`). Todos los ids se validan antes de
  guardar: un alta rechazada no deja el servicio creado.
- Un profesional nuevo nace sin servicios y se le asignan desde Equipo.

### Disponibilidad y reserva pública: filtradas por servicio

- `GET /public/salons/:slug/availability`: los profesionales que se ofrecen son
  los reservables (F2a) **que hacen el servicio**. Un `professional_id` que no
  lo hace responde `404` (no existe para esa búsqueda).
- **Si nadie hace el servicio** la respuesta es `200 {"slots": []}`, no un
  error: no hay agenda que ofrecer.
- `POST /public/salons/:slug/bookings`: sin `professional_id` se asigna el
  primer reservable **que hace el servicio** y está libre. Si no queda ninguno
  → `409` (nunca `500`). Con `professional_id` de alguien que no hace ese
  servicio → `400 {"error":"... el profesional elegido no hace ese servicio"}`;
  un id que no atiende sigue siendo `404`.
- El turno público guarda `bookings.service_id` (el servicio reservado). Si el
  servicio se borra, el turno queda sin servicio (`ON DELETE SET NULL`): no se
  pierde la historia de la agenda.
- La reserva de staff (`POST /salons/:id/bookings`) **no** exige servicio y no
  cambia: el calendario agenda turnos sin catálogo.

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

## Fase 6 — F2d1: horario semanal del salón y de cada profesional

Hasta acá el salón tenía **una** ventana (`open_time`/`close_time` de
`/settings`) que valía todos los días y cada profesional trabajaba todo lo que
el salón abría. Ahora el salón puede configurar la ventana de **cada día** y cada
persona sus **tramos** (con descansos: los huecos entre tramos).

Convenciones: día ISO **1..7** (1 = lunes, 7 = domingo) calculado en la zona
horaria del salón; horas `"HH:MM"` de 24 h.

**La ausencia de filas es comportamiento, no dato faltante** (la migración 017 no
hace backfill):

- Salón sin ninguna fila → la ventana única de `/settings` vale los 7 días (lo
  de hoy). Con una o más filas, **el día sin fila está cerrado**.
- Profesional sin ninguna fila → sigue el horario del salón. Con filas, solo
  trabaja en esos tramos: un día sin tramo es día libre, hasta 4 tramos por día,
  sin solaparse.
- La ventana efectiva de una persona un día = ventana del salón ∩ sus tramos,
  recortada.

### `GET /salons/:id/hours` y `PUT /salons/:id/hours`

```json
GET -> 200 { "days": [ { "weekday": 1, "open": "09:00", "close": "18:00" },
                       { "weekday": 2, "open": "10:00", "close": "14:00" } ],
             "custom": true }

PUT { "days": [ { "weekday": 1, "open": "09:00", "close": "18:00" } ] } -> 200 (igual forma)
PUT { "days": [] } -> 200  // vuelve al horario único (custom=false, 7 días)
```

- **Cualquier miembro** lee (la agenda lo necesita); solo `owner`/`admin` escribe
  (`403`); sin token `401`; un ajeno al salón `403` (el `salon_id` de la ruta no
  es la credencial, la membresía sí).
- `custom: false` = no hay filas: se devuelve la ventana única expandida a los
  **7 días**. Un salón nunca responde una semana vacía por no haber
  configurado nada.
- El `PUT` reemplaza el horario **entero** en una transacción (o queda el nuevo o
  queda el anterior); la lista vacía borra las filas. Se valida antes de
  escribir.
- Errores exactos (400): `{"error":"Horario inválido: el inicio debe ser anterior al fin"}`,
  `{"error":"Los tramos de un mismo día no pueden solaparse"}` (no aplica al
  salón: un día tiene una sola ventana), `{"error":"Máximo 4 tramos por día"}`
  (tampoco aplica), `{"error":"Día inválido"}`, `{"error":"Horario inválido: usá
  horas HH:MM"}` y `{"error":"El día está repetido"}` (el salón manda un día una
  sola vez).
- `GET /salons/:id/settings` **no cambia**: sigue siendo la ventana única, que es
  la que vale cuando `custom = false`.

### `GET /salons/:id/members/:uid/hours` y `PUT` (mismo body)

```json
GET -> 200 { "segments": [ { "weekday": 2, "start": "09:00", "end": "13:00" },
                           { "weekday": 2, "start": "14:00", "end": "18:00" } ],
             "custom": true }

PUT { "segments": [] } -> 200  // vuelve a "igual que el salón"
```

- `GET`: `owner`/`admin` o **la propia persona** (`200`). Otra profesional del
  mismo salón → `403`; un miembro de otro salón → `404`.
- `PUT`: solo `owner`/`admin` (`403`), también si es el horario propio. Solo
  miembros reservables: recepción/cliente o un dueño/administración sin
  `attends` → `400 {"error":"Ese rol no tiene horario"}`. Miembro de otro salón
  → `404`.
- Validación: formato `HH:MM`, inicio < fin, hasta **4 tramos por día**, sin
  solaparse (un duplicado es un solape). Los mensajes son los del salón, más
  `{"error":"Horario inválido: usá horas HH:MM"}`.
- Reemplazo atómico: un cuerpo inválido deja los tramos anteriores intactos.
- Un tramo que se sale de la ventana del salón **no es un error**: se recorta al
  calcular (el salón manda).

### `GET /public/salons/:slug` agrega `hours`

```json
GET /api/v1/public/salons/navajas-oro -> 200 {
  "salon": { "id": "s_...", "slug": "navajas-oro", "name": "Navajas de Oro" },
  "services": [ ... ],
  "hours": [ { "weekday": 1, "open": "09:00", "close": "19:00" } ]
}
```

- Solo los días **abiertos**, ya expandidos: la landing pone «Cerrado» en los que
  no están y resalta el día de hoy. Un salón sin horario propio devuelve los 7
  días de la ventana única.

### Disponibilidad y reserva pública: la ventana efectiva manda

- `GET /public/salons/:slug/availability` ofrece un hueco solo si el turno
  **entra entero** en la ventana efectiva de esa persona ese día: no se ofrece
  nada que cruce un descanso ni el cierre. Un día cerrado del salón no ofrece
  nada (`200 {"slots":[]}`), y una persona con horario propio no aparece en sus
  días libres.
- `POST /public/salons/:slug/bookings`: la asignación automática aplica la misma
  regla (saltea a quien no trabaja en ese horario). Sin nadie con ventana ese
  día → `409 {"error":"... no hay profesional libre en ese horario"}` (nunca
  `500`), igual que un día cerrado. Con `professional_id` de alguien que no
  trabaja ese día/horario → `409 {"error":"... el horario ya no está disponible"}`;
  un turno fuera de la ventana del salón sigue siendo `400`.
- El **día de la semana es el del salón**, no el de UTC: un domingo 23:30 en
  CDMX ya es lunes en UTC y se agenda como domingo.
- La reserva de staff (`POST /salons/:id/bookings`) **no se restringe por
  horario**: el calendario sigue agendando a mano fuera de horario y solo el
  choque de turnos la frena (como antes).

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

## Email a clientes (T12)

La reserva pública acepta un email **opcional** y la API manda dos correos SMTP:
la confirmación del turno (al agendar) y un recordatorio ~24 h antes.

```json
POST /api/v1/public/salons/:slug/bookings
{ "service_id": "svc_...", "client_name": "Lía", "starts_at": "2026-12-01T15:00:00Z",
  "client_email": "Lia@Mail.com" }

201 { "booking": { "id": "b_...", "professional_id": "u_...", "starts_at": "...",
                    "ends_at": "...", "status": "booked" } }
```

- `client_email` es opcional y **no** viaja en la respuesta: el JSON del turno no
  cambia (el email es dato de contacto, no del turno).
- Se normaliza (minúsculas, sin espacios) y se valida con la misma regla que el
  email de una ficha/una cuenta. Forma inválida o más de 254 caracteres →
  `400 {"error":"Email inválido"}` y **la reserva no entra**.
- Vacío o ausente = reserva anónima: no se manda nada ni se guarda nada.
- Un reintento con el mismo `client_op_id` devuelve la reserva original y **no**
  manda una segunda confirmación.
- La confirmación se envía en segundo plano con un tope de 15 s: un SMTP lento o
  caído no demora ni hace fallar la respuesta `201` (si falla, queda en el log
  del servidor sin la dirección).
- Guardar la dirección para el recordatorio es best-effort: si falla, la reserva
  entra igual (se pierde el recordatorio, no el turno).

**Recordatorio.** La tarea periódica (`REMINDER_INTERVAL`, 60 s por defecto)
reclama atómicamente los turnos que cumplen TODO esto:

- estado `booked`, `starts_at` en el futuro y dentro de las próximas 24 h;
- la reserva se creó con al menos 24 h de anticipación: una reserva hecha para
  dentro de dos horas ya recibió su confirmación y no lleva recordatorio;
- recordatorio no enviado, con menos de 3 intentos y sin un reclamo vivo (un
  reclamo vence a los 5 minutos, así un proceso caído no pierde el turno).

Lo enviado se marca (`reminder_sent_at`) y no se repite. Un fallo de envío no se
marca: vuelve en un tick posterior, y nunca frena al resto del lote.

**Copia.** Español, asunto `Turno confirmado · <salón>` /
`Recordatorio de tu turno · <salón>`, nombre del salón, nombre de la persona,
fecha y hora en la zona del salón con el nombre de la zona
(`01/12 09:00 (America/Mexico_City)`) y una línea para cambiar o cancelar
(ofreciendo contactar al salón: la tabla `salons` no tiene datos de contacto,
así que no se inventan). Sin ids ni tokens. Texto plano + HTML simple
escapado, sin imágenes remotas. Si la zona del salón no se puede leer, la hora
sale en UTC con la etiqueta `(UTC)` y el correo sale igual.

**Configuración.** `SMTP_HOST` vacío deshabilita todo (no-op logueado una vez al
arrancar como `email deshabilitado`). Ver la tabla de variables y
[`docs/ops/environment.md`](../ops/environment.md). Con Docker Compose los
defaults apuntan al Mailpit del stack (UI `http://localhost:8025`).

## Cambios de comportamiento en endpoints existentes

- **`POST /salons/:id/sales`**: el precio unitario **lo pone el catálogo**. El
  `unit_price_cents` del body se usa solo para validar el pedido (fuera de rango →
  `400`); el que se cobra y se persiste es el `price_cents` del servicio. Un
  servicio inactivo ya no se vende (`400`). El `professional_id` tiene que ser
  personal del salón que **atiende** (`400` si no): rol `professional`, o
  `owner`/`admin` con `attends = true` (F2a).
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
- **Tope de body (todas las rutas)**: pasa a aplicarse **por ruta** y no por
  `Content-Type`. Un body de más de 1 MB con `Content-Length` declarado ahora
  responde `413 {"error":"body demasiado grande"}` (antes el JSON daba `400` y
  el `multipart/form-data`/`image/png` falseado se saltaba el tope por completo).
  La subida de fotos conserva su tope de 8 MB. Sin `Content-Length`, el tope
  corta la lectura y la respuesta sigue siendo `400`.
- **IP del cliente**: sale del peer TCP y `X-Forwarded-For` solo se lee si el
  peer está en `TRUSTED_PROXIES` (vacío = no se confía en nadie). Detrás de un
  reverse proxy hay que configurarla o el rate limit cuenta a todo el mundo con
  la misma IP.
- **`GET /salons/:id/staff`**: una falla del servidor al leer la membresía ya no
  se disfraza de `403`: responde `500 {"error":"error interno"}` y el detalle
  queda en el log.
- **`GET /public/salons/:slug`**: agrega `hours` (los días abiertos, expandidos)
  y la reserva pública respeta la ventana efectiva de cada persona (F2d1). Un
  turno en un día cerrado del salón pasa a ser `409` (antes era `400` por estar
  fuera de horario).

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

Migración de la ronda 3 (T12):

- `014_booking_emails.sql`: tabla `booking_emails(booking_id TEXT PRIMARY KEY
  REFERENCES bookings(id) ON DELETE CASCADE, salon_id TEXT NOT NULL, email TEXT
  NOT NULL CHECK (char_length(email) <= 254), reminder_sent_at TIMESTAMPTZ,
  reminder_attempts INTEGER NOT NULL DEFAULT 0, reminder_claimed_at
  TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now())` e índice parcial
  `booking_emails_due_idx` para la consulta de vencimiento. Va en tabla aparte
  para no tocar las SELECT de reservas ni `scanBooking`: un turno sin email no
  ocupa una fila y el JSON de un turno no cambia. `booking_id` es `TEXT` porque
  `bookings.id` es `TEXT`. Idempotente (`CREATE TABLE/INDEX IF NOT EXISTS` +
  guarda `DO` del `CHECK`).

Migración de la fase 6 (F2a):

- `015_membership_attends.sql`: `memberships.attends BOOLEAN NOT NULL DEFAULT
  false` y backfill `true` para las filas existentes con rol `owner`, `admin` o
  `professional` (recepción y cliente quedan en `false`). El backfill corre
  **solo** cuando la columna se crea, así que una segunda pasada no vuelve a
  encender a quien se apagó desde la pantalla de equipo. La guarda mira
  `current_schema()`, no cualquier tabla `memberships` del catálogo.

Migración de la fase 6 (F2c):

- `016_professional_services.sql`:
  - `memberships.trade TEXT NOT NULL DEFAULT ''` con `CHECK (char_length(trade) <= 40)`
    (nombre `memberships_trade_len`).
  - Tabla `professional_services(salon_id, user_id, service_id)` con PK
    `(salon_id, user_id, service_id)`, FK `(user_id, salon_id)` a
    `memberships` y FK `service_id` a `services(id)`, **las dos con
    `ON DELETE CASCADE`** (si la persona sale del salón o el servicio se borra,
    las filas se van con ellos), e índice `(salon_id, service_id)`.
  - `bookings.service_id TEXT NULL` con FK a `services(id) ON DELETE SET NULL`.
  - **Backfill solo al crear la tabla**: cada miembro reservable (profesional, o
    dueño/administración con `attends = true`) × cada servicio de su salón. No
    cambia lo que hay hoy. Una segunda pasada no duplica ni repisa lo que se
    asignó o se quitó desde Equipo, y un servicio creado después no se asigna
    solo.
  - Guardas miradas en `current_schema()` (columna, CHECK y FK): una segunda
    ejecución no hace nada y una tabla homónima de otro esquema no la confunde.

Migración de la fase 6 (F2d1):

- `017_weekly_hours.sql`:
  - `salon_weekly_hours(salon_id, weekday SMALLINT CHECK 1..7, open_time,
    close_time)`, PK `(salon_id, weekday)` (una ventana por día), FK a `salons`
    `ON DELETE CASCADE`, `CHECK (open_time < close_time)` y un `CHECK` de formato
    `HH:MM` (las horas son `TEXT`, así que la comparación lexicográfica es la del
    reloj).
  - `professional_hours(salon_id, user_id, weekday SMALLINT CHECK 1..7,
    start_time, end_time)`, PK `(salon_id, user_id, weekday, start_time)`, FK
    `(user_id, salon_id)` a `memberships` **`ON DELETE CASCADE`** (si la persona
    sale del equipo, su horario se va con ella), `CHECK (start_time < end_time)` y
    el mismo `CHECK` de formato. Índice `(salon_id, user_id, weekday)`.
  - **Sin backfill**: la ausencia de filas es el comportamiento actual (ventana
    única y la persona sigue al salón), así que una base migrada se comporta
    igual hasta que alguien configure horarios.

Las 17 migraciones se probaron aplicándolas dos veces con
`psql -v ON_ERROR_STOP=1` sobre una base limpia y sobre una ya migrada, y el
binario arranca contra esas bases. El backfill de la 016 se probó además sobre
una base migrada **solo hasta la 015**, con dueño/administración/profesional/
recepción, dos servicios y un turno ya creados: los tres reservables quedan con
los dos servicios, recepción con ninguno y el turno con `service_id` NULL.
