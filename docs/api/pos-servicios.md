# API — catálogo de servicios + POS (Fase 3, backend)

Base: `/api/v1`. Todos los endpoints de este documento requieren
`Authorization: Bearer <jwt>`; el token guarda el `userID` y el salón se resuelve
por **membership** (`users × salons × role`). Sin membership en el salón del
`:id` la respuesta es `403`.

Reglas transversales:

- **Dinero en centavos enteros** (`price_cents`, `total_cents`, `*_cash_cents`,
  `counted_cents`, `difference_cents`). Nunca floats, nunca strings con moneda.
- **Todo scopeado por `salon_id`**: un recurso de otro salón responde `404`
  (no se filtra ni siquiera su existencia).
- **Totales calculados en el servidor**: si el cliente manda `total` o
  `total_cents` en el body, se ignora en silencio.
- Fechas/horas: `RFC3339` en UTC (`2026-03-04T10:00:00Z`).
- Errores: `{"error": "<mensaje>"}` con status `400` validación / `401` sin
  token / `403` sin permiso / `404` no existe / `409` duplicado (`5xx` solo si
  falla la infraestructura). Nunca se devuelve SQL ni detalles de Postgres.

Permisos por rol (RBAC del salón):

| Acción | owner | admin | reception | professional | client |
|---|---|---|---|---|---|
| `GET /services` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST/PUT /services` | ✅ | ✅ | ❌ | ❌ | ❌ |
| `GET /sales` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST /sales` | ✅ | ✅ | ✅ | ❌ | ❌ |
| `GET /cash-closes` | ✅ | ✅ | ✅ | ✅ | ✅ |
| `POST /cash-closes` | ✅ | ✅ | ❌ | ❌ | ❌ |
| `GET /dashboard` | ✅ | ✅ | ✅ | ✅ | ✅ |

## Servicios (catálogo)

Entidad:

```json
{ "id": "svc_...", "salon_id": "sal_...", "name": "Corte de dama",
  "duration_min": 45, "price_cents": 150000, "commission_pct": 20, "active": true }
```

### `GET /salons/:id/services?include_inactive=true`

Sin el query param solo devuelve activos. Orden: por nombre.

```json
{ "services": [ { ...servicio... } ] }
```

### `POST /salons/:id/services` → `201`

Body (todos los campos obligatorios):

```json
{ "name": "Corte de dama", "duration_min": 45, "price_cents": 150000, "commission_pct": 20 }
```

Responde **la entidad plana** (mismos campos que el listado, no envuelta en
`{"service": ...}`) para que el frontend guarde el `id` que acaba de crear.

Reglas: `name` no vacío y único entre los **activos** del salón
(case-insensitive: `"Corte"` ≡ `"corte"` → `409`); `duration_min > 0`;
`price_cents >= 0`; `commission_pct` entre `0` y `100`.

### `PUT /salons/:id/services/:service_id` → `200`

Es un **replace** de los datos del servicio (mismos campos que el POST, más
`active` opcional; si se omite, `active` queda `true`):

```json
{ "name": "Corte y brushing", "duration_min": 60, "price_cents": 180000,
  "commission_pct": 35, "active": false }
```

Responde la entidad plana actualizada. Desactivar **libera el nombre** para un
servicio nuevo. `:service_id` inexistente o de otro salón → `404`.

## Ventas (POS)

Entidad:

```json
{ "id": "sale_...", "salon_id": "sal_...", "professional_id": "usr_...",
  "client_name": "Lía", "items": [ { "service_id": "svc_...", "qty": 2, "unit_price_cents": 150000 } ],
  "total_cents": 300000, "method": "cash", "created_at": "2026-03-04T14:05:00Z" }
```

`method` es un enum cerrado: `cash` (efectivo), `card` (tarjeta), `transfer`
(transferencia). Otro valor → `400`.

### `POST /salons/:id/sales` → `201`

```json
{ "professional_id": "usr_...", "client_name": "Lía", "method": "cash",
  "items": [ { "service_id": "svc_...", "qty": 2, "unit_price_cents": 150000 } ] }
```

Respuesta:

```json
{ "sale_id": "sale_...", "total": 300000 }
```

`total` es **centavos** y sale de `Σ qty × unit_price_cents` en el servidor.
`total` / `total_cents` en el request se ignoran por diseño.

Validaciones (`400`): al menos un ítem, `qty >= 1`, `unit_price_cents >= 0`,
`client_name` y `professional_id` no vacíos, `service_id` existente **en este
salón** (vender un servicio de otro tenant no pasa) y `method` del enum.

### `GET /salons/:id/sales?from=&to=` → `200`

`from`/`to` opcionales (RFC3339, ventana `[from, to)`); sin parámetros devuelve
**el día UTC de hoy**. Orden: más reciente primero.

```json
{ "sales": [ { ...venta... } ] }
```

## Cierre de caja

Entidad:

```json
{ "id": "close_...", "salon_id": "sal_...",
  "opened_at": "2026-03-04T00:00:00Z", "closed_at": "2026-03-04T20:00:00Z",
  "system_cash_cents": 250000, "counted_cents": 248000,
  "difference_cents": -2000, "note": "faltó vuelto" }
```

### `POST /salons/:id/cash-closes` → `201`

```json
{ "counted_cents": 248000, "note": "faltó vuelto" }
```

```json
{ "close_id": "close_...", "expected_cash": 250000 }
```

`expected_cash` (= `system_cash_cents`) es el **efectivo esperado**: suma de
ventas con `method = cash` desde la apertura del turno hasta ahora. La apertura
es el `closed_at` del cierre anterior; si es el primer cierre del salón, las
`00:00 UTC` de hoy. Tarjeta y transferencia no entran al cajón.

La diferencia contable (`difference_cents = counted_cents - system_cash_cents`,
negativa = faltante) no viene en el POST; se calcula en el servidor y se ve en el
`GET`.

`counted_cents < 0` → `400`. Solo owner/admin cierran (`reception` → `403`).

### `GET /salons/:id/cash-closes` → `200`

```json
{ "closes": [ { ...cierre... } ] }
```

Más reciente primero.

## Dashboard

### `GET /salons/:id/dashboard` → `200`

```json
{ "revenue_today": 200000,
  "appointments_today": 2,
  "occupancy_rate": 0.1667,
  "top_services": [ { "service_id": "svc_...", "name": "Corte", "qty": 3, "revenue_cents": 450000 } ] }
```

- `revenue_today`: suma de `total_cents` de las ventas del día UTC de hoy (todos
  los métodos), en centavos.
- `appointments_today`: turnos de hoy con estado `booked` o `done` (canceladas y
  no-shows no cuentan).
- `occupancy_rate`: minutos reservados hoy ÷ (profesionales del salón × 720
  minutos de jornada de referencia), redondeado a 4 decimales y con tope `1.0`.
  Sin profesionales, `0`.
- `top_services`: los 5 servicios más vendidos hoy por unidades; desempata el
  ingreso. Siempre array (nunca `null`).

## Notas para el frontend

- No hay `DELETE` de servicios: se desactivan con `PUT ... {"active": false}`.
- El catálogo y el POS nunca exponen `float`: si la UI quiere mostrar plata,
  dividir por 100 recién al formatear.
- Los endpoints públicos del salón (landing) no viven acá: todo este documento
  pide JWT.
