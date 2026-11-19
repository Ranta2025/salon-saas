# Salón SaaS — Plataforma multi-tenant de gestión para salones y peluquerías

## Visión
SaaS web para que cada salón tenga su espacio propio: reservas online, calendario,
clientes, catálogo de servicios, POS y cierre de caja, comisiones por barbero,
notificaciones en tiempo real y página pública para reservar sin cuenta.
PWA offline-first para recepción/caja.

Evolución web de `gestion-salon` (Flutter + sqflite offline) + realtime de `chatapp`
(Gin + React). Pensado para portfolio senior: multi-tenancy real, concurrencia en
reservas, sync offline, archivos, push, Docker y tests.

## Stack
- Backend: Go + Gin + Postgres + Redis (cola + realtime vía websocket/SSE)
- Frontend: React + TypeScript + Vite + PWA (offline para recepción)
- Archivos: S3 compatible (MinIO en local)
- Infra local: Docker Compose (api, web, postgres, redis, minio, mailpit)
- Tests: `go test -race`, vitest, Playwright e2e

## Arquitectura
```
salon-saas/
  backend/          # API Gin, un módulo por dominio en internal/
    cmd/api/        # main.go, bootstrap
    internal/
      auth/         # login, JWT, refresh, roles
      tenants/      # salones, miembros, roles por salón
      services/     # servicios, combos, comisiones
      bookings/     # reservas, calendario, anti-solape
      clients/      # clientes, historial, fotos
      pos/          # ventas, caja, ingresos/gastos
      notifications/# websocket hub, push, email (mailpit local)
      storage/      # subida a MinIO/S3
  frontend/         # Vite React TS PWA
    src/pages/      # login, dashboard, calendar, pos, clients, public-booking
    src/components/ # UI compartida
    src/lib/        # api client, auth, realtime, offline queue
  docs/             # decisiones, API, roadmap
```

Multi-tenancy: todo dato cuelga de `salon_id`. Roles: `owner/admin/barbero/recepcion/cliente`.
Regla dura: ninguna query sin `salon_id`, ningún archivo sin `salon_id`.

## Módulos (alcance grande)
1. Auth + organizaciones + auditoría
2. Salones: perfil, horarios, sillones/recursos, página pública `/b/:slug`
3. Servicios + combos + duración + precio + comisión
4. Reservas: calendario por barbero/recurso, anti-solape transaccional, recordatorios,
   cancelación, no-show
5. Clientes: fichas, historial, fotos, preferencias, búsqueda
6. POS: venta de servicios/productos, cierre de caja, ingresos/gastos
7. Dashboard: ocupación, ingresos, ranking barberos, exportación CSV/Excel/PDF
8. Notificaciones: hub realtime + web-push + email + centro de notificaciones
9. Adjuntos: fotos de trabajos, comprobantes (MinIO, variantes webp)
10. Offline: cola de ventas/reservas en PWA que sincroniza con idempotencia

## Reglas de negocio clave
- Una reserva no puede solaparse con otra del mismo barbero/recurso (constraint +
  transacción; 409 ante carrera).
- Cancelación hasta X horas antes (config por salón); después marca no-show.
- Cierre de caja cuadra: `ventas - gastos = neto`; todo movimiento auditable.
- Comisión del barbero = % del servicio a precio cobrado, no a precio lista.
- Reserva pública crea cliente automáticamente si el teléfono no existe.

## API (v1, prefijo `/api/v1`)
- `POST /auth/login|refresh|logout` — JWT corto + refresh rotado
- `CRUD /salons/:id/...` — services, barbers, clients, bookings, sales, expenses
- `GET /b/:slug/availability?date=` — huecos públicos
- `POST /b/:slug/bookings` — reserva pública (rate-limit)
- `WS /ws?token=` — eventos: booking.created/updated, sale.created, notify

## Estado actual
Esqueleto inicial: carpetas, `docker-compose.yml`, `Makefile`, backend `GET /health`,
frontend Vite mínimo. Siguiente: auth + tenants + migraciones.

## Cómo correr (local)
```bash
docker compose up -d --build
go run ./backend/cmd/api      # :8080, GET /health
cd frontend && npm i && npm run dev  # :5173
```

## Roadmap
- Fase 1: auth + tenants + migraciones + CRUD servicios/clientes
- Fase 2: reservas con anti-solape + calendario + página pública
- Fase 3: POS + caja + dashboard + reportes
- Fase 4: realtime + push + adjuntos + PWA offline
- Fase 5: hardening (rate-limit, auditoría, backups) + demo seed + deploy

## Qué demuestra (para entrevistas)
Multi-tenancy, transacciones y carreras en reservas, RBAC, sync offline idempotente,
subida de archivos, realtime, PWA, Docker, tests de backend + e2e.
