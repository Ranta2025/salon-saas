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
| `APP_URL` | no | `http://localhost:8081` | Base pública del frontend (la web del compose). Con ella se arma el enlace de la invitación de equipo: `<APP_URL>/invitacion#<token>`. Tiene que ser una URL `http(s)` absoluta con host; un valor inválido **impide arrancar** y la barra final se quita. |
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
| Invitación pública (`POST /public/invitations/lookup` y `/accept`) | 20 intentos por IP por minuto | `429` (mismo mensaje). Son las otras escrituras sin sesión (F2g1). |
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
- **Zona congelada por la nómina (F3.3)**: con al menos una liquidación (abierta
  o pagada), un `PUT` que **cambia** `timezone` responde `409 {"error":"No se
  puede cambiar la zona horaria: ya hay liquidaciones de nómina calculadas con
  la zona actual"}` y no aplica **ningún** campo. Con la misma zona, el resto de
  la configuración se sigue guardando; borrar la última liquidación abierta
  vuelve a permitir el cambio.
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
- `GET /salons/:id/bookings` y `POST /salons/:id/bookings` no cambian de forma,
  salvo por el campo `covered_from` de F2d3 (ver «Quién tenía el turno»).

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
- `DELETE`: desde F2g2 **da de baja** (marca `ended_at`), no borra la fila: ver
  «Fase 6 — F2g2: dar de baja en vez de borrar». No se puede dar de baja al
  **último owner** del salón (`400 {"error":"No se puede quitar al último
  dueño"}`); a alguien que ya está dado de baja o que no es miembro de ese salón
  → `404`. La respuesta sigue siendo `204`.
- `POST` con el email de una persona **dada de baja** la reactiva con el rol
  pedido, pero solo si el actor podría darla de baja (misma regla de rol); la
  administración no reactiva así a un `admin` ni a un `owner` → `403
  {"error":"sin permiso"}`. Ver «Fase 6 — F2g2».
- `GET` agrega `active` y `ended_at` por miembro (F2g2) y devuelve también a
  quien fue dado de baja.
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

## Fase 6 — F2d2: vacaciones y días libres puntuales por profesional

Una **ausencia** es un período `[starts_at, ends_at)` en UTC de **una** persona
reservable (rol `professional`, o dueño/administración con `attends = true`:
F2a). La UI convierte desde la zona del salón; un **día completo** es de `00:00`
a `00:00` del día siguiente en esa zona. El período es semiabierto igual que los
turnos: una ausencia que termina a las 12:00 **no** bloquea el turno que arranca
a las 12:00, y una que empieza a las 12:00 no bloquea el que termina a esa hora.

### `GET /salons/:id/time-off?from&to`

```json
GET /api/v1/salons/s_.../time-off?from=2026-12-01T00:00:00Z&to=2026-12-08T00:00:00Z -> 200 {
  "time_off": [ { "id": "t_...", "user_id": "u_...",
                  "starts_at": "2026-12-02T05:00:00Z", "ends_at": "2026-12-03T05:00:00Z",
                  "reason": "Vacaciones" } ]
}
```

- **Cualquier miembro** lee: la agenda necesita saber quién falta. Las fechas
  viajan en RFC3339 y el rango es obligatorio, con tope de **92 días**
  (`400 {"error":"El rango no puede superar los 92 días"}`).
- Se devuelven las ausencias que **pisan** el rango (una que empezó antes y sigue
  en curso aparece; el período es semiabierto en los dos lados).
- **Privacidad del motivo**: `reason` viene **vacío** para quien no es
  dueño/administración, salvo en sus **propias** ausencias. Una profesional ve
  que su compañera está ausente pero no por qué; recepción no ve ningún motivo.
  Dueño y administración ven todos los motivos.
- Sin token `401`; un ajeno al salón `403` (el `salon_id` de la ruta no es la
  credencial, la membresía sí).

### `POST /salons/:id/members/:uid/time-off`

```json
POST { "starts_at": "2026-12-02T05:00:00Z", "ends_at": "2026-12-03T05:00:00Z",
       "reason": "Vacaciones" }        // reason es opcional

201 { "time_off": { "id": "t_...", "user_id": "u_...",
                    "starts_at": "2026-12-02T05:00:00Z", "ends_at": "2026-12-03T05:00:00Z",
                    "reason": "Vacaciones" },
      "conflicts": [ { "id": "b_...", "client_name": "Lía",
                       "starts_at": "2026-12-02T15:00:00Z",
                       "ends_at": "2026-12-02T15:45:00Z" } ] }
```

- Solo `owner`/`admin` (`403`); el miembro tiene que ser de **ese** salón
  (`404` si es de otro) y tiene que **atender**:
  `400 {"error":"Ese rol no tiene ausencias"}` para recepción, cliente o un
  dueño/administración sin `attends`.
- **Crear una ausencia NO cancela turnos** (S-F2d2-4). `conflicts` lista los
  turnos `booked` de **esa** persona que se pisan con la ausencia, ordenados por
  inicio: los cancelados no cuentan y los de otra persona tampoco. El salón los
  reasigna (F2d3) o los cancela desde el calendario. `conflicts` es siempre un
  array (vacío si no hay nada).
- Validación (400, textos exactos):
  `El fin debe ser posterior al inicio`, `Una ausencia no puede durar más de 90
  días`, `No se puede cargar una ausencia que ya terminó` (una ausencia **en
  curso** sí se puede cargar), `Ya hay una ausencia en ese período` (dos
  ausencias de la misma persona no se pisan; la misma ventana para **otra**
  persona es válida) y, del motivo, `El motivo no puede tener saltos de línea` /
  `El motivo no puede superar los 120 caracteres` (se recorta).
- Fechas en RFC3339; vacías → `400 {"error":"Indicá el inicio y el fin de la
  ausencia"}`; ilegibles → `400 {"error":"Fecha inválida: usá RFC3339"}`.

### `DELETE /salons/:id/time-off/:tid` -> `204`

- Solo `owner`/`admin` (`403`). Una id de **otro salón** es `404` (no un borrado
  silencioso) y volver a borrar la misma es `404`.

### Disponibilidad y reserva pública: la ausencia manda

- `GET /public/salons/:slug/availability`: una profesional ausente **no se
  ofrece** ese día ni en los huecos que pisan su ausencia. El resto del equipo
  sigue igual; si nadie queda, `200 {"slots":[]}` (nunca un error).
- `POST /public/salons/:slug/bookings`: la asignación automática saltea a quien
  está ausente. Con todo el equipo ausente → `409 {"error":"... no hay
  profesional libre en ese horario"}`; con un `professional_id` ausente elegido a
  mano → `409 {"error":"... el horario ya no está disponible"}`. Nunca `500`.
- La **reserva de staff** desde el calendario (`POST /salons/:id/bookings`) **no
  se restringe por ausencias**: igual que con el horario (F2d1), solo la frena el
  choque de turnos. El salón reasigna o cancela a mano (F2d3).
- Un turno de 90 minutos que arranca a las 09:00 y una ausencia de 10:00 a 12:00
  **se pisan**: ese hueco no se ofrece (el turno tiene que entrar entero fuera de
  la ausencia).

## Fase 6 — F2d3: cubrir una ausencia reasignando los turnos

Cuando alguien falta, el salón **reasigna** sus turnos a otra profesional que
pueda tomarlos y **avisa a la clienta**. Es una acción **manual** del
dueño/administración: elegir quién cubre es una decisión del salón, el servidor
no la toma por su cuenta.

- **No hay tabla nueva** (S-F2d3-1): reasignar es un `UPDATE` del
  `professional_id` del turno, con el mismo `starts_at`/`ends_at` y el mismo
  estado. La **EXCLUDE de `bookings`** (migración 002) sigue siendo la garantía
  de que nadie quede con dos turnos a la misma hora; su rechazo se traduce a
  `409`, nunca a `500`.
- **Todo o nada** (S-F2d3-3): "reasignar todo" corre en **una transacción** con
  un advisory lock por salón. Si un turno no entra, la operación responde `409`
  con el detalle y **no se movió ninguno**.
- Solo `owner`/`admin` (`403`); sin token `401`; un ajeno al salón `403`. El
  motivo de la ausencia es privado, por eso no lo ve cualquiera.

Quién puede **cubrir** un turno (reglas de F2a, F2c, F2d1 y F2d2 juntas):

| Condición | Mensaje exacto |
|---|---|
| No atiende clientes (recepción, cliente, dueño/administración sin `attends`, o alguien de **otro salón**) | `Esa persona no atiende clientes` |
| No está de turno ese día: descanso semanal / día libre / salón cerrado (F2d1) **o** ausente (F2d2) | `Esa persona no está de turno ese día` |
| El turno tiene `service_id` y esa persona no lo hace (F2c) | `Esa persona no hace ese servicio` |
| El turno es de otra persona o no cae dentro de la ausencia | `Ese turno no es de esa ausencia` |
| El turno ya no está `booked` (cancelado, hecho o no-show) | `El turno ya no está reservado` |
| No se nombró destino | `No hay a quién reasignar` |
| El destino **ya tiene un turno** en ese horario (409, lo decide la EXCLUDE) | `Ya tiene un turno a esa hora` |

- Un turno **sin** `service_id` (agendado a mano desde el calendario) lo puede
  tomar **cualquier** persona reservable que esté de turno: el oficio solo se
  exige cuando el turno dice qué servicio es.
- Los tres primeros son `400` (la regla no se cumple); el turno cerrado y el
  horario ocupado son `409` (choque de estado). En todos los casos el cuerpo
  lleva el detalle: `{"error":"...","bookings":[{"id","client_name","starts_at"}]}`
  (array vacío cuando el pedido no nombra a nadie).

### `GET /salons/:id/absences?from&to`

```json
GET /api/v1/salons/s_.../absences?from=2026-12-01T00:00:00Z&to=2026-12-08T00:00:00Z -> 200 {
  "absences": [ {
    "id": "t_...", "user_id": "u_...", "user_name": "Ana",
    "starts_at": "2026-12-02T16:00:00Z", "ends_at": "2026-12-02T19:00:00Z",
    "reason": "Vacaciones",
    "bookings": [ {
      "id": "b_...", "client_name": "Lía",
      "starts_at": "2026-12-02T16:00:00Z", "ends_at": "2026-12-02T16:45:00Z",
      "service_id": "sv_...", "service_name": "Corte",
      "candidates": [
        { "professional_id": "u-...", "name": "Bea", "available": true,  "reason": "" },
        { "professional_id": "u-...", "name": "Ana", "available": false,
          "reason": "Esa persona no está de turno ese día" }
      ] } ]
  } ]
}
```

- Para cada persona ausente en el período, sus turnos **`booked`** que se pisan
  (ordenados por inicio) con las candidatas y el motivo por el que no sirven: el
  `reason` es **el mismo texto** que devuelve el `POST` al rechazarla, y
  `available` es `reason == ""`. Una candidata **ocupada** en ese horario aparece
  como `"reason":"Ya tiene un turno a esa hora"`.
- `reason` de la ausencia se devuelve siempre (solo dueño/administración llega a
  este endpoint; para el resto es `403`).
- `from`/`to` son obligatorios, RFC3339 y con tope de **92 días**, con los mismos
  mensajes que `GET /time-off`: `Indicá el rango de fechas (from y to)`,
  `Rango inválido: el fin debe ser posterior al inicio`,
  `El rango no puede superar los 92 días` (`400`). Fechas ilegibles →
  `400 {"error":"Fecha inválida: usá RFC3339"}`.
- El salón de la ruta es el único que se mira: una ausencia de otro salón no
  aparece.

### `POST /salons/:id/absences/:tid/reassign`

```json
POST { "professional_id": "u-..." }                       // TODOS los turnos de la ausencia
POST { "professional_id": "u-...", "booking_ids": ["b_..."] } // solo esos

200 { "reassigned": 2,
      "bookings": [ { "id": "b_...", "client_name": "Lía",
                      "starts_at": "2026-12-02T16:00:00Z" } ] }
```

- Mueve los turnos `booked` de la ausencia a esa profesional, con el **mismo
  horario**. `booking_ids` es opcional: sin lista se mueven todos; con lista, solo
  esos (y uno que no sea de esa ausencia es
  `400 {"error":"Ese turno no es de esa ausencia"}`). Una lista vacía se trata
  como "todos".
- Una ausencia sin turnos reservados responde `200 {"reassigned":0,"bookings":[]}`.
- Una ausencia de **otro salón** es `404`.
- Si un turno no entra, `409` (o `400` según la tabla de arriba) con el detalle y
  **ningún** turno movido: la transacción se deshace entera.

### `POST /salons/:id/bookings/:bid/reassign`

```json
POST { "professional_id": "u-..." } -> 200 {"reassigned":1,"bookings":[{...}]}
POST { "cancel": true, "reason": "Se enfermó" }
     -> 200 { "cancelled": true, "booking": { ...el turno cancelado... } }
```

- Reasigna **un** turno suelto (el calendario, o uno que la ausencia no explica),
  con las mismas reglas de destino.
- `{"cancel":true}` cancela el turno y **registra el motivo** en la notificación
  interna (`Turno cancelado · Lía · 02/12 10:00 · Motivo: Se enfermó`). El motivo
  es opcional, se recorta y no admite saltos de línea ni más de 120 caracteres
  (`El motivo no puede tener saltos de línea` / `El motivo no puede superar los
  120 caracteres`). Un turno ya cerrado es
  `409 {"error":"El turno ya no está reservado"}`. F1 formalizará el enlace del
  email; acá solo se cancela y se avisa.
- Un turno de otro salón es `404`; reasignar y cancelar son de
  dueño/administración (`403` para el resto).

### Aviso a la clienta (S-F2d3-4)

- **Reasignar**: el aviso interno dice `Turno reasignado · Lía · 02/12 10:00 ·
  ahora la atiende Bea` y publica `booking.updated` para que los calendarios
  abiertos se refresquen. La clienta recibe un correo (T12) con **el mismo
  horario** y quién la atiende ahora; sin email guardado del turno no se manda
  nada (no hay a quién escribir) y la operación termina bien igual.
- **Cancelar**: el aviso interno lleva el motivo y la clienta recibe que el turno
  fue cancelado y que el salón se va a comunicar con ella.
- Los correos salen en segundo plano (como la confirmación): un SMTP lento no
  demora la respuesta ni tumba la operación.

### Quién tenía el turno: `covered_from` (S-F2d3-6)

- Migración **019** (idempotente): `bookings.covered_from` es `text` NULL, **sin
  FK** (la persona puede dejar el equipo y el turno tiene que seguir sabiendo de
  quién era) y **sin backfill**: ningún turno que ya existía estaba cubierto, así
  que una base migrada desde la 018 deja los turnos en `null`.
- Regla: al reasignar, si `covered_from` está vacío se guarda la profesional
  **anterior**; si ya tenía valor se conserva (Ana -> Cata -> Bea sigue diciendo
  Ana) y si el turno vuelve a quien figura ahí, se limpia. Reasignar a la misma
  persona no marca nada.
- Ningún otro flujo lo toca: reprogramar, cambiar estado (`PATCH`), el alta de
  staff y la reserva pública lo dejan como está; un turno nuevo nace en `null`.
  Se persiste en toda escritura del turno (alta y el `UPDATE` de la transacción
  de cobertura) y se lee en toda consulta de la agenda.
- Se expone como `covered_from` (`string`, o **`null`** cuando nadie lo cubre)
  **solo** en el JSON de turnos del personal: `GET /salons/:id/bookings`, el
  `PATCH /salons/:id/bookings/:bid` y el `POST /salons/:id/bookings`.
  **No** sale en la reserva pública (`POST /public/salons/:slug/bookings`) ni en
  el payload del websocket (`booking.created` / `booking.updated`): los
  calendarios releen la agenda del personal.
- «Mi agenda» lo usa para el aviso «Tu turno del X lo cubre otra profesional»:
  el turno trae `covered_from` = mi id, sigue `booked` y todavía no llegó.

### Disponibilidad, reserva pública y PATCH clásico: sin cambios

- F2d3 **no** toca la disponibilidad pública, la reserva web ni el `PATCH
  /bookings/:bid` de siempre (estado y reprogramación): siguen con las mismas
  reglas de F2a/F2c/F2d1/F2d2. La cobertura es una acción aparte, explícita y
  auditada en la bandeja de avisos. El único agregado es `covered_from` en el
  JSON del personal (arriba); el `PATCH` no lo escribe.

## Fase 6 — F2e: puestos y equipamiento con capacidad

Un salón tiene **tipos de equipo** ("Puesto de peluquería", "Silla de
barbería", "Lavacabezas") y cada tipo tiene **unidades con nombre** ("Puesto
1", "Puesto 2"...). Un **servicio** puede exigir un tipo (`resource_type_id`) y
entonces cada **turno ocupa una unidad concreta** (`resource_unit_id`) durante
toda su duración, además de la profesional.

La capacidad **no es un contador**: cada unidad es una fila con su propia
`EXCLUDE USING gist (salon_id, resource_unit_id, tstzrange(starts_at, ends_at))`
en Postgres (migración 020), el mismo patrón que `bookings_no_overlap` de la
002. Dos reservas simultáneas nunca se llevan la última unidad: la base deja
entrar una y la otra vuelve como conflicto.

Sin backfill: un servicio **sin** `resource_type_id` y un turno **sin**
`resource_unit_id` se comportan exactamente como antes de F2e.

Permisos: **leer** cualquier miembro del salón; **escribir** (crear, renombrar,
agregar unidades, activar/desactivar, borrar) solo dueño/administración. Todo
va scopeado por `salon_id`: un id de otro salón responde **404**.

### `GET /salons/:id/resources`

```json
{
  "types": [
    {
      "id": "t1",
      "name": "Puesto de peluquería",
      "units": [
        {"id": "u1", "name": "Puesto de peluquería 1", "active": true},
        {"id": "u2", "name": "Puesto de peluquería 2", "active": false}
      ],
      "services": 2
    }
  ]
}
```

`services` es cuántos servicios del catálogo (activos o no) usan ese tipo: es el
aviso «No hay equipo activo: no se ofrecen turnos» de la pantalla de servicios.

### `POST /salons/:id/resources` → `201`

Body `{"name": "Puesto de peluquería", "count": 3}`. Crea el tipo y sus
unidades con nombre automático (`"<tipo> 1"`, `"<tipo> 2"`...) en **una
transacción**: o queda el tipo con todas sus unidades o no queda nada.
`count` va de 1 a 50.

### `PATCH /salons/:id/resources/:tid` → `200`

Body `{"name": "Puesto"}`. Renombrar no toca las unidades ni los servicios.

### `POST /salons/:id/resources/:tid/units` → `201`

Body `{"count": 2}` → `{"units": [...]}`. Las unidades nuevas usan el **siguiente
número libre** del tipo: si la "3" se renombró o no existe, se vuelve a usar ese
número.

### `PATCH /salons/:id/resource-units/:uid` → `200`

Body `{"name"?: "Puesto A", "active"?: false}`. **Quitar equipo es desactivar**:
la unidad sale de la disponibilidad pero los turnos ya agendados la conservan.
La respuesta informa cuántos turnos `booked` futuros la usan (informativo):

```json
{"id": "u1", "name": "Puesto A", "active": false, "future_bookings": 3}
```

### `DELETE /salons/:id/resources/:tid` → `204`

Solo si **ningún servicio** usa el tipo y **ninguna unidad** tiene turnos. Si no:

```json
409 {"error": "Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo"}
```

Un tipo libre se borra con sus unidades en la misma transacción.

### Errores exactos del equipamiento

| Situación | Status | `error` |
|---|---|---|
| Nombre repetido (sin importar mayúsculas) | 400 | `Ya existe un equipo con ese nombre` |
| Más de 20 tipos | 400 | `Máximo 20 tipos de equipo` |
| Más de 50 unidades en un tipo | 400 | `Máximo 50 unidades por tipo` |
| Nombre vacío | 400 | `El nombre del equipo es obligatorio` |
| Nombre de más de 60 caracteres | 400 | `El nombre no puede superar los 60 caracteres` |
| `count` fuera de 1..50 | 400 | `La cantidad de unidades debe estar entre 1 y 50` |
| Borrar un tipo en uso | 409 | `Hay servicios o turnos que lo usan: desactivá las unidades en lugar de borrarlo` |
| Tipo/servicio de otro salón | 404 | `no encontrado` |

### Servicios: `resource_type_id`

`POST /salons/:id/services` y `PUT /salons/:id/services/:service_id` aceptan
`resource_type_id` (string o `null`; `""`/`null` = el servicio deja de usar
equipo). Un tipo de **otro salón** o inexistente:

```json
400 {"error": "Equipo inválido"}
```

Los JSON de servicio (`POST`, `PUT`, `GET /services`, y el público de la
landing no lo necesita) agregan `resource_type_id` (`string` o `null`).

Los turnos del personal (`GET /salons/:id/bookings`, `POST`, `PATCH`) agregan
`resource_unit_id` (`string` o `null`). La reserva pública **no** lo devuelve.

### Disponibilidad pública: la capacidad manda

`GET /public/salons/:slug/availability` con un servicio que usa equipo:

- Un horario se ofrece solo si hay **al menos una profesional elegible libre**
  (F2a/F2c/F2d1/F2d2) **y al menos una unidad ACTIVA libre en todo el
  intervalo**. Por eso 5 profesionales con 3 puestos ofrecen **3** turnos por
  hora, no 5.
- Sin unidades activas del tipo: `{"slots": []}` (nunca un error).
- Las duraciones cuentan enteras: un turno de 3 horas ocupa su unidad 3 horas y
  mientras tanto otro servicio de 30 minutos entra en otra unidad.
- Desactivar una unidad la saca de la oferta; el turno que ya la tenía la
  conserva (y la sigue ocupando hasta que se cancele).

### Reserva pública: el par (profesional, unidad)

`POST /public/salons/:slug/bookings` asigna profesional **y** unidad y guarda
`resource_unit_id` en el turno. La `EXCLUDE` de Postgres decide el doble turno:
si rechaza el par (otra reserva entró entre el chequeo y el `INSERT`), el caso
de uso **reintenta con otro par hasta 3 veces** antes de responder.

- Sin unidades activas del tipo: `409 {"error":"ya existe: el horario ya no está disponible"}`.
- Sin ningún par libre (última unidad o último profesional tomado):
  `409` (mensaje de siempre: `no hay profesional libre en ese horario` cuando
  la asignación es automática). **Nunca un 500 y nunca dos turnos en la misma
  unidad.**

### Reprogramar, cancelar y cubrir una ausencia

- `PATCH /salons/:id/bookings/:bid` de un turno con unidad: se conserva su
  unidad si está libre en el horario nuevo; si no, toma otra unidad **activa**
  libre del mismo tipo; si no queda ninguna,
  `409 {"error":"ya existe: no hay equipo libre en ese horario"}`.
- Cancelar **libera** la unidad (la `EXCLUDE` solo mira `booked`).
- Reasignar una ausencia (F2d3) **conserva** la unidad: el horario no cambia.
- La reserva de staff desde el calendario **sin servicio** no ocupa unidad
  (como hoy) y se reprograma sin mirar el equipamiento.

## Fase 6 — F2g1: invitaciones de equipo

Hasta acá `POST /members` exigía que la persona **ya tuviera cuenta**
(`400 {"error":"La persona todavía no tiene cuenta"}`). La invitación resuelve el
alta de gente nueva: el salón emite un enlace de un solo uso, la persona crea su
cuenta y entra con el rol asignado.

Reglas de seguridad (no negociables):

- El token son **32 bytes aleatorios de `crypto/rand` en base64url** (43
  caracteres). En la base se guarda **solo su SHA-256**: el token en claro no
  existe en la tabla, ni en el log, ni en ningún listado.
- El enlace lo lleva en el **fragmento**: `<APP_URL>/invitacion#<token>`. Lo que
  va después del `#` no se manda al servidor, así que no queda en el log de la
  API ni en el de nginx. Las rutas públicas reciben el token en el **cuerpo**
  (nunca en la ruta ni en el query) y están bajo el rate limit por IP, como
  `/auth`.
- **Un solo uso y vence a los 7 días.** El canje es atómico: dos canjes
  simultáneos del mismo token — uno gana y el otro recibe el 404 genérico.
- **Sin oráculo**: token inexistente, vencido, usado o revocado responden
  exactamente lo mismo (`404 {"error":"Invitación inválida o vencida"}`). El
  lookup público jamás dice si un email tiene cuenta salvo por `has_account`, y
  solo con un token válido.

### `POST /salons/:id/invitations` → `201`

Solo `owner`/`admin` del salón. `attends` es opcional (default del rol, igual que
`POST /members`).

```json
POST {"email":"lucia@mail.com","role":"professional"}
-> 201 {
     "invitation": { "id": "i_...", "email": "lucia@mail.com", "role": "professional",
                     "attends": true, "expires_at": "2027-03-08T12:00:00Z",
                     "created_at": "2027-03-01T12:00:00Z" },
     "link": "http://localhost:8081/invitacion#<token>",
     "email_sent": true
   }
```

- El `link` se devuelve **solo en esta respuesta** (sirve para compartirlo a mano
  si el correo falla) y no se puede recuperar después.
- **Reinvitar al mismo email revoca la anterior** y emite un token nuevo: el
  token viejo pasa a dar el 404 genérico.
- Máximo **50 pendientes** por salón; reinvitar no suma (la anterior se revoca
  antes de contar).
- El rol sigue la **misma regla de `POST /members`** (`Role.AddableBy`): un
  `owner` invita `admin`/`professional`/`reception`; un `admin` no invita admins;
  nadie invita `client` ni un segundo `owner`.

| Caso | HTTP | `error` |
|---|---|---|
| El rol no se puede invitar (admin→admin, `client`, `owner`, inexistente) | `400` | `No podés invitar con ese rol` |
| Email sin forma de dirección | `400` | `Email inválido` |
| Ya es parte del equipo | `409` | `Esa persona ya es parte del equipo` |
| 50 pendientes | `400` | `Máximo 50 invitaciones pendientes` |
| Quien pide no es owner/admin del salón | `403` | `sin acceso al salón` / `sin permiso` |

### `GET /salons/:id/invitations` → `200`

Pendientes (ni aceptadas, ni revocadas, ni vencidas), de la más vieja a la más
nueva. **Nunca** incluye el token ni su hash:

```json
{ "invitations": [ { "id": "i_...", "email": "lucia@mail.com", "role": "professional",
                     "attends": true, "expires_at": "...", "created_at": "..." } ] }
```

### `DELETE /salons/:id/invitations/:iid` → `204`

Revoca una pendiente del salón. Una invitación de otro salón, ya revocada o ya
aceptada da `404`. Después de revocar, el token responde el 404 genérico.

### `POST /public/invitations/lookup` (sin sesión) → `200`

```json
POST {"token":"<token>"}
-> 200 { "salon_name": "Navajas de Oro", "role": "professional",
         "role_label": "profesional", "email_hint": "l***@mail.com",
         "expires_at": "2027-03-08T12:00:00Z", "has_account": false }
```

`email_hint` es la dirección enmascarada (primera letra + `***` + dominio).
Token inválido/vencido/usado/revocado: `404 {"error":"Invitación inválida o vencida"}`
en los **cuatro** casos (mismo código y mismo cuerpo).

### `POST /public/invitations/accept` (sin sesión) → `200`

```json
POST {"token":"<token>","name":"Lucía","password":"secreta123"}
-> 200 { "token": "<jwt>" }
```

- La respuesta es **la misma sesión del login** (`{"token"}`): el JWT se usa igual
  en `/me` y en el resto de la API.
- La cuenta se crea con la **política de contraseña vigente** (8 a 72 bytes, la
  del registro) y se valida **antes** de gastar bcrypt.
- La cuenta, la membresía (con el rol y el `attends` de la invitación) y el
  estado de la invitación se guardan en **una sola transacción**: si la membresía
  falla no queda ni la cuenta ni la invitación usada.
- El email ya tiene cuenta → `409 {"error":"Ya tenés cuenta: iniciá sesión para aceptar la invitación"}`
  (para eso está la ruta autenticada).

### `POST /invitations/accept` (con sesión) → `200`

Es el camino de quien ya tiene cuenta. Solo une al salón si la invitación es para
**su** email.

```json
POST {"token":"<token>"}   (Authorization: Bearer <jwt>)
-> 200 { "salon": { "id": "s_...", "slug": "navajas", "name": "Navajas de Oro" },
         "role": "reception", "role_label": "recepción" }
```

- Email de la cuenta ≠ email de la invitación →
  `403 {"error":"Esta invitación es para otra cuenta"}`.
- Sin sesión → `401`. Token que no sirve → el 404 genérico.
- Alguien que ya es miembro del salón → `409 {"error":"Esa persona ya es parte del equipo"}`
  (el canje no cambia el rol de quien ya está adentro).

### Correo al invitado (S-F2g1-5)

Asunto `<Salón> te invitó a su equipo`. Texto + HTML con el rol en español, la
fecha de vencimiento en el día local del salón y el enlace. El HTML escapa el
nombre del salón. Sale por el mismo `MailSender` del T12.

- Es **best effort**: si el SMTP falla (o `SMTP_HOST` está vacío) la invitación
  existe igual y la respuesta dice `"email_sent": false`; el enlace se puede
  pasar a mano.
- El token y el enlace **nunca** se loguean: el log solo nombra el id de la
  invitación y el salón.

## Fase 6 — F2g2: dar de baja en vez de borrar

Hasta acá `DELETE /salons/:id/members/:uid` hacía un `DELETE` físico de la
membresía. Los turnos y las ventas sobrevivían (referencian a `users`), pero se
perdían en cascada `professional_hours`, `time_off` y `professional_services`, y
el historial ya no podía decir cómo se llamaba la persona. Ahora la baja
**conserva la fila** con `ended_at`: el historial queda intacto, F3 puede
liquidar comisiones de alguien que ya no está, y la persona se puede reactivar
con su rol, su marca y su oficio de antes.

### `DELETE /salons/:id/members/:uid` → `204`: ahora da de baja

```json
DELETE /salons/:id/members/u_...            -> 204 (sin body)
DELETE de alguien con turnos futuros        -> 409
  {"error":"Tiene 3 turnos pendientes: reasignalos o cancelalos antes de darla de baja"}
DELETE de alguien con 1 turno futuro        -> 409
  {"error":"Tiene 1 turno pendiente: reasignalo o cancelalo antes de darla de baja"}
```

- **Mismo verbo y mismo `204`**: el frontend actual no cambia de forma. Lo que
  cambia es el efecto: la membresía queda con `ended_at` (no se borra) y sus
  horarios, ausencias y servicios **se conservan** para cuando vuelva.
- **Con turnos futuros no cancelados no se hace nada**: `409` con la oración
  exacta (singular para 1, plural para N) y la persona sigue activa. Cuentan solo
  los turnos `booked` que **empiezan después de ahora**; los pasados y los
  cancelados no frenan la baja.
- **Quién da de baja** (`manageActor`): solo `owner`/`admin` del salón, igual
  que siempre (`403` para recepción, profesional, cliente y para quien no es del
  salón). La regla de rol de la baja **no** es la del alta: el **`owner` da de
  baja a cualquier rol**, incluido otro `owner` y a sí mismo (con otro owner
  activo), mientras que la **administración solo da de baja a profesional,
  recepción o cliente** —no a otro `admin` ni al `owner`: `403
  {"error":"sin permiso"}`—, el mismo criterio de su invitación (F2g1). El
  **último `owner` activo** no se puede dar de baja
  (`400 {"error":"No se puede quitar al último dueño"}`, igual que antes de
  F2g2) y esa comprobación va **antes** que la del rol: con un solo owner activo
  el `400` gana y el salón nunca queda sin dueño. La reactivación usa la misma
  regla.
- **La carrera del último owner está cerrada**: el conteo se repite DENTRO de la
  transacción, después del `UPDATE`, y las bajas del mismo salón se serializan
  con un lock corto por salón (`pg_advisory_xact_lock` sobre
  `member-write:<salón>`). Sin eso, dos owners que se daban de baja **mutuamente**
  bloqueaban filas distintas, los dos veían «queda otro owner» y el salón quedaba
  sin dueño. Ahora una baja entra (`204`) y la otra responde `400` (o `404` si su
  fila ya no estaba): nunca cero owners y nunca un `500`.
- **En la misma transacción** se revocan las invitaciones pendientes que esa
  persona emitió (`invitations.created_by`, hallazgo de F2g1): el token viejo
  pasa a responder el 404 genérico y no sobrevive a quien lo emitió.
- Alguien que **ya está dado de baja** (o es de otro salón) → `404`
  `{"error":"no encontrado"}`: para esta ruta no está.
- Los `403`/`404` del resto de la pantalla de equipo no cambian; el aislamiento
  es el de siempre (el `salon_id` de la ruta manda).

### `POST /salons/:id/members/:uid/reactivate` → `200`: deshacer la baja

```json
POST /salons/:id/members/u_.../reactivate -> 200
{ "id": "u_...", "name": "Beto", "email": "beto@mail.com", "role": "professional",
  "attends": true, "trade": "Barbero", "service_ids": ["svc_..."],
  "active": true, "ended_at": null }
```

- Devuelve el miembro completo, igual que el `PATCH`. **Conserva** el rol, la
  marca `attends`, el oficio y los servicios que hacía: la fila nunca se borró.
- Misma regla que la baja, también al revés (`Role.RemovableBy`): el `owner`
  reactiva a cualquiera, incluido otro `owner`; la administración solo a
  profesional, recepción o cliente (un `admin` no reactiva al `owner` ni a otro
  `admin`: `403`). Recepción, profesional y cliente no reactivan a nadie.
- Si ya estaba activo → `409 {"error":"Esa persona ya está en el equipo"}`. Un
  miembro de otro salón o inexistente → `404`.
- Dos bajas o baja+reactivación simultáneas dejan un estado consistente: la
  escritura es un `UPDATE` condicional por `ended_at` (nunca dos filas).

### Reactivación por las otras dos puertas

Las dos puertas resuelven a la persona con la lectura que **también ve la baja**
(`FindMember`): con la lectura de siempre una baja parecía alguien nuevo y el
`upsert` del alta le cambiaba el rol al que eligiera el actor.

- **`POST /salons/:id/members`** con el email de alguien dado de baja lo
  **reactiva** con el rol pedido (`attends` según el rol, o el del body):
  `201` y el miembro, **igual que un alta**. Ya no responde «ya es parte del
  equipo» para quien está de baja: esa persona para el alta no está en el equipo.
  La autorización es la de la baja/reactivación sobre el **rol guardado**
  (`Role.RemovableBy`): el `owner` reactiva a cualquiera con el rol que pida; la
  administración solo a profesional, recepción o cliente y por lo tanto **no
  reactiva a un `admin` ni a un `owner` dados de baja**, ni para bajarles el rol:
  `403 {"error":"sin permiso"}` y nada cambia.
- **Crear una invitación** para alguien dado de baja sigue la misma regla: el
  `owner` puede (y el canje lo reactiva con el rol de la invitación), la
  administración no puede invitar a un `admin` ni a un `owner` dados de baja
  (`403 {"error":"sin permiso"}`), porque el canje les cambiaría el rol.
- **Aceptar una invitación** (`POST /invitations/accept`, la ruta autenticada de
  una cuenta que ya existe) también lo reactiva con el rol de la invitación. Se
  le puede invitar: una baja no bloquea la invitación.

### Campos nuevos en `GET /salons/:id/members`

```json
GET /salons/:id/members -> { "members": [
  { "id": "u_...", "name": "Beto", "email": "beto@mail.com", "role": "professional",
    "attends": true, "trade": "Barbero", "service_ids": [],
    "active": true,  "ended_at": null },
  { "id": "u_...", "name": "Carla", "email": "carla@mail.com", "role": "reception",
    "attends": false, "trade": "", "service_ids": [],
    "active": false, "ended_at": "2026-05-01T18:30:00Z" } ] }
```

- `active` (bool) y `ended_at` (RFC3339 en UTC, o `null`): las dos claves están
  **siempre**. El listado devuelve **activos Y dados de baja** y el frontend
  decide cómo mostrarlos (la lista principal muestra solo activos y el bloque
  «Ex equipo» usa `ended_at`).
- `POST`, `PATCH` y la reactivación devuelven la misma forma: un alta y una
  reactivación salen con `active: true` y `ended_at: null`.

### Quien fue dado de baja pierde el acceso ya

- `memberships.Find` y `ListByUser` devuelven **solo membresías activas**: la
  persona recibe `403` en cualquier ruta del salón en su **próximo pedido** (sin
  esperar a que venza el JWT de 24 h) y su `GET /me` deja de listar ese salón.
  Su cuenta (`users`) no se toca: sigue entrando a la app y, si es miembro de
  otro salón, conserva ese acceso.
- Tampoco aparece en ningún selector ni en la agenda: `GET /salons/:id/staff`,
  la disponibilidad pública, la auto-asignación, «Mi agenda», los equipos de la
  agenda y los candidatos de cobertura usan solo activos, así que una baja deja
  de atender sin tocar turnos ni ventas pasadas.

| Caso | HTTP | `error` |
|---|---|---|
| Baja de alguien con turnos futuros (1 / N) | `409` | `Tiene 1 turno pendiente: reasignalo o cancelalo antes de darla de baja` / `Tiene N turnos pendientes: reasignalos o cancelalos antes de darla de baja` |
| Baja del último `owner` activo | `400` | `No se puede quitar al último dueño` |
| Baja de un rol que la administración no maneja (admin→owner, admin→admin) | `403` | `sin permiso` |
| Baja de quien ya está de baja / de otro salón | `404` | `no encontrado` |
| Reactivar a alguien que ya está activo | `409` | `Esa persona ya está en el equipo` |
| Reactivar un inexistente o de otro salón | `404` | `no encontrado` |
| Reactivar sin permiso (profesional, recepción, otro salón, admin→owner/admin) | `403` | `sin permiso` / `sin acceso al salón` |
| Reactivar por `POST /members` o invitación a un `admin`/`owner` dado de baja, siendo `admin` | `403` | `sin permiso` |

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
| Reactivar (por alta o invitación) a un admin/owner dado de baja, siendo admin | `403` | `sin permiso` |

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

## Fase 6 — F3.2: nómina y comisiones

Cada miembro del equipo puede cobrar **sueldo fijo por período**, **comisión**
sobre lo que vendió, o **mixto**. La comisión se calcula con la **foto** del
porcentaje que cada venta guardó desde F3.1, y el período lo deriva **siempre el
servidor** en la zona horaria del salón: la nómina nunca usa el día UTC.

### Reglas del período (D5)

El cliente manda `kind` (`week` | `fortnight` | `month`) y `date`
(`YYYY-MM-DD`, **cualquier** día dentro del período). El servidor lo deriva en la
zona de `salons.timezone` (la misma que el cierre de caja):

- **`week`**: de lunes a domingo que contiene la fecha.
- **`fortnight`**: 1–15 o 16–fin de mes (en febrero el fin es el 28 o el 29).
- **`month`**: el mes calendario.

La respuesta devuelve `from` y `to` como fechas locales **inclusivas**
(`YYYY-MM-DD`). Internamente la ventana es `[from 00:00 local, (to+1 día) 00:00
local)` convertida a UTC con la zona del salón; el día siguiente se calcula como
fecha, **nunca sumando 24 h**: un día con cambio de horario de verano dura 23 o 25
horas (en `America/New_York`, la semana del 8/3/2026 dura 167 h y la del 1/11/2026
169 h) y el período lo incluye entero sin correrse.

`closed` es `true` cuando el período ya terminó: `to` es anterior a **hoy** en la
zona del salón. El período en curso (incluido hoy) es `closed: false`. Un `kind`
desconocido o una `date` que no es una fecha real → `400
{"error":"Período inválido"}` (también sin parámetros en `/me`).

### Fórmula de la comisión (D2)

```
commission_cents = floor( Σ qty × unit_price_cents × pct / 100 )
```

sobre las ventas **acreditadas a esa persona** con `created_at` en la ventana del
período. **Una sola división al final**, no por ítem: dividir ítem por ítem
acumula el redondeo (dos líneas de 335 centavos al 10 % dan 67 centavos con la
fórmula y 66 si se redondea cada una). Todo en enteros (`bigint` en la suma);
nunca `float`.

`pct` es la **foto** de la línea (`sale_items.commission_pct`, F3.1) y, cuando la
línea es anterior a F3.1 (`NULL`), la tarifa **actual** del servicio; en ese caso
la línea de la nómina sale con `estimated: true`. No hay backfill: una venta con
foto no cambia aunque el dueño edite el porcentaje del catálogo, una sin foto se
estima con lo que el catálogo diga hoy y lo avisa.

### `GET /salons/:id/pay-models` → `200`

```json
{ "pay_models": [
  { "user_id": "u_...", "name": "Ana", "role": "professional", "active": true,
    "fixed_cents": 150000, "fixed_period": "fortnight", "commission_enabled": false },
  { "user_id": "u_...", "name": "Beto", "role": "professional", "active": false,
    "fixed_cents": 0, "fixed_period": "month", "commission_enabled": true } ] }
```

- Devuelve **todos** los miembros del salón, activos y **dados de baja** (`active`
  dice cuál es cuál).
- **Sin fila** rige el default de D3: `fixed_cents: 0`, `fixed_period: "month"`,
  `commission_enabled: true` (solo comisión, lo de siempre). No hay backfill.
- Solo `owner`/`admin` (ver la tabla de permisos).

### `PUT /salons/:id/members/:uid/pay-model` → `200`

```json
PUT {"fixed_cents":150000,"fixed_period":"fortnight","commission_enabled":false}
-> 200 { "user_id": "u_...", "name": "...", "role": "professional", "active": true,
         "fixed_cents": 150000, "fixed_period": "fortnight", "commission_enabled": false }
```

- El PUT REEMPLAZA el modelo entero: `fixed_cents`, `fixed_period` y `commission_enabled`
  son OBLIGATORIOS. Un cuerpo parcial (campo ausente o `null`) es un `400`
  `{"error":"Faltan datos del modelo de pago: indicá el sueldo fijo, su período y si cobra comisión"}`
  y no escribe nada (antes `{"fixed_period":"week"}` guardaba `fixed_cents: 0` en
  silencio y borraba un sueldo).
  Para apagar la comisión hay que mandar `false` explícito.
- Se guarda con `upsert` por `(salon_id, user_id)`: escribir dos veces deja una
  sola fila.
- `fixed_cents` **negativo** → `400 {"error":"El sueldo fijo no puede ser
  negativo"}`; arriba del máximo (2.000.000.000 centavos, el tope del dinero del
  resto del sistema) → `400 {"error":"El sueldo fijo no puede superar los
  2000000000 centavos"}`; `fixed_period` desconocido → `400 {"error":"Período de
  pago desconocido: usá semana, quincena o mes"}`. Un monto con decimales
  (`1500.5`) no es un modelo válido → `400 {"error":"body inválido"}`.
- Una persona que **no es del salón** (o es de otro tenant) → `404
  {"error":"no encontrado"}`, y no se escribe nada. La FK compuesta de la tabla
  es la garantía dura.
- Los permisos se evalúan **antes** de la validación: recepción o profesional
  con un monto inválido reciben `403`, no `400`.

### `POST /salons/:id/payroll/preview {kind,date}` → `200`

```json
{ "period": { "kind": "month", "from": "2026-03-01", "to": "2026-03-31", "closed": false },
  "lines": [
    { "user_id": "u_...", "name": "Dueña", "active": true, "sales_count": 0,
      "sales_total_cents": 0, "commission_cents": 0, "fixed_cents": 100000,
      "total_cents": 100000, "estimated": false },
    { "user_id": "u_...", "name": "Ana", "active": true, "sales_count": 3,
      "sales_total_cents": 3499, "commission_cents": 499, "fixed_cents": 0,
      "total_cents": 499, "estimated": true } ],
  "totals": { "commission_cents": 499, "fixed_cents": 100000, "total_cents": 100499 } }
```

- **Líneas**: aparece quien tiene al menos una venta en el período **o** un fijo
  que aplicar. Cada línea es `comisión + fijo`; `totals` es la suma de las líneas.
- **Fijo (D4)**: se paga **una vez** y solo si el `kind` liquidado es el
  `fixed_period` del miembro **y** la persona está **activa**. Una persona dada de
  baja entra con la comisión de lo que vendió y **fijo 0** (el dueño puede sumar
  un ajuste en F3.3).
- **`commission_enabled: false`** deja `commission_cents: 0`, pero sus ventas
  siguen contando en `sales_count` y `sales_total_cents` (fijo puro). Tampoco marca `estimated`: sin comisión no hay nada estimado.
- Un profesional **con ventas** que ya no está en el equipo aparece igual
  (`active: false`, con la comisión de lo que vendió): la plata no se pierde.
- **Orden**: total descendente y, a igual total, por nombre.
- Solo `owner`/`admin`; `400 {"error":"Período inválido"}` si `kind` o `date` no
  son válidos. La vista previa acepta el período **en curso** (`closed: false`);
  crear una liquidación (F3.3) exigirá `closed: true`.

### `GET /salons/:id/payroll/me?kind=&date=` → `200`

```json
{ "period": { "kind": "week", "from": "2026-03-09", "to": "2026-03-15", "closed": false },
  "sales_count": 3, "sales_total_cents": 3499, "commission_cents": 499,
  "estimated": true }
```

- **Solo las figuras de quien llama**, con la misma forma de período. La consulta
  es de UNA persona (`CommissionTotalFor`): la respuesta no se arma leyendo los
  números de los demás, así no hay forma de que se filtren.
- Si su modelo tiene la comisión apagada, `commission_cents: 0` (coherente con la
  vista previa) y sus ventas siguen informadas.
- `owner`, `admin` y `professional` activos. `reception` → `403
  {"error":"sin permiso"}` (tampoco ve las suyas, D7). Un miembro dado de baja no
  entra (`403`), y `kind`/`date` inválidos → `400 {"error":"Período inválido"}`.

### Permisos de la nómina (D7)

| Ruta | owner | admin | professional | reception | dado de baja |
| --- | --- | --- | --- | --- | --- |
| `GET /salons/:id/pay-models` | 200 | 200 | 403 | 403 | 403 |
| `PUT /salons/:id/members/:uid/pay-model` | 200 | 200 | 403 | 403 | 403 |
| `POST /salons/:id/payroll/preview` | 200 | 200 | 403 | 403 | 403 |
| `GET /salons/:id/payroll/me` | 200 | 200 | 200 (solo lo suyo) | 403 | 403 |

`403` es `{"error":"sin permiso"}` cuando el rol no alcanza y `{"error":"sin
acceso al salón"}` cuando no hay membresía activa en el salón de la ruta (otro
tenant, persona dada de baja). Sin sesión, `401`. Toda consulta lleva `salon_id`:
el modelo de pago, las ventas y las personas de otro salón no se leen ni se
escriben (un `uid` de otro tenant en el `PUT` es `404`).

## Fase 6 — F3.3: liquidaciones de nómina

Una **liquidación** congela un período ya terminado: los montos de cada línea
(comisión + fijo, con la foto del nombre y de las ventas) y el ajuste manual
quedan escritos en la tabla. Desde que se crea, editar el catálogo, cargar ventas
del período o cambiar el modelo de pago **no mueve lo guardado**. Desde que se
**paga**, no admite ningún cambio.

La creación reutiliza el **mismo motor** de la vista previa de F3.2 (`kind` +
`date`, período derivado en la zona del salón): no hay una segunda fórmula. La
migración **025** agrega `payroll_runs` y `payroll_lines`, con una `EXCLUDE` por
salón sobre `daterange(from_date, to_date + 1, '[)')`: dos liquidaciones (de
cualquier tipo, abiertas o pagadas) no pueden pisar los mismos días, así ninguna
venta se paga dos veces. La creación corre en **una transacción** que primero
toma un advisory lock por salón (`payroll-write:<salon_id>`), después lee la
**zona horaria del salón** y deriva el período, y recién entonces inserta
cabecera + líneas; la `EXCLUDE` es la red de seguridad y su `23P01` responde
**409**, nunca 500.

**Zona horaria congelada.** Como la corrida guarda fechas **locales** y la
`EXCLUDE` compara fechas, cambiar `salons.timezone` después de una liquidación
haría que la próxima derive otra ventana de instantes: las ventas del borde se
pagarían dos veces (o en ninguna). Por eso, desde que el salón tiene **al menos
una liquidación (abierta o pagada)**, `PUT /salons/:id/settings` que **cambia**
la zona horaria responde
`409 {"error":"No se puede cambiar la zona horaria: ya hay liquidaciones de nómina calculadas con la zona actual"}`
y **no escribe nada** (ni los otros campos). El chequeo es un `EXISTS` de
liquidaciones bajo el **mismo lock por salón** que la creación, así que no hay
carrera entre un cambio de zona y una creación. Un `PUT` con la **misma** zona
sigue funcionando (los otros campos se aplican), y **borrar la última corrida
abierta descongela** la zona porque no es una marca: mientras no queden
liquidaciones, la zona se puede cambiar.

### Objeto `run` (detalle)

```json
{ "id": "run_...", "kind": "month", "from": "2026-02-01", "to": "2026-02-28",
  "status": "open", "created_at": "2026-03-02T12:00:00Z", "paid_at": null,
  "totals": { "commission_cents": 250, "fixed_cents": 0, "adjustment_cents": 0,
              "total_cents": 250 },
  "lines": [
    { "user_id": "u_...", "name": "Ana", "active": true, "sales_count": 2,
      "sales_total_cents": 2500, "commission_cents": 250, "fixed_cents": 0,
      "adjustment_cents": 0, "adjustment_note": "", "total_cents": 250,
      "estimated": true } ] }
```

- `from` y `to` son fechas **locales inclusivas** del salón (`YYYY-MM-DD`).
- `total_cents` de la línea = comisión + fijo + ajuste, y **nunca** es negativo.
- `paid_at` es `null` en una abierta y RFC3339 en una pagada (la clave está
  siempre). `name`, `sales_count`, `sales_total_cents`, `commission_cents`,
  `fixed_cents` y `estimated` son la **foto** del momento de crear; `active` sale
  de la membresía actual (una baja posterior se informa como tal).
- Totales: `adjustment_cents` es la suma de los ajustes de las líneas.

### `POST /salons/:id/payroll/runs {kind,date}` → `201 run`

- Calcula con la vista previa y congela. `kind` ∈ `week` | `fortnight` |
  `month`; `date` es cualquier día del período (`YYYY-MM-DD`).
- Exige el período **terminado**: `to` anterior a hoy en la zona del salón, si no
  → `400 {"error":"El período todavía no terminó: liquidá cuando haya pasado"}`.
- Sin líneas para pagar → `400 {"error":"No hay nada para liquidar en ese período"}`.
- Días ya cubiertos por otra liquidación (abierta o pagada, de cualquier tipo)
  → `409 {"error":"Ya hay una liquidación que cubre esos días"}`. Dos creaciones
  simultáneas del mismo período: una `201` y la otra `409`, nunca dos corridas.
- `kind`/`date` inválidos → `400 {"error":"Período inválido"}` (el mensaje lo pone
  el dominio). La corrida nace `open`, con `created_by` = quien la creó.

### `GET /salons/:id/payroll/runs` → `200`

```json
{ "runs": [ { "id": "run_...", "kind": "month", "from": "2026-02-01",
  "to": "2026-02-28", "status": "open", "created_at": "2026-03-02T12:00:00Z",
  "paid_at": null, "lines_count": 1,
  "totals": { "commission_cents": 250, "fixed_cents": 0,
              "adjustment_cents": 0, "total_cents": 250 } } ] }
```

Las del salón, la más nueva primero (por `from` descendente). Sin liquidaciones
devuelve `{"runs":[]}` (nunca `null`).

### `GET /salons/:id/payroll/runs/:rid` → `200 run` o `404`

Inexistente **o de otro salón**: `404 {"error":"no encontrado"}` (el id de otro
tenant no se lee ni se escribe: toda consulta lleva `salon_id`).

### `PATCH /salons/:id/payroll/runs/:rid/lines/:uid` → `200 run`

```json
PATCH {"adjustment_cents": -1250, "adjustment_note": "Venta duplicada"}
```

- `adjustment_cents` es **OBLIGATORIO**: campo ausente o `null` →
  `400 {"error":"Faltan datos del ajuste: indicá el monto del ajuste"}` (nunca un
  valor por defecto en silencio). `adjustment_note` es opcional.
- Solo si la corrida está `open`. Pagada → `409 {"error":"La liquidación ya está
  pagada: no se puede modificar"}`.
- Nota **obligatoria** si el ajuste no es 0 → `400 {"error":"Explicá el ajuste en
  una nota"}`; hasta 200 caracteres y sin saltos de línea ni caracteres de
  control → `400 {"error":"La nota del ajuste no puede superar los 200
  caracteres"}` / `{"error":"La nota del ajuste no puede tener saltos de línea ni
  caracteres de control"}`.
- Ajuste entero con signo dentro de ±2.000.000.000 centavos (`domain.MaxPayCents`).
- El total de la línea nunca queda negativo → `400 {"error":"El ajuste dejaría el
  total en negativo"}`.
- El total de la línea tampoco supera el tope de dinero (2.000.000.000 centavos):
  un ajuste que lo empuja por encima responde
  `400 {"error":"El total de la línea no puede superar los 2000000000 centavos"}`
  (antes llegaba al `CHECK` de la DB y salía el genérico "un dato del pedido está
  fuera del rango permitido"). La creación de la corrida aplica la misma frase
  si la foto de la vista previa ya viene por encima del tope.
- `:uid` que no está en la corrida → `404 {"error":"no encontrado"}`. Corrida
  inexistente o de otro salón → `404`.
- La escritura repite `status='open'` en el SQL: una pagada es inmutable también
  para cualquier camino que no sea la API.

### `POST /salons/:id/payroll/runs/:rid/pay` → `200 run`

- Marca la corrida `paid`, `paid_at = now()` y `paid_by` en una transacción con
  `UPDATE ... WHERE status='open'`. Pagar otra vez, o dos pagos simultáneos
  (uno gana y el otro vuelve) → `409 {"error":"La liquidación ya está pagada: no
  se puede modificar"}`. Corrida inexistente o ajena → `404`.
- Desde ahí la liquidación es **inmutable**: ajustar, borrar y volver a pagar
  responden `409` con esa misma frase, y las queries de escritura no la tocan.

### `DELETE /salons/:id/payroll/runs/:rid` → `204`

Solo si está `open`: borra la corrida y sus líneas (cascada) y **libera los
días** para poder liquidarlos de nuevo. Pagada → `409` con la frase de arriba.
Inexistente o de otro salón → `404`.

### `GET /salons/:id/payroll/runs/:rid/export.csv` → `200`

`Content-Type: text/csv; charset=utf-8` y
`Content-Disposition: attachment; filename="nomina-<kind>-<from>-<to>.csv"`
(nombre armado **solo** con el tipo y las dos fechas: sin nombres ni datos del
salón). Se baja con `fetch` autenticado + blob, no con un enlace directo.

- UTF-8 **con BOM** (Excel abre bien los acentos) y encabezado exacto:
  `Profesional,Estado,Ventas,Total vendido,Comisión,Fijo,Ajuste,Nota del ajuste,Total`.
- `Estado` es `Activo` o `Dado de baja`. Montos como decimales con punto y **dos**
  decimales calculados desde los centavos **enteros** (sin float): `0.00`, `0.05`,
  `0.99`, `1.00`, `1234567.89`, `-12.50`.
- Comillas RFC 4180 (comillas dobles, comas y saltos de línea) y filas con CRLF.
- **Neutralización de fórmulas (D8)**: toda celda de TEXTO (nombre y nota) que
  empiece con `=`, `+`, `-`, `@`, tab o CR se prefija con una comilla simple
  (`=SUM(A1)` sale `'=SUM(A1)`), así abrir el archivo no ejecuta nada. La regla
  mira el **primer carácter no blanco**: se saltan los espacios Unicode
  iniciales (espacio, NBSP `U+00A0`, `U+2003`, `U+3000`, etc.) y también se
  neutralizan las formas de **ancho completo** `＝`, `＋`, `－` y `＠`
  (`" =SUM(A1)"` sale `"' =SUM(A1)"`). Los números y los nombres comunes no se
  tocan.
- Se puede exportar una liquidación abierta o pagada: los montos son la foto,
  nunca un recálculo.

### Permisos de las liquidaciones (D7)

| Ruta | owner | admin | professional | reception | dado de baja | otro salón |
| --- | --- | --- | --- | --- | --- | --- |
| `POST /salons/:id/payroll/runs` | 201 | 201 | 403 | 403 | 403 | 403 |
| `GET /salons/:id/payroll/runs` | 200 | 200 | 403 | 403 | 403 | 403 |
| `GET /salons/:id/payroll/runs/:rid` | 200 | 200 | 403 | 403 | 403 | 403/404 |
| `PATCH .../runs/:rid/lines/:uid` | 200 | 200 | 403 | 403 | 403 | 403/404 |
| `POST .../runs/:rid/pay` | 200 | 200 | 403 | 403 | 403 | 403/404 |
| `DELETE .../runs/:rid` | 204 | 204 | 403 | 403 | 403 | 403/404 |
| `GET .../runs/:rid/export.csv` | 200 | 200 | 403 | 403 | 403 | 403/404 |

`403` es `{"error":"sin permiso"}` cuando el rol no alcanza y `{"error":"sin
acceso al salón"}` cuando no hay membresía activa en el salón de la ruta. Sin
sesión, `401`. Un `:rid` de otro salón es `404` (no existe para este tenant).

## Fase 6 — F3b: gastos y ganancia neta

Gastos operativos (alquiler, insumos, servicios, otros) con fecha y categoría, y
el panel de **ganancia neta** = ingresos − gastos − nómina pagada por
día/semana/mes. Las decisiones del contrato son D1–D7:

- **D1 Base de caja**: la nómina entra a la ganancia SOLO cuando la liquidación
  está **pagada**, en el día local (zona del salón) de `paid_at`, por la suma de
  `payroll_lines.total_cents` congelados. Las liquidaciones abiertas y las
  comisiones sin liquidar **no cuentan**.
- **D2 Día local**: ingresos, gastos y nómina se agrupan por día local en
  `salons.timezone` (no UTC: no hereda H14). La ventana de cada día es
  `[00:00 local, 00:00 local del día siguiente)` con `time.Date`, correcta en los
  cambios de horario de verano (días de 23 y 25 horas). Los gastos guardan un
  día local (`spent_on DATE`): no dependen de la zona.
- **D3 Gasto**: `expenses(id, salon_id, category, amount_cents, spent_on,
  description, created_by, created_at)`. Categorías fijas `rent` (Alquiler),
  `supplies` (Insumos), `utilities` (Servicios) y `other` (Otros). Monto entero
  en centavos `1..2000000000` (CHECK en la base). `spent_on` entre `2000-01-01` y
  hoy local (no futuro). `description` recortada, hasta 200 **caracteres**, puede
  ser vacía.
- **D4 Edición**: crear, editar (PUT que **reemplaza**: exige las cuatro claves)
  y borrar (DELETE físico: es dinero operativo). Sin idempotencia por
  `client_op_id`.
- **D5 Permisos**: solo `owner` y `admin` en TODAS las rutas de gastos y
  ganancia; recepción, profesional y cliente reciben `403`; sin token, `401`;
  otro salón `403/404`. Toda consulta lleva `salon_id`: un gasto de otro salón es
  `404` `{"error":"Gasto no encontrado"}`.
- **D6 Lectura consistente**: el reporte lee ingresos, gastos y nómina en UNA
  sola transacción **REPEATABLE READ READ ONLY** (la misma técnica de `FindRun`):
  los tres componentes salen de la misma foto.
- **D7 No tocar lo existente**: no cambian `NewPayPeriod`, la nómina, el
  dashboard, `GET /sales` ni el cierre de caja. El período del reporte usa una
  función propia: `day` es nuevo; `week` (lunes–domingo) y `month` (calendario)
  reutilizan la derivación de la nómina sin cambiarla.

Objeto `expense` (forma congelada):

```json
{"id":"…","category":"rent|supplies|utilities|other","amount_cents":150000,
 "spent_on":"2026-03-19","description":"Alquiler","created_at":"2026-03-20T15:04:05Z",
 "created_by":"<user_id>"}
```

`spent_on` es una fecha local `YYYY-MM-DD`; `created_at` es RFC3339.

### `POST /salons/:id/expenses` → `201 expense`

Body `{category, amount_cents, spent_on, description?}`. La descripción es
opcional al crear y por defecto es `""`. Si falta `category`, `amount_cents` o
`spent_on`: `400 {"error":"Faltan datos del gasto: categoría, monto y fecha"}`.
Un `400` no escribe nada y un JSON de tipo equivocado (monto como texto o con
fracciones) es `400`, nunca `500`.

### `GET /salons/:id/expenses?from=&to=` → `200`

```json
{"expenses":[expense…],"truncated":false}
```

Ordenado por `spent_on` descendente y después `created_at` descendente, máximo
**500** filas (`truncated:true` si había más). `from` y `to` son obligatorios e
**inclusivos**; `to >= from`; rango máximo 366 días. Si no:
`400 {"error":"Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días"}`.

### `PUT /salons/:id/expenses/:eid` → `200 expense`

Body `{category, amount_cents, spent_on, description}`: las **cuatro** claves son
obligatorias (`description` puede ser `""`). Si falta una:
`400 {"error":"Faltan datos del gasto: categoría, monto, fecha y descripción"}` y
**no se escribe nada**. Inexistente o de otro salón:
`404 {"error":"Gasto no encontrado"}`.

### `DELETE /salons/:id/expenses/:eid` → `204`

`404 {"error":"Gasto no encontrado"}` si no existe o es de otro salón.

### Validaciones del gasto (400, textos exactos)

| Caso | Mensaje |
|---|---|
| categoría fuera del catálogo | `Categoría inválida: usá alquiler, insumos, servicios u otros` |
| monto `<= 0` | `El monto debe ser mayor a cero` |
| monto `> 2000000000` | `El monto no puede superar los 2000000000 centavos` |
| fecha mal formada | `Fecha inválida: usá AAAA-MM-DD` |
| fecha futura (día local del salón) | `La fecha del gasto no puede ser futura` |
| fecha anterior a `2000-01-01` | `La fecha del gasto es demasiado antigua` |
| descripción de más de 200 caracteres | `La descripción no puede superar los 200 caracteres` |
| descripción con un carácter NUL (`\u0000`) | `La descripción tiene caracteres no válidos` |

Nota de zona horaria: la zona del salón puede cambiarse mientras no exista ninguna liquidación. Si cambia, los ingresos se vuelven a agrupar por el día local de la zona nueva, pero un gasto conserva su `spent_on` guardado: uno cargado como «hoy» en una zona adelantada puede pasar a ser «futuro» en la nueva y su edición (PUT) dará 400 `La fecha del gasto no puede ser futura` hasta corregir la fecha; borrarlo siempre se puede. Con una liquidación pagada o abierta la zona queda congelada (F3.3).

### `GET /salons/:id/profit?kind=day|week|month&date=YYYY-MM-DD` → `200`

```json
{"period":{"kind":"week","from":"2026-03-02","to":"2026-03-08"},
 "totals":{"revenue_cents":75000,"expenses_cents":410000,"payroll_cents":30000,
           "net_cents":-365000},
 "days":[{"date":"2026-03-02","revenue_cents":50000,"expenses_cents":10000,
          "payroll_cents":0,"net_cents":40000}],
 "expenses_by_category":[{"category":"supplies","total_cents":10000}]}
```

`date` es opcional: sin él, el período es **hoy** en la zona del salón. El
servidor deriva el período (día = ese día; semana lunes–domingo; mes calendario)
y `from`/`to` son inclusivos. `days` trae **todos** los días locales del período,
ascendentes y con ceros. `net_cents = revenue_cents - expenses_cents -
payroll_cents` (puede ser negativo) y cada total es la suma **exacta** de `days`.
`expenses_by_category` solo trae categorías con total `> 0`, ordenadas por
código. Un `kind` o un `date` inválidos son `400 {"error":"Período inválido"}`.
Un salón sin movimientos devuelve ceros (no `500`).

### Permisos de gastos y ganancia (D5)

| Ruta | owner | admin | reception | professional | client | otro salón |
| --- | --- | --- | --- | --- | --- | --- |
| `POST /salons/:id/expenses` | 201 | 201 | 403 | 403 | 403 | 403 |
| `GET /salons/:id/expenses` | 200 | 200 | 403 | 403 | 403 | 403 |
| `PUT /salons/:id/expenses/:eid` | 200 | 200 | 403 | 403 | 403 | 403/404 |
| `DELETE /salons/:id/expenses/:eid` | 204 | 204 | 403 | 403 | 403 | 403/404 |
| `GET /salons/:id/profit` | 200 | 200 | 403 | 403 | 403 | 403 |

`403` es `{"error":"sin permiso"}` cuando el rol no alcanza y
`{"error":"sin acceso al salón"}` cuando no hay membresía activa en el salón de la
ruta. Sin sesión, `401`.

## Fase 6 — F1: enlace firmado de un solo uso para cancelar o reprogramar

El email de la reserva (confirmación y recordatorio) lleva un enlace con el que la
persona **cancela o mueve su turno sin cuenta ni sesión**. El enlace es una
capability: quien lo tiene puede tocar ESE turno y nada más.

Reglas (D1..D9 del contrato de la fase 6):

- **D1 — un token por email.** Se emite un token NUEVO cada vez que se manda un
  correo con enlace (confirmación, recordatorio y el aviso de una
  reprogramación). Solo existe para un turno **con email**: un turno sin email no
  tiene enlace. El token son 32 bytes de `crypto/rand` en base64url sin relleno
  (43 caracteres), el mismo generador de las invitaciones de equipo; de él se
  guarda **solo su SHA-256** (`booking_links.token_hash`, 64 hex, `UNIQUE`).
  Nunca se loguea ni se devuelve: solo viaja en el fragmento del enlace del email
  (`<APP_URL>/turno#<token>`).
- **D2 — un solo uso exitoso.** El primer cancelar o reprogramar que sale bien
  consume el token (`used_at`); el que falla por horario ocupado, por menos de 2
  horas o por cualquier validación **no** lo consume (el reclamo se hace dentro
  de la misma transacción del cambio y el error la deshace). Reprogramar emite un
  token nuevo y el viejo muere. Los otros tokens vivos del mismo turno siguen
  valiendo hasta que se usen o el turno deje de estar vigente.
- **D3 — vigencia en vivo, sin `expires_at`.** El token sirve solo si su turno
  sigue `booked` y faltan **2 horas o más** (`domain.LinkMinNoticeHours`). Si el
  personal lo cancela, el enlace muere; si lo mueve, el enlace vale contra el
  horario nuevo. Con menos de 2 horas el turno se muestra (`can_change: false`)
  pero no se puede tocar.
- **D4 — una transacción con un solo lock.** Reclamar el token, validar y cambiar
  el turno ocurren en UNA transacción con `pg_advisory_xact_lock('booking-write:' ||
  salón)`: el mismo lock de la reserva pública y del PATCH de la agenda, sin
  ningún otro lock ni una segunda conexión del pool. Dos pedidos simultáneos con
  el mismo token: exactamente uno sale bien y el otro recibe `404`.
  El EXCLUDE de solape de `bookings` sigue siendo la última barrera.
- **D5 — reprogramar = una reserva nueva.** El horario nuevo tiene que entrar en
  la ventana del salón y del profesional, no pisar una ausencia, dejar libre una
  unidad de equipamiento (`resource_unit_id`, puede cambiar de unidad) y no
  solaparse con otro turno, con el turno que se mueve excluido de los conflictos.
  Se apoya en la variante nueva `AvailabilityService.DayExcluding`; `Day` no
  cambia (delega con un id vacío).
- **D6 — respuestas sin filtrar datos.** Un token inexistente, ya usado, mal
  formado o de un turno que ya no está vigente da SIEMPRE el mismo
  `404 {"error":"Enlace inválido o vencido"}`. Con un token vigente la respuesta
  no lleva teléfono, ni email, ni ids internos del cliente. Las cuatro rutas son
  `POST` con el token en el **cuerpo** (nunca en la ruta ni en el query).
- **D7 — límite propio.** Las cuatro rutas van bajo un cubo por IP propio
  (`PublicBookingLinkLimit`, 20/min, el valor de `/auth`) que **no comparte** el
  cubo de la reserva pública ni el de las invitaciones. Al pasarse:
  `429 {"error":"demasiados intentos, esperá un minuto y volvé a probar"}`.
- **D8 — eventos y correos.** Cancelar o reprogramar publica el MISMO
  `booking.updated` y el MISMO aviso interno que `BookingService.Update`, y manda
  al cliente un correo del cambio (cancelado, o el horario nuevo con el enlace
  nuevo) en segundo plano: un SMTP caído se loguea y **nunca** cambia la
  respuesta. Las plantillas de confirmación y recordatorio reemplazan «escribile
  al salón» por el enlace y conservan una línea de contacto.
- **D9 — lo existente no cambia.** `Book`, el PATCH de la agenda, la cobertura, la
  disponibilidad `Day`, el POS y los permisos del personal quedan igual.

### `POST /api/v1/public/booking-links/lookup {token}` → `200`

```json
{"booking":{"salon_name":"Navajas de Oro","service_name":"Corte",
  "professional_name":"Ana","starts_at":"2026-12-01T15:00:00Z",
  "ends_at":"2026-12-01T15:45:00Z","timezone":"America/Mexico_City",
  "can_change":true,"min_notice_hours":2}}
```

`starts_at`/`ends_at` en RFC3339 UTC; la hora se muestra en `timezone`.
`can_change` es `false` cuando faltan menos de 2 horas.

### `POST /api/v1/public/booking-links/slots {token, date}` → `200`

Misma forma que la disponibilidad pública
(`{"date":"YYYY-MM-DD","slots":[{"professional_id","starts_at","ends_at"}]}`), para
el MISMO servicio y el MISMO profesional del turno, con el propio turno excluido
de los conflictos. `date` es un día local del salón.

| Caso | Respuesta |
| --- | --- |
| ok | `200` con los huecos |
| fecha mal formada | `400 {"error":"Fecha inválida: usá AAAA-MM-DD"}` |
| menos de 2 horas | `409 {"error":"Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo"}` |
| token que no sirve | `404 {"error":"Enlace inválido o vencido"}` |

### `POST /api/v1/public/booking-links/cancel {token}` → `200`

`{"status":"cancelled"}`. El hueco queda libre de inmediato para una reserva
pública nueva. Menos de 2 horas: `409` con el texto de arriba. Un token usado, de
un turno cerrado o pasado: `404`.

### `POST /api/v1/public/booking-links/reschedule {token, starts_at}` → `200`

`{"booking":{...misma forma que lookup...},"new_link_sent":bool}`.
`new_link_sent` es `true` si el correo con el enlace nuevo salió o quedó encolado
en segundo plano; **el token nuevo nunca viaja en la respuesta**. `starts_at` en
RFC3339.

Reglas del horario nuevo (las de una reserva pública nueva, con el propio turno excluido):
tiene que ser **futuro**; la **duración es la del servicio hoy** (no la que tenía el turno
al reservarse); el profesional y el servicio no cambian; la unidad de equipo se
conserva solo si sigue **activa** y libre (si no, se toma otra activa libre). El token
tiene que llegar **exacto**: un token con espacios, tabs o saltos de línea alrededor es
el `404` genérico (no se recorta).

| Caso | Respuesta |
| --- | --- |
| ok | `200` con el turno movido |
| el mismo instante | `400 {"error":"Elegí un horario distinto al actual"}` |
| horario pasado o «ahora», ocupado, fuera de horario, ausencia, sin equipo o con la única unidad de equipo desactivada | `409 {"error":"Ese horario ya no está disponible: elegí otro"}` (el token sigue vivo) |
| menos de 2 horas | `409` con el texto de la ventana mínima |
| token que no sirve | `404 {"error":"Enlace inválido o vencido"}` |

Errores comunes a las cuatro: `400 {"error":"body inválido"}` (JSON roto, tipo
equivocado —incluido un token que no es una cadena— o `starts_at`/`date`
ausentes) y `429` por velocidad. Nunca un `500` por entrada del cliente.

## Fase 6 — F4: exportación CSV de ventas, cierres de caja y clientes

Salida **masiva** de datos personales y de dinero: las tres rutas son solo
**dueño y administración** (D1). Recepción, profesional y cliente reciben `403`
`{"error":"sin permiso"}` aunque hoy puedan ver esas listas en pantalla; sin
sesión `401`; pedir el salón de otro tenant `403` (`sin acceso al salón`). No
cambian los permisos de `GET /sales`, `/cash-closes` ni `/clients` (D8: sus
repositorios y queries no se tocan; las lecturas del export son métodos nuevos
con `LIMIT`, una sola sentencia SQL por archivo).

- **D2 día local**: `from`/`to` son días **locales** del salón
  (`salons.timezone`), inclusivos, con la ventana `[from 00:00 local, (to+1)
  00:00 local)` calculada con `time.Date` (correcta en los días de 23 y 25
  horas). Las fechas del archivo se escriben en esa zona como
  `AAAA-MM-DD HH:MM`; el alta de un cliente como `AAAA-MM-DD`. Los clientes no
  llevan rango. Rango máximo **366 días**.
- **D3 sin truncar en silencio**: tope de **50000 filas** por archivo. La
  consulta pide `tope + 1`; si hay más, `400` y **ningún archivo parcial**. El
  tope es un campo del caso de uso (`ExportService.RowCap`): las pruebas lo
  bajan en vez de cargar 50000 filas.
- **D4 mínimo necesario**: las ventas **no** llevan teléfono ni email de la
  clienta (solo el nombre tal como se anotó en la venta); los montos son
  decimales con punto, sin símbolo de moneda; el método sale en español
  (`Efectivo`, `Tarjeta`, `Transferencia`); sin filas → `200` con **solo la
  cabecera**.
- **D5 fórmulas**: toda celda de TEXTO (profesional, cliente, notas, teléfono,
  email, nota del cierre) pasa por `CSVCell` (la regla de la nómina, sin
  cambios); las celdas NUMÉRICAS (montos, cantidades) se escriben crudas, así
  una diferencia de caja negativa sigue siendo un número (`-5.00`). Un teléfono
  que empieza con `+` sale con la comilla simple de neutralización
  (`'+54 11 5555-1234`): es el comportamiento deliberado de `CSVCell`.
- **D6 cabeceras HTTP**: `Content-Type: text/csv; charset=utf-8`,
  `Content-Disposition: attachment; filename="..."` con el nombre saneado por
  `csvSafe` (solo `[a-z0-9-]`, guiones y el punto del `.csv`), `Cache-Control:
  no-store` y `X-Content-Type-Options: nosniff`.
- **D7 rastro**: sin tabla de auditoría (F10), cada export deja **una** línea de
  log del servidor `export csv: tipo=<ventas|cierres|clientes> salon=<id>
  usuario=<id> filas=<n>`, sin ningún dato personal, monto ni contenido. Un
  `400`/`403` no es un export y no deja línea.

### `GET /salons/:id/export/sales.csv?from=YYYY-MM-DD&to=YYYY-MM-DD` → `200`

Cabecera exacta `Fecha,Venta,Profesional,Cliente,Método,Ítems,Total`: una fila
por **venta**, más reciente primero (`created_at DESC, id`). `Venta` es el id;
`Profesional` el nombre de la cuenta (vacío si no existe); `Ítems` la suma de
cantidades de sus líneas; `Total` como decimal con punto. Archivo
`ventas-<from>_<to>.csv`.

### `GET /salons/:id/export/cash-closes.csv?from=&to=` → `200`

Cabecera exacta `Apertura,Cierre,Efectivo del sistema,Efectivo contado,Diferencia,Nota`:
cierres con `closed_at` dentro de la ventana, más reciente primero. `Diferencia`
conserva su signo. Archivo `cierres-<from>_<to>.csv`.

### `GET /salons/:id/export/clients.csv` → `200`

Cabecera exacta `Nombre,Teléfono,Email,Notas,Alta`, ordenado por nombre. `Alta`
es el día local de `created_at`. Archivo `clientes-<hoy local>.csv`.

### Errores de la exportación

| Caso | Status | Texto exacto |
| --- | --- | --- |
| sin `from`/`to`, fecha mal formada, `to` < `from`, más de 366 días | 400 | `Rango inválido: indicá desde y hasta (AAAA-MM-DD), hasta 366 días` |
| más de 50000 ventas o cierres | 400 | `El archivo tiene más de 50000 filas: elegí un rango más corto` |
| más de 50000 clientes | 400 | `Hay más de 50000 clientes: no se puede exportar de una vez` |
| rol sin permiso | 403 | `sin permiso` |
| sin token | 401 | — |

Nunca un `500` por entrada de la persona. Un `400` no devuelve archivo ni anuncia
`Content-Disposition`.

Tope de tiempo: cada lectura masiva se cancela a los 25 s (por debajo de los 30 s de escritura del servidor); si lo supera la respuesta es un `500` genérico y la consulta NO queda viva en Postgres reteniendo una conexión del pool.

## Cambios de comportamiento en endpoints existentes

- **`DELETE /salons/:id/members/:uid`** (F2g2): ya no borra la membresía, la
  **da de baja** (`ended_at`) conservando historial, horarios, ausencias y
  servicios; responde `204` igual que antes y `409` si la persona tiene turnos
  futuros sin cancelar. Los `403`/`404` del resto de la pantalla no cambian: la
  autorización sigue siendo solo `owner`/`admin` del salón, y la regla de rol es
  la de la baja (el dueño baja a cualquiera; la administración solo a
  profesional, recepción o cliente; el último owner activo es `400`). Ver «Fase
  6 — F2g2: dar de baja en vez de borrar».
- **Correos a clientes (F1)**: la confirmación de la reserva pública y el
  recordatorio de 24 h ya no dicen «escribile al salón» como única salida: llevan
  un enlace de un solo uso (`<APP_URL>/turno#<token>`) para cancelar o
  reprogramar y **conservan una línea de contacto**. Un turno sin email sigue sin
  correo y sin enlace. Si el token no se puede emitir (crypto/DB caídos), el
  correo sale igual con la línea de contacto: el aviso no se pierde.
- **`POST /salons/:id/sales`**: el precio unitario **lo pone el catálogo**. El
  `unit_price_cents` del body se usa solo para validar el pedido (fuera de rango →
  `400`); el que se cobra y se persiste es el `price_cents` del servicio. Un
  servicio inactivo ya no se vende (`400`). El `professional_id` tiene que ser
  personal del salón que **atiende** (`400` si no): rol `professional`, o
  `owner`/`admin` con `attends = true` (F2a).
  Desde F3.1 cada línea guarda además la **foto** del `commission_pct` que el
  servicio tenía al venderse (`sale_items.commission_pct`, migración 023): una
  venta nueva la copia ítem por ítem y editar el porcentaje del catálogo después
  **no** cambia lo ya vendido; el repetido por `client_op_id` devuelve la venta
  original con su foto original. Las ventas anteriores quedan en `NULL` (sin
  backfill) y el **JSON de la venta no cambia**: `commission_pct` es interno y no
  se expone en ninguna respuesta.
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

Migración de la fase 6 (F2d2):

- `018_time_off.sql`:
  - `time_off(id TEXT PRIMARY KEY, salon_id TEXT NOT NULL REFERENCES salons(id)
    ON DELETE CASCADE, user_id TEXT NOT NULL, starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL, reason TEXT NOT NULL DEFAULT '', created_by
    TEXT, created_at TIMESTAMPTZ NOT NULL DEFAULT now())`, FK `(user_id,
    salon_id)` a `memberships` **`ON DELETE CASCADE`** (si la persona sale del
    equipo, sus ausencias se van con ella), `CHECK (starts_at < ends_at)`, tope
    del motivo `CHECK (char_length(reason) <= 120)` (nombre `time_off_reason_len`)
    e índice `(salon_id, user_id, starts_at)`.
  - **Sin backfill**: una base migrada no tiene ausencias hasta que alguien carga
    una, así que el comportamiento de hoy no cambia.
  - "Dos ausencias de la misma persona no se pisan" **no** es un constraint: el
    mensaje tiene que ser el del contrato (`Ya hay una ausencia en ese período`) y
    lo verifica el caso de uso contra las filas existentes, antes de escribir.
  - Guardas miradas en `current_schema()` y `CREATE INDEX IF NOT EXISTS`: una
    segunda ejecución no falla ni duplica nada.

Fase 6 (F2d3): **sin migración nueva**. Reasignar es un `UPDATE` del
`professional_id` de un turno y la garantía anti-solape es la `EXCLUDE
bookings_no_overlap` de la 002; el "todo o nada" lo da la transacción del
adaptador Postgres (`pg_advisory_xact_lock` por salón), no el esquema.

Migración de la fase 6 (F2e):

- `020_resources.sql`:
  - `resource_types(id TEXT PRIMARY KEY, salon_id TEXT NOT NULL REFERENCES
    salons(id) ON DELETE CASCADE, name TEXT NOT NULL, created_at TIMESTAMPTZ NOT
    NULL DEFAULT now())` con `CHECK (length(btrim(name)) BETWEEN 1 AND 60)` e
    índice único `resource_types_salon_name_idx (salon_id, lower(name))` (el
    nombre es único por salón sin importar mayúsculas).
  - `resource_units(id TEXT PRIMARY KEY, salon_id TEXT NOT NULL REFERENCES
    salons(id) ON DELETE CASCADE, type_id TEXT NOT NULL REFERENCES
    resource_types(id) **ON DELETE RESTRICT**, name TEXT NOT NULL, active BOOLEAN
    NOT NULL DEFAULT true, created_at TIMESTAMPTZ NOT NULL DEFAULT now())` con el
    mismo `CHECK` de nombre y el índice único
    `resource_units_salon_type_name_idx (salon_id, type_id, lower(name))`.
  - `services.resource_type_id TEXT NULL REFERENCES resource_types(id) ON DELETE
    RESTRICT` y `bookings.resource_unit_id TEXT NULL REFERENCES resource_units(id)
    ON DELETE RESTRICT`, agregadas con guardas de `information_schema` (las
    columnas nacen `NULL`: **sin backfill**, un servicio sin tipo se comporta
    como antes).
  - `EXCLUDE USING gist (salon_id WITH =, resource_unit_id WITH =,
    tstzrange(starts_at, ends_at) WITH &&) WHERE (status = 'booked' AND
    resource_unit_id IS NOT NULL)`, con el nombre de guarda
    `bookings_resource_no_overlap` (mismo patrón que `bookings_no_overlap` de la
    002): la capacidad de la última unidad la garantiza la base, no un contador.
  - Índices `bookings_resource_unit_idx (salon_id, resource_unit_id, starts_at)
    WHERE resource_unit_id IS NOT NULL` (ocupación de la ventana) y
    `services_resource_type_idx (salon_id, resource_type_id) WHERE
    resource_type_id IS NOT NULL` (uso del tipo y rechazo del borrado).

Migración de la fase 6 (F2g1):

- `021_invitations.sql`:
  - `invitations(id TEXT PRIMARY KEY, salon_id TEXT NOT NULL REFERENCES salons(id)
    ON DELETE CASCADE, email TEXT NOT NULL, role TEXT NOT NULL, attends BOOLEAN NOT
    NULL DEFAULT false, token_hash TEXT NOT NULL, expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ, accepted_by TEXT NOT NULL DEFAULT '', revoked_at
    TIMESTAMPTZ, created_by TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT
    NULL DEFAULT now())` con `CHECK (char_length(token_hash) = 64)` (SHA-256 en
    hex, nombre `invitations_token_hash_len`) y `CHECK (char_length(email) <= 254)`
    (`invitations_email_len`).
  - `invitations_token_hash_key`: índice **único** de `token_hash` (el canje es
    inequívoco y la búsqueda por token — la única sin `salon_id` — es barata).
  - `invitations_pending_email_idx`: índice **único parcial** de
    `(salon_id, lower(email)) WHERE accepted_at IS NULL AND revoked_at IS NULL`:
    una sola pendiente por email y salón. Revocadas, vencidas y aceptadas no
    ocupan el lugar.
  - **Sin backfill**: una base migrada no tiene invitaciones hasta que alguien
    emite una. La vigencia **no** se chequea contra `created_at` a propósito: un
    entorno de prueba adelanta `expires_at` para probar el vencimiento y el que
    decide es el `UPDATE` condicional del canje.
  - Guardas en `pg_indexes` de `current_schema()` y `CREATE INDEX IF NOT EXISTS`:
    una segunda ejecución no falla ni duplica nada.

Migración de la fase 6 (F3.2):

- `024_member_pay.sql`: tabla `member_pay(salon_id TEXT NOT NULL REFERENCES
  salons(id) ON DELETE CASCADE, user_id TEXT NOT NULL, fixed_cents INTEGER NOT
  NULL DEFAULT 0, fixed_period TEXT NOT NULL DEFAULT 'month', commission_enabled
  BOOLEAN NOT NULL DEFAULT true, PRIMARY KEY (salon_id, user_id))` con
  `CONSTRAINT member_pay_membership_fkey FOREIGN KEY (user_id, salon_id)
  REFERENCES memberships(user_id, salon_id) ON DELETE CASCADE` (el modelo de pago
  solo existe para alguien del salón, baja incluida),
  `member_pay_fixed_cents_range CHECK (fixed_cents >= 0 AND fixed_cents <=
  2000000000)` (el tope del dinero de `010_limits`) y
  `member_pay_fixed_period_check CHECK (fixed_period IN
  ('week','fortnight','month'))`.
  - **Sin backfill**: sin fila rige el default de D3 (fijo 0, mes, comisión
    encendida), así que una base migrada se comporta exactamente como antes.
  - Guarda de `information_schema` mirada en `current_schema()` (igual que
    015–023): una segunda ejecución no hace nada y, sobre una base con datos, no
    cambia ninguna fila.

Las 21 migraciones se probaron aplicándolas dos veces con
`psql -v ON_ERROR_STOP=1` sobre una base limpia y sobre una ya migrada, y el
binario arranca contra esas bases. La 020 se probó además sobre una base migrada
**solo hasta la 019** con dos servicios y dos turnos ya creados: los servicios
quedan con `resource_type_id` NULL, los turnos con `resource_unit_id` NULL y no
se crea ningún tipo ni unidad (el comportamiento de hoy no cambia). El backfill de la 016 se probó además sobre
una base migrada **solo hasta la 015**, con dueño/administración/profesional/
recepción, dos servicios y un turno ya creados: los tres reservables quedan con
los dos servicios, recepción con ninguno y el turno con `service_id` NULL. La 024
se probó además sobre una base migrada **solo hasta la 023** con un salón, dos
miembros, un servicio y una venta con dos líneas (una con foto y otra `NULL`):
las dos pasadas no cambian ninguna fila, y el modelo de pago de un miembro se
guarda y se lee mientras la FK compuesta rechaza a quien no es del salón.

Migración de la fase 6 (F3b):

- `026_expenses.sql`: tabla `expenses(id TEXT PRIMARY KEY, salon_id TEXT NOT NULL
  REFERENCES salons(id) ON DELETE CASCADE, category TEXT NOT NULL, amount_cents
  INTEGER NOT NULL, spent_on DATE NOT NULL, description TEXT NOT NULL DEFAULT '',
  created_by TEXT REFERENCES users(id), created_at TIMESTAMPTZ NOT NULL DEFAULT
  now())` con los CHECK del contrato (`category IN ('rent','supplies','utilities',
  'other')`, `amount_cents BETWEEN 1 AND 2000000000`, `spent_on >= DATE
  '2000-01-01'`, `char_length(description) <= 200`) e índice `expenses_salon_spent_idx
  (salon_id, spent_on DESC, created_at DESC)` para el listado. Guardas de
  `information_schema` / `pg_indexes` miradas en `current_schema()` (igual que
  015–025) y **sin backfill**: una segunda pasada no hace nada y sobre una base
  con datos no cambia ninguna fila.

La 026 se probó aplicándola **dos veces** con `psql -v ON_ERROR_STOP=1` sobre una
base limpia y sobre una migrada **solo hasta la 025** con un salón, dos miembros,
un servicio, una venta con su línea y una liquidación pagada: las dos pasadas no
cambian las filas existentes (md5 igual) y la base queda igual. Los tests de
integración (`SALON_IT=1`, base fresca) cubren además los CHECK/FK con inserciones
directas, el scope por salón en las cuatro operaciones de gastos y los bordes de
día local (23:59/00:00 en Nueva York y Auckland, día de 23 h del 8/3/2026 y de
25 h del 1/11/2026), la liquidación abierta que no suma y la pagada que sí, el
salón vacío en ceros y las lecturas concurrentes con altas y borrados.

Migración de la fase 6 (F1):

- `027_booking_links.sql`: tabla `booking_links(id TEXT PRIMARY KEY, salon_id TEXT
  NOT NULL REFERENCES salons(id) ON DELETE CASCADE, booking_id TEXT NOT NULL
  REFERENCES bookings(id) ON DELETE CASCADE, token_hash TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(), used_at TIMESTAMPTZ, CONSTRAINT
  booking_links_token_hash_len CHECK (char_length(token_hash) = 64))` con índice
  único `booking_links_token_hash_key (token_hash)` —es la única lectura del
  sistema, junto con la de invitaciones, que no va scopeada por salón: el hash es
  la capability— e índice `booking_links_booking_id_idx (booking_id)` para el
  borrado en cascada del turno. **Sin `expires_at`**: la vigencia la decide el
  turno (D3). **Sin backfill**: las reservas existentes no tienen enlace hasta su
  próximo email. Guardas de `pg_indexes` miradas en `current_schema()` (igual que
  015–026).

La 027 se probó aplicándola **dos veces** con `psql -v ON_ERROR_STOP=1` sobre una
base limpia (todas las migraciones en orden, dos pasadas: `pg_dump` idéntico
salvo la marca aleatoria de `pg_dump`) y sobre una base migrada **solo hasta la
026** con un salón, un usuario, una membresía, un servicio, un turno y su fila de
email: las filas existentes quedan idénticas. Los tests de integración
(`SALON_IT=1`, base fresca) cubren además el flujo completo, 16 pedidos
simultáneos con el mismo token (al cancelar y al reprogramar) con exactamente un
`200`, un horario ocupado que no consume el token, dos reprogramaciones distintas
al mismo hueco con un solo ganador y sin solape, el turno de otro salón, el enlace
del recordatorio y el `CHECK` del hash.

## F5: historial de la clienta, alergias y vínculo por id (fase 6)

Un dato de salud (`allergies`) y el dinero de la clienta (`spend`/`sales`) son
información sensible: los permisos de esta sección son parte del contrato.

### Ficha

- `POST /salons/:id/clients` acepta `allergies` **opcional** (por defecto `""`).
  `GET /salons/:id/clients` agrega `allergies` a cada ficha (campo nuevo: nada se
  quita ni cambia). El export CSV de clientes (F4) **no** cambia.
- `GET /salons/:id/clients/:cid` → 200 `{"id","salon_id","name","phone","email","notes","allergies","created_at"}`.
  Lo lee **cualquier miembro activo** (la misma regla del listado). Una ficha de
  otro salón es `404 {"error":"Ficha no encontrada"}`.
- `PUT /salons/:id/clients/:cid` **reemplaza** los cinco campos
  `{name, phone, email, notes, allergies}` (email, notas y alergias pueden ser
  `""`). Solo owner/admin/recepción; el resto `403 {"error":"sin permiso"}`.
  - Faltó una clave: `400 {"error":"Faltan datos de la ficha: nombre, teléfono, email, notas y alergias"}`.
  - Alergias de más de 500 caracteres: `400 {"error":"Las alergias no pueden superar los 500 caracteres"}`.
  - Un NUL en cualquiera de los cinco campos: `400 {"error":"La ficha tiene caracteres no válidos"}`.
  - Las demás validaciones son las del alta (nombre ≤ 120, teléfono ≤ 40, email
    válido o vacío, notas ≤ 2000). Un `400` **no escribe nada**.

### Historial

`GET /salons/:id/clients/:cid/history` → 200:

```json
{"client_id":"c_...",
 "summary":{"visits":4,"no_shows":1,"cancelled":2,"upcoming":1,
            "last_visit":"2026-03-07"|null,"next_visit":"2026-03-20T16:00:00Z"|null,
            "timezone":"America/Mexico_City"},
 "bookings":[{"id","starts_at","status","service_name","professional_name"}],
 "spend":null|{"sales_count":2,"total_cents":123400},
 "sales":null|[{"id","created_at","total_cents","method"}]}
```

- Visita = turno `done`; `no_shows`, `cancelled` y `upcoming` (`booked` futuros)
  son conteos del mismo cliente.
- `last_visit` es el día **LOCAL** del salón del turno `done` más reciente;
  `next_visit` es el próximo `booked` futuro en RFC3339.
- `spend.total_cents` suma **solo** las ventas vinculadas (`sales.client_id`): el
  gasto anterior a F5 no se atribuye. Cada lista tiene `LIMIT 20`, más reciente
  primero.
- Lo lee cualquier miembro activo. El dinero (`spend` y `sales`) solo
  owner/admin/recepción; para un profesional y para el rol cliente vienen en
  **`null`**. Ficha inexistente o de otro salón: `404 {"error":"Ficha no encontrada"}`.

### POS

`POST /salons/:id/sales` acepta `client_id` **opcional**. Si viene, tiene que ser
una ficha del mismo salón: si no, `400 {"error":"Ficha de clienta inválida"}` y la
venta **no se guarda**. Si `client_name` viene vacío se completa con el nombre de
la ficha. Sin `client_id` no hay vínculo (no se infiere por nombre). La respuesta
agrega `client_id` (`""` si no hay). El replay por `client_op_id` devuelve la
venta original sin tocar su vínculo.

Los turnos se vinculan solos, con un `UPDATE` aparte **best-effort** (un fallo se
loguea y nunca tumba el turno ni la venta): la reserva pública con la ficha que
crea o reusa, el turno del personal con la **única** ficha cuyo teléfono
normalizado coincide (nunca crea una ficha). Un cambio de teléfono **no**
desvincula.

### Migración `028_client_links.sql`

- `clients.allergies TEXT NOT NULL DEFAULT ''` + `CHECK (char_length(allergies) <= 500)`.
  Sin relleno desde `notes`.
- `bookings.client_id` y `sales.client_id` (`TEXT NULL`, FK a `clients(id)
  ON DELETE SET NULL`) con índice parcial `(salon_id, client_id) WHERE client_id
  IS NOT NULL`.
- Relleno **acotado** (solo turnos): el teléfono normalizado (solo dígitos, no
  vacío) coincide con **exactamente una** ficha del mismo salón; los ambiguos o
  sin coincidencia quedan en `NULL`. No toca filas que ya tienen `client_id` y no
  rellena ventas. Idempotente: una segunda pasada no cambia nada.
