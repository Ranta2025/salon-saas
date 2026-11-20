# API — notificaciones, realtime, clientes y adjuntos (Fase 4, backend)

Base: `/api/v1`. Todo requiere `Authorization: Bearer <jwt>` salvo donde se
indica. El salón de cada request sale de la **membership** (`users × salons ×
role`): sin membership en el `:id` la respuesta es `403`; un recurso de otro
salón responde `404` (no se filtra ni su existencia).

Reglas transversales (las mismas de la fase 3):

- **Dinero en centavos enteros**. Nunca floats.
- **Todo scopeado por `salon_id`**: salones, clientes, fotos, avisos.
- Fechas `RFC3339` en UTC.
- Errores `{"error": "<mensaje>"}` con `400` validación / `401` sin token /
  `403` sin permiso / `404` no existe / `409` duplicado.
- Adjuntos: solo `image/jpeg`, `image/png`, `image/webp`, máximo **8 MB**. Lo
  que no sea imagen se rechaza con `400`.

| Acción | owner | admin | reception | professional | client |
|---|---|---|---|---|---|
| `GET /salons/:id/notifications` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST /notifications/:nid/read` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `GET /salons/:id/clients` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST /salons/:id/clients` | ✅ | ✅ | ✅ | ❌ | ❌ |
| `GET /salons/:id/clients/:cid/photos` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST /salons/:id/clients/:cid/photos` | ✅ | ✅ | ✅ | ❌ | ❌ |
| `DELETE /salons/:id/clients/:cid/photos/:pid` | ✅ | ✅ | ✅ | ❌ | ❌ |
| `GET /photos/:pid` | público (id de capacidad) | | | | |
| `GET /ws?token=` | cualquier miembro de algún salón | | | | |

## Notificaciones

Aviso persistido (sobrevive al refresh; el websocket es solo el "ahora"):

```json
{ "id": "n_...", "salon_id": "sal_...", "user_id": "u_...",
  "title": "Nuevo turno", "body": "Lía · 03/12 10:00", "kind": "booking",
  "read": false, "read_at": null, "created_at": "2026-12-03T09:12:00Z" }
```

- `user_id` **no aparece** cuando el aviso es para todo el salón (`kind` de
  aviso general). Un aviso general lo puede marcar leído cualquier miembro.
- `kind`: `info` | `booking` | `sale` | `cash`.
- `read_at` es `null` mientras no se leyó; `read` es el booleano derivado.

### `GET /salons/:id/notifications?limit=50`

Devuelve los avisos visibles para quien pide: los del salón completo y los
dirigidos a esa persona, más nuevos primero. `limit` default `50`, máximo `200`.

```json
{ "notifications": [ { "id": "...", "title": "...", "kind": "booking",
                       "read": false, "salon_id": "...", "body": "...",
                       "read_at": null, "created_at": "..." } ],
  "unread": 1 }
```

`unread` es **aditivo** al contrato acordado (el badge no necesita otra
llamada). El frontend puede ignorarlo y contar `read: false`.

### `POST /notifications/:nid/read` → `204` (sin body)

Idempotente. `404` si el aviso no existe, es de otro salón o está dirigido a
otra persona. La ruta no lleva `:salon_id`: el alcance se resuelve con las
memberships del usuario del token.

Las notificaciones las crea el backend, no el cliente: hoy una reserva crea
`Nuevo turno` (`kind: booking`) y un cierre de caja crea `Cierre de caja`
(`kind: cash`, con esperado/contado/diferencia en el body).

## Realtime — `WS /api/v1/ws?token=<jwt>`

El JWT viaja por query param (un WebSocket del navegador no puede mandar
`Authorization`). Se valida **antes** del upgrade: sin token válido la conexión
se rechaza con `401`; un usuario sin ningún salón, con `403`. La conexión se une
a la room de **cada salón del que el usuario es miembro**.

Formato de cada mensaje:

```json
{ "type": "sale.created", "salon_id": "sal_...",
  "data": { "id": "sale_...", "total_cents": 300000, "method": "cash",
            "client_name": "Lía", "professional_id": "u_..." },
  "ts": "2026-12-03T10:00:00Z" }
```

| `type` | `data` |
|---|---|
| `booking.created` | `{id, professional_id, client_name, starts_at, ends_at, status}` |
| `booking.updated` | reservado (todavía no hay endpoint de edición de reservas; no se emite) |
| `sale.created` | `{id, total_cents, method, client_name, professional_id}` |
| `cash_closed` | `{id, system_cash_cents, counted_cents, difference_cents}` |
| `notify` | `{notification: {id, salon_id, title, body, kind, read, read_at, created_at}}` |

Aislamiento: un evento se publica a **una** room. Un cliente del salón A nunca
recibe un evento del salón B (hay test que lo prueba y una mutación de control
que lo hace fallar).

## Clientes

```json
{ "id": "cli_...", "salon_id": "sal_...", "name": "Lía Fernández",
  "phone": "11 5555-1234", "notes": "prefiere turnos de mañana",
  "created_at": "2026-12-03T09:00:00Z" }
```

### `POST /salons/:id/clients` → `201` (la ficha completa)

```json
{ "name": "Lía Fernández", "phone": "11 5555-1234", "notes": "opcional" }
```

`phone` y `notes` son opcionales. `400` si el nombre viene vacío.

### `GET /salons/:id/clients` → `200`

```json
{ "clients": [ { "id": "...", "salon_id": "...", "name": "...",
                 "phone": "...", "notes": "...", "created_at": "..." } ] }
```

Ordenado por nombre.

## Fotos de la ficha

La foto se normaliza en el servidor: se guarda una variante **WebP con el lado
mayor en 1280 px** (una foto de 1600×1200 queda en 1280×960). `w` y `h` son las
dimensiones de la variante que se sirve, no del archivo subido.

```json
{ "id": "ph_...", "salon_id": "sal_...", "client_id": "cli_...",
  "url": "/api/v1/photos/ph_...", "w": 1280, "h": 960, "bytes": 180000,
  "content_type": "image/webp", "created_at": "2026-12-03T09:05:00Z" }
```

### `POST /salons/:id/clients/:cid/photos` → `201 {photo}`

Acepta:

1. `multipart/form-data` con el archivo en el campo **`photo`** (lo que manda un
   `<input type="file">` con `FormData.append('photo', file)`). También se acepta
   el nombre genérico **`file`**:

```bash
curl -X POST "$API/api/v1/salons/$SALON/clients/$CLI/photos" \
  -H "Authorization: Bearer $TOKEN" -F "photo=@foto.jpg"
```

2. El archivo crudo en el body con `Content-Type: image/jpeg|image/png|image/webp`.

Respuesta:

```json
{ "photo": { "id": "ph_...", "url": "/api/v1/photos/ph_...", "w": 1280,
             "h": 960, "bytes": 180000, "salon_id": "...", "client_id": "...",
             "content_type": "image/webp", "created_at": "..." } }
```

`400` si no es una imagen, si supera 8 MB, si los campos `photo`/`file` faltan o
si el archivo no se puede decodificar. `403` si el rol no administra clientes. `404`
si la ficha no existe en ese salón.

### `GET /salons/:id/clients/:cid/photos` → `200`

```json
{ "photos": [ { "id": "...", "url": "/api/v1/photos/...", "w": 1280,
                "h": 960, "bytes": 180000, "salon_id": "...",
                "client_id": "...", "content_type": "image/webp",
                "created_at": "..." } ] }
```

Más nuevas primero.

### `DELETE /salons/:id/clients/:cid/photos/:pid` → `204`

Borra la fila y el archivo. `404` si no existe en esa ficha/salón.

### `GET /photos/:pid` → la imagen (sirve bytes, no JSON)

**Sin `Authorization`**: un `<img src>` no puede mandar headers, así que el id
aleatorio de 128 bits **es** la capability (no se enumera). Devuelve
`Content-Type: image/webp` y `Cache-Control: private, max-age=86400`. `404` si
no existe. El listado, la subida y el borrado siguen scopeados por salón; esta
URL es la que viaja en el campo `url` de cada foto.

> Nota de implementación: el storage es un adapter de **filesystem local**
> (`MEDIA_DIR`, default `./media`). La interfaz `ports.PhotoStorage` es la misma
> que usaría S3/MinIO: cambiar de adapter es una línea en `cmd/api/main.go`.
