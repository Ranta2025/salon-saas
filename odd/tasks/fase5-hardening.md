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
- [ ] T3 Backend ronda 1 (writer A): B-01 CORS por env (también Origin del websocket), B-02 JWT (secreto obligatorio, `WithValidMethods` HS256, `WithExpirationRequired`), B-03 límites de dominio en ventas (qty <= 1000, unit_price <= 10_000_000 centavos, <= 50 ítems, chequeo de overflow) + `SUM::bigint` + error 22003 -> 400, B-04 precio sale del catálogo y se rechazan servicios inactivos, B-05 emails normalizados a minúsculas + `Users.Save` por `pgError` + bcrypt falso si no existe el usuario, B-06 clave entre 8 y 72 bytes, B-07 rate limit en login y register (por IP), B-08 token del websocket fuera de los logs de Gin (redactar `token=`), B-09 cierre de caja con transacción y `pg_advisory_xact_lock`, B-10 lock de migraciones (`pg_advisory_lock`) y tabla `schema_migrations`, B-11 `http.Server` con timeouts + shutdown elegante + tope de 1 MB en JSON + `SetTrustedProxies` + pool configurado, B-12 `professional_id` debe ser miembro del salón con rol professional/admin/owner, B-14 500 genéricos, B-15 idempotencia de reservas ante 23P01, B-16 límites de largo en campos de texto, B-18 reservas: duración máxima 12 h y no en el pasado. Endpoints nuevos: `/me`, `/salons/:id/staff`, `/public/salons/:slug`. Tests nuevos para cada punto (CORS, overflow, JWT forjado, concurrencia de cierres, migrate).
- [ ] T4 Frontend (writer B): F-01 documentar y usar `VITE_API_URL` (y proxy de Vite en dev), F-02 select de profesional desde `/staff`, F-03 selección automática del salón desde `/me` (sin pegar IDs), F-04 cola offline (4xx definitivo a `dropped`, 401 pausa sin gastar intentos, logout aísla la cola), F-05 tabla de servicios sin desborde en móvil, F-06 navegación móvil completa, F-07 contraste AA (`--ink-muted` >= 4.5:1, nunca gold sobre cream como texto), F-08 galería sin huecos, F-09/F-10 imágenes con width/height y hero con `fetchpriority="high"` + preload, F-11 iconos PNG 192/512/maskable/apple-touch generados con Chromium headless desde los SVG, F-12 fecha local en el formulario, F-13 servicios y precios de la landing desde `/public/salons/:slug` con respaldo estático, F-14 Dockerfile multietapa con nginx y `ARG VITE_API_URL`, F-15..F-24 (caché del SW, timeout de fetch, WS que deja de reconectar con token inválido, eventos `booking.created`/`sale.created` refrescan panel y calendario, sin jerga de "centavos de la API", `scroll-margin-top`, foco del hero).
- [ ] T5 Operación (writer C): CI de GitHub Actions (go vet, `go test -race`, IT con servicio postgres, tsc, tests y build del frontend), `docker-compose.yml` completo (api, web, postgres, redis, mailpit; MinIO opcional con `MINIO_IMAGE` configurable), `.env.example`, README raíz reescrito, `docs/ops/minio.md` con los comandos de instalación.
- [ ] T6 Ronda 2 backend: reserva pública (`GET /public/salons/:slug/availability`, `POST /public/salons/:slug/bookings`), editar/cancelar/no-show de turno (emite `booking.updated`), gestión de miembros (`POST /salons/:id/members`), `salons.timezone` para la ventana del cierre, índices B-19, tests de `cmd/api`.
- [ ] T7 Ronda 2 frontend: formulario de reserva pública real, acciones sobre turnos en el calendario, pantalla de equipo.
- [ ] T8 Verificación final, assess RDD, merge a main y push.

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
