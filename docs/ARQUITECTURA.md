# Arquitectura

El código se ordena por **funcionalidad**, no por capa. Cada funcionalidad es un
paquete de Go con sus reglas, sus casos de uso y sus adaptadores; la dirección de
las dependencias sigue siendo la de siempre (hexagonal): los adaptadores importan
al núcleo y el núcleo no conoce HTTP, SQL ni frameworks.

## Para qué

Antes el código estaba por capa (`domain/`, `ports/`, `app/`, `adapters/`): para
tocar una sola funcionalidad había que abrir cuatro carpetas de 80 a 140
archivos. Hoy una funcionalidad se lee, se prueba y se mueve en una carpeta.

## Mapa

| Funcionalidad | Núcleo y adaptadores |
|---|---|
| agenda, disponibilidad, reserva pública, enlaces y recordatorios | `internal/bookings/{,postgres,httpapi}` |
| señas online y banco simulado | `internal/deposits/{,postgres,httpapi}` + `internal/simbank` |
| autorización, 2FA, recuperación de contraseña, sesión | `internal/auth/{,postgres,httpapi}` + `internal/auth/security` |
| equipo, invitaciones, staff y tenants | `internal/team/{,postgres,httpapi}` |
| servicios, profesionales y equipamiento | `internal/staffing/{,postgres,httpapi}` |
| horarios, ausencias y cobertura | `internal/schedules/{,postgres,httpapi}` |
| clientes, ficha, historial y fotos | `internal/clients/{,postgres,httpapi}` |
| POS, caja y panel del día | `internal/pos/{,postgres,httpapi}` |
| productos e inventario | `internal/products/{,postgres,httpapi}` |
| paquetes y bonos prepagos | `internal/packages/{,postgres,httpapi}` |
| fidelidad | `internal/loyalty/{,postgres,httpapi}` |
| nómina y comisiones | `internal/payroll/{,postgres,httpapi}` |
| gastos y ganancia neta | `internal/finance/{,postgres,httpapi}` |
| exportes CSV | `internal/exports/{,postgres,httpapi}` |
| lista de espera | `internal/waitlist/{,postgres,httpapi}` |
| reseñas posvisita | `internal/reviews/{,postgres,httpapi}` |
| auditoría | `internal/audit/{,postgres,httpapi}` |
| notificaciones | `internal/notifications/{,postgres,httpapi}` |
| ajustes, perfil público y galería | `internal/settings/{,postgres,httpapi}` |

Lo transversal:

| Carpeta | Qué es |
|---|---|
| `internal/domain` | reglas puras (dinero en centavos, fechas, email/teléfono, turnos). No importa nada del proyecto |
| `internal/ports` | las interfaces (puertos), en un archivo por funcionalidad |
| `internal/kernel` | lo que comparten todos los casos de uso: permisos, ajustes del salón, avisos de mejor esfuerzo y eventos |
| `internal/adapters/http` | middleware compartido (auth, CORS, rate limit, tope de body, log) y el registro de rutas |
| `internal/adapters/postgres` | infraestructura compartida de pgx: pool, migraciones, timeout de transacción e identidad (users/salons/memberships) |
| `internal/adapters/{realtime,storage,imaging,mail}` | adaptadores compartidos (websocket, adjuntos, WebP, SMTP) |
| `internal/testutil` (+ `itdb`, `httpkit`, `agendakit`) | fakes y helpers compartidos de test |

## Regla de dependencias

- `domain` no importa nada del proyecto.
- `ports` son interfaces; `kernel` es el comportamiento compartido de los casos
  de uso. Los dos son el núcleo.
- `internal/<funcionalidad>/postgres` y `.../httpapi` importan al núcleo; el
  núcleo NUNCA importa un adaptador.
- Dos funcionalidades se hablan por una interfaz (en `ports` o del lado de quien
  la consume), no importando el `postgres`/`httpapi` de la otra. Así se resolvió
  el ciclo real reservas <-> señas: `bookings` define `DepositCreator` y `ports`
  publica `BookingCanceller`, `BookingNoticeRepository` y `BookingLinkRepository`.
- `cmd/api` es el único lugar que conoce adaptadores concretos.

## Composition root

`backend/cmd/api` es la fábrica:

- `main.go`: `main()`, timeouts y el orden de alto nivel (núcleo, funcionalidad
  por funcionalidad, servidor).
- `wire.go`: el struct `wiring` (lo que se construye una sola vez).
- `wire_<funcionalidad>.go`: repositorios, casos de uso y trabajos de fondo de
  esa funcionalidad. Las inyecciones posteriores (mailer, colas de email, banco)
  están en el mismo archivo que las construye.
- `wire_deps.go`: arma `httpapi.Deps` (un handler por funcionalidad).

## Tests

- Unitarios: al lado del paquete que prueban (`internal/<feature>/*_test.go`).
- Integración (Postgres real): `*_it_test.go` en el `postgres/` de cada
  funcionalidad y los históricos de `internal/adapters/postgres/`; se habilitan
  con `SALON_IT=1` y `DATABASE_URL`.
- Comandos: `make test` (unitarios con `-race`), `make test-it` (integración) y,
  para un paquete puntual, `cd backend && go test -count=1 ./internal/<feature>/...`.

## Cómo agregar una funcionalidad

1. Migración SQL idempotente en `backend/migrations/` (guarda en `pg_catalog`).
2. Interfaces en `internal/ports/ports_<funcionalidad>.go`.
3. Caso de uso en `internal/<funcionalidad>/` (con sus tests, fakes compartidos
   de `internal/testutil`).
4. Adaptador de persistencia en `internal/<funcionalidad>/postgres/`.
5. Handler HTTP en `internal/<funcionalidad>/httpapi/`.
6. Rutas en `internal/adapters/http/routes_<funcionalidad>.go` y su llamada en
   `routes.go` (el struct `Deps` suma el handler).
7. Cableado en `cmd/api/wire_<funcionalidad>.go` y su lugar en `buildRouter`.
8. Tests de integración (`-it_test.go`) y, si hay UI, la carpeta en
   `frontend/src/features/<funcionalidad>/`.
9. Documentación: este mapa, `README.md`, `docs/DECISIONES.md` y el contrato en
   `docs/api/` cuando la haya.

## Frontend

`frontend/src` sigue el mismo criterio por funcionalidad:

- `src/features/<nombre>/{pages,components,lib,index.ts}`: página, componentes,
  lógica pura y sus tests, con el `index.ts` como única puerta de entrada.
- `src/lib/`: módulos transversales que todavía viven ahí (api, sesión, dinero,
  fechas, realtime, cola offline, permisos).
- `src/app/`: shell, contextos (notificaciones, tiempo real, offline).
- `src/styles/`: tokens y CSS por superficie.

Tests: `cd frontend && npx tsc --noEmit && npm test` (`node --test 'src/**/*.test.ts'`).
