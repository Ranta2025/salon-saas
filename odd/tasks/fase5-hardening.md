# Salón SaaS — Fase 5: auditoría, hardening y faltantes

## Specs
- S1 (usuario): "primero vamos a terminar bien este completo, hazle una pasada buscando errores tanto en el backend como en el frontend en el diseno , y termina todo lo faltante, si no pudiste bajar cosas dame los comandos para bajarlo para en lo que terminas lo hago"
- S2 (usuario): "no me pidas permiso para hacer las cosas te doy autorizacion"
- S3 (usuario, antes): "rdd a todo" y "siempre buenas practicas respetando principios solid y patrones de diseno"

## Evidencia de la auditoría (ronda 1)
Dos auditores de solo lectura con pruebas reales. IDs B-xx (backend) y F-xx (frontend) abajo.
- P0 confirmado: sin CORS el login falla en el navegador (preflight 404); secreto JWT por defecto aceptó un token forjado; una venta de montos grandes rompe dashboard y cierre de caja para siempre (`SUM::int`).
- Verificado correcto (no rehacer): aislamiento entre tenants en todas las rutas; roles; `alg:none` y token vencido dan 401; govulncheck limpio; `go test -race` y los IT pasan; dinero en enteros; idempotencia de ventas; tsc, 57 tests y build del frontend pasan.

## API contract nuevo (ambos writers lo respetan; el frontend lo consume desde ya)

```
GET  /api/v1/me
  -> 200 {"user":{"id","name","email"},"salons":[{"id","slug","name","role"}]}
GET  /api/v1/salons/:id/staff           (cualquier miembro del salón)
  -> 200 {"staff":[{"id","name","role"}]}   roles owner|admin|professional
GET  /api/v1/public/salons/:slug        (SIN auth)
  -> 200 {"salon":{"id","slug","name"},"services":[{"id","name","duration_min","price_cents"}]}
  -> 404 si no existe; solo servicios active
```

Errores de validación: cuerpo `{"error":"<mensaje claro en español>"}`; nunca SQL ni mensajes de pgx. Los 500 devuelven `{"error":"error interno"}` y el detalle va al log.

Variables de entorno del backend: `DATABASE_URL`, `JWT_SECRET` (obligatoria, >= 32 caracteres cuando `GIN_MODE=release`; sin ella el proceso termina), `CORS_ORIGINS` (lista separada por comas; por defecto `http://localhost:5173,http://localhost:4173`), `MEDIA_DIR`, `PORT`, `GIN_MODE`.

Frontend: `VITE_API_URL` (build arg en Docker). La imagen de producción sirve el build con nginx en el puerto 80.

## Tasks
- [x] T1 Auditoría backend (solo lectura) — reporte B-01..B-21
- [x] T2 Auditoría frontend + diseño (solo lectura) — reporte F-01..F-24
- [x] T3 (commits 0ed8e48, 8014f69, 47c1262, 8ef2bb9; P0 verificados por el orquestador contra binario real) Backend ronda 1 (writer A): B-01 CORS por env (también Origin del websocket), B-02 JWT (secreto obligatorio, `WithValidMethods` HS256, `WithExpirationRequired`), B-03 límites de dominio en ventas (qty <= 1000, unit_price <= 10_000_000 centavos, <= 50 ítems, chequeo de overflow) + `SUM::bigint` + error 22003 -> 400, B-04 precio sale del catálogo y se rechazan servicios inactivos, B-05 emails normalizados a minúsculas + `Users.Save` por `pgError` + bcrypt falso si no existe el usuario, B-06 clave entre 8 y 72 bytes, B-07 rate limit en login y register (por IP), B-08 token del websocket fuera de los logs de Gin (redactar `token=`), B-09 cierre de caja con transacción y `pg_advisory_xact_lock`, B-10 lock de migraciones (`pg_advisory_lock`) y tabla `schema_migrations`, B-11 `http.Server` con timeouts + shutdown elegante + tope de 1 MB en JSON + `SetTrustedProxies` + pool configurado, B-12 `professional_id` debe ser miembro del salón con rol professional/admin/owner, B-14 500 genéricos, B-15 idempotencia de reservas ante 23P01, B-16 límites de largo en campos de texto, B-18 reservas: duración máxima 12 h y no en el pasado. Endpoints nuevos: `/me`, `/salons/:id/staff`, `/public/salons/:slug`. Tests nuevos para cada punto (CORS, overflow, JWT forjado, concurrencia de cierres, migrate).
- [ ] T4 Frontend (writer B): F-01 documentar y usar `VITE_API_URL` (y proxy de Vite en dev), F-02 select de profesional desde `/staff`, F-03 selección automática del salón desde `/me` (sin pegar IDs), F-04 cola offline (4xx definitivo a `dropped`, 401 pausa sin gastar intentos, logout aísla la cola), F-05 tabla de servicios sin desborde en móvil, F-06 navegación móvil completa, F-07 contraste AA (`--ink-muted` >= 4.5:1, nunca gold sobre cream como texto), F-08 galería sin huecos, F-09/F-10 imágenes con width/height y hero con `fetchpriority="high"` + preload, F-11 iconos PNG 192/512/maskable/apple-touch generados con Chromium headless desde los SVG, F-12 fecha local en el formulario, F-13 servicios y precios de la landing desde `/public/salons/:slug` con respaldo estático, F-14 Dockerfile multietapa con nginx y `ARG VITE_API_URL`, F-15..F-24 (caché del SW, timeout de fetch, WS que deja de reconectar con token inválido, eventos `booking.created`/`sale.created` refrescan panel y calendario, sin jerga de "centavos de la API", `scroll-margin-top`, foco del hero).
- [x] T5 (commit 3f5ea59) Operación (writer C): CI de GitHub Actions (go vet, `go test -race`, IT con servicio postgres, tsc, tests y build del frontend), `docker-compose.yml` completo (api, web, postgres, redis, mailpit; MinIO opcional con `MINIO_IMAGE` configurable), `.env.example`, README raíz reescrito, `docs/ops/minio.md` con los comandos de instalación.
- [ ] T6 Ronda 2 backend (ver contrato ronda 2; incluye R3-001 fallo de cwebp -> 400, R3-002 Content-Type case-insensitive, R4-003 listado de notificaciones sin caer si falla el conteo, adaptador S3 contra el MinIO real y fotos con URL firmada): reserva pública (`GET /public/salons/:slug/availability`, `POST /public/salons/:slug/bookings`), editar/cancelar/no-show de turno (emite `booking.updated`), gestión de miembros (`POST /salons/:id/members`), `salons.timezone` para la ventana del cierre, índices B-19, tests de `cmd/api`.
- [ ] T7 Ronda 2 frontend: formulario de reserva pública real, acciones sobre turnos en el calendario, pantalla de equipo.
- [ ] T9 Backend ronda 3 (E-03, E-04, E-07)
- [ ] T10 Frontend ronda 3 (E-02, E-03 UI, E-05, E-06, E-08, E-09, E-10, E-11, E-13)
- [ ] T11 Moneda USD, teléfono de contacto +53 55528595 y selector de código de país en los formularios de teléfono (frontend; sale de L-usuario abajo)
- [ ] T12 Recordatorios de turno por email (backend: puerto ReminderSender, SMTP, tarea periódica idempotente; frontend: email opcional en la reserva pública)
- [ ] T8 Verificación final, assess RDD, merge a main y push.

## Contrato ronda 2 (backend T6 y frontend T7)

Ajustes del salón (nuevas columnas en `salons`: `timezone` IANA, default `America/Mexico_City`; `open_time`/`close_time` en `HH:MM`, default `09:00`/`19:00`; `slot_step_min` default 30):
```
GET /api/v1/salons/:id/settings            (miembro) -> {"timezone","open_time","close_time","slot_step_min"}
PUT /api/v1/salons/:id/settings            (owner/admin) mismo cuerpo -> 200
```
Reserva pública (SIN auth, rate limit por IP):
```
GET  /api/v1/public/salons/:slug/availability?date=YYYY-MM-DD&service_id=<id>[&professional_id=<id>]
  -> 200 {"date","slots":[{"professional_id","starts_at","ends_at"}]}   (UTC RFC3339; solo huecos libres dentro del horario del salón, en el futuro)
POST /api/v1/public/salons/:slug/bookings
  {"service_id","professional_id"(opcional),"client_name","client_phone","starts_at","client_op_id"(opcional)}
  -> 201 {"booking":{"id","professional_id","starts_at","ends_at","status"}}   duración = la del servicio; sin professional_id elige uno libre
  -> 409 {"error":"..."} si el hueco ya no está; 400 si faltan datos
```
Turnos (miembros; owner/admin/reception sobre cualquiera, professional solo los suyos):
```
PATCH /api/v1/salons/:id/bookings/:bid
  {"status":"cancelled|no_show|done"}  y/o  {"starts_at","ends_at","professional_id"} (reprogramar; respeta anti-solape)
  -> 200 {"booking":{...}}   emite el evento websocket booking.updated
```
Equipo:
```
GET    /api/v1/salons/:id/members                   (owner/admin) -> {"members":[{"id","name","email","role"}]}
POST   /api/v1/salons/:id/members {"email","role"}   (owner/admin; la persona ya debe tener cuenta; solo owner agrega admin) -> 201
DELETE /api/v1/salons/:id/members/:uid               (owner/admin; no se puede quitar al último owner) -> 204
```
Fotos: la `url` de cada foto pasa a ser una URL firmada con vencimiento (`/api/v1/photos/:id?exp=<unix>&sig=<hmac>`, 24 h); sin firma válida el streaming responde 403. El frontend usa la `url` tal cual la recibe y vuelve a pedir el listado si vence.

Almacenamiento S3: si `S3_ENDPOINT` está definido se usa el adaptador S3 (`S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET`, `S3_USE_SSL`); si no, el adaptador de disco local. MinIO del usuario corre en `localhost:9000` (usuario `salon`, clave `salon-secret-123`, bucket `salon-media` ya creado).

## Ronda 3: hallazgos del E2E en navegador real (T9 backend, T10 frontend)

Un auditor corrió el producto en Chromium real contra el backend real con el frontend en otro origen. P0 ya corregido por el orquestador (E-01: CORS sin PATCH, commit 1058b1e). Pendiente:

Backend (T9):
- E-03: una reserva pública debe dejar una ficha de cliente (upsert por salon_id + teléfono normalizado; sin duplicar en cada reserva; si falla la ficha la reserva igual gana y se loguea); `POST /salons/:id/clients` debe aceptar y persistir `email` opcional (migración idempotente con guarda DO si hace falta columna) y devolverlo.
- E-04: los avisos de turno (booking.created público y de staff, booking.updated) deben formatear la hora en la zona IANA del salón y nombrar la zona; el payload del websocket no cambia (`starts_at` sigue en UTC RFC3339).
- E-07: mensajes de los endpoints de equipo en español limpio, sin el prefijo "validación:" y con roles en español (dueño, administración, profesional, recepción, cliente); mismos códigos HTTP.

Frontend (T10):
- E-02: el rol `professional` en `/panel` no debe llamar a `/dashboard`: redirigir al calendario; un 403 se muestra amable y sin botón Reintentar.
- E-03 (UI): formulario "Nueva clienta" (nombre, teléfono, email, notas) solo para owner/admin/reception; email visible en la ficha; texto de la pantalla verdadero.
- E-05: una operación offline descartada por rechazo del servidor (400/403/404/409/422) muestra el motivo real del servidor, no "se agotaron los intentos".
- E-06: el POS offline no muestra "No pudimos cargar el equipo" si el select tiene datos; el service worker responde las GET de API que no puede servir con JSON y estado propio, sin ruido en consola.
- E-08: el detalle del turno como panel lateral visible (hoja completa en móvil) con foco dentro; no ofrecer Reprogramar en no_show/cancelled/done; horas de la grilla según `open_time`/`close_time` de `/settings` (respaldo 09:00-20:00); plural "1 cita" / "2 citas".
- E-09: el horario de atención de la landing no puede mentir: una sola constante marcada como orientativa (la API pública no expone horario; no inventar campos).
- E-10: con la API caída, `/panel/calendario` nunca muestra "No hay citas" como si fuera dato real: estado "Sin conexión: mostrando datos guardados" o de error.
- E-11: el preload del hero solo en la landing (sin la advertencia "preload not used" en el panel).
- E-13: menú del panel en 390px compacto (barra horizontal o inferior), objetivos de 44px, sin entradas recortadas.

## Allowed edit surfaces
Writer A:
backend/**
docs/api/**

Writer B:
frontend/**
docs/design/**

Writer C:
.github/**
docker-compose.yml
.env.example
README.md
Makefile
docs/ops/**

## Log
- L1 (usuario): ver S1.
- L2 (usuario): "conitinua no me pidas permiso para hacer las cosas te doy autorizacion" (S2).
- L3 (orquestador): auditorías cerradas; bases de prueba salon_audit, salon_audit2, salon_audit_fe y salon_fresh eliminadas.

### L-usuario (ronda 3, pedido adicional, verbatim)
> pon los precios en dolares, numero de telefono mi numero es +53 55528595, y que cuando vaya a introducir el numero que salga algo que lo ayude con el codigo de region, una pregunta cuando el cliente saca cita no tiene que tener membresia y cuando tiene un turno se le manda un gmail o mensaje recordando o como es ?

Decisiones tomadas al anotarlo:
- Moneda: hoy es una constante `CURRENCY='MXN'` / `LOCALE='es-MX'` en `frontend/src/lib/money.ts`. Pasa a `USD`; el formato `$1,234.55` no cambia. Las cifras se guardan como centavos enteros, así que no hay migración de datos (solo cambia la etiqueta). Moneda por salón queda fuera de alcance.
- Teléfono de contacto de la landing: `+53 55528595` (una sola constante, usada por header, footer y CTA).
- Campo de teléfono: prefijo de país seleccionable con +53 por defecto; se envía al backend en formato internacional; el backend ya normaliza (`phone_norm`).
- Cliente sin membresía: la reserva pública es anónima (nombre + teléfono); el rol `client` existe pero no lo usa ese flujo.
- Recordatorios: hoy NO existe envío de email ni mensajes a clientes (Mailpit es solo un buzón SMTP de desarrollo; los avisos actuales son internos para el staff). Se hace email por SMTP detrás de un puerto `ReminderSender` para poder enchufar WhatsApp después.
