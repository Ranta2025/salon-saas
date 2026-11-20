# API — Fase 5: hardening, CORS y endpoints nuevos

Base: `/api/v1`. Este documento cubre lo que cambió en la fase 5. El contrato de
catálogo/POS sigue en [`pos-servicios.md`](./pos-servicios.md) y el de avisos en
[`notificaciones-clientes.md`](./notificaciones-clientes.md).

## Variables de entorno del backend

| Variable | Obligatoria | Default | Qué hace |
|---|---|---|---|
| `DATABASE_URL` | no | `postgres://salon:salon@localhost:5432/salon?sslmode=disable` | Conexión a Postgres. |
| `JWT_SECRET` | **sí** | — | Firma y valida los tokens. **Sin ella el proceso no arranca.** Debe tener **≥ 32 caracteres**; solo `GIN_MODE=debug`/`test` admiten un secreto corto. |
| `CORS_ORIGINS` | no | `http://localhost:5173,http://localhost:4173` | Allowlist de orígenes del navegador, separada por comas. Se usa para CORS **y** para el `Origin` del websocket. |
| `MEDIA_DIR` | no | `./media` | Directorio de las fotos adjuntas (filesystem local; la interfaz del puerto es la de S3). |
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
  Access-Control-Allow-Methods: GET, POST, PUT, DELETE, OPTIONS
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
  contabiliza **una sola vez**. `counted_cents` y `system_cash_cents` tienen tope
  de 2.000.000.000 centavos.
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
