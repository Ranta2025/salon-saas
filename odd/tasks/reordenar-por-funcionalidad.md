# Reordenar el codigo por funcionalidad

Decision del usuario: reordenar DESPUES de cerrar F11 y ANTES de F13. Frontend primero, backend despues, una funcionalidad por commit, solo mover (sin cambiar comportamiento), `-race` completa tras cada tanda. Este documento es el resultado de la exploracion de solo lectura hecha mientras corria el worker de F11 (conteos de lineas estimados: el explorador no tuvo shell; el inventario de archivos y los acoplamientos si salen de listados y busquedas completas).

## Specs

- **S1** El problema: ordenamos por CAPA (domain, ports, app, adapters) pero no por FUNCIONALIDAD. Para tocar «fidelidad» hay que abrir cuatro carpetas de 80 a 140 archivos.
- **S2** Medido: backend `app/` 142 archivos (55 codigo + 87 tests), `adapters/http/` 102, `adapters/postgres/` 90, `domain/` 82, `ports/ports.go` un solo archivo de 1400+ lineas; frontend `lib/` 102 archivos planos, `app/components/` 62, `app/` 41. Unos 450 archivos Go y ~50k lineas en el backend.
- **S3** Objetivo frontend (estilo «una carpeta por componente» de Gentleman Programming): `src/features/<nombre>/{components,lib,pages,index.ts}` con la logica y sus tests dentro, mas `src/shared/` para lo transversal.
- **S4** Objetivo backend: en Go cada carpeta es un paquete y no hay subcarpetas dentro de uno, asi que el equivalente es un MODULO POR FUNCIONALIDAD: `internal/<feature>/` (dominio + puertos + casos de uso) con `internal/<feature>/postgres/` y `internal/<feature>/httpapi/`. Regla hexagonal intacta: los adaptadores importan al nucleo, nunca al reves. Un NUCLEO COMPARTIDO para lo transversal. El cableado sigue en `cmd/api`.
- **S5** Reglas del movimiento: solo mover y renombrar, cero cambios de comportamiento; una funcionalidad por commit; `git mv` para que se vea como renombre; compila + todos los tests despues de cada commit; `-race` completa tras cada tanda; la revision nativa va por tandas.

## Hallazgos (para decidir el orden)

### Backend: funcionalidades y tamano (D=domain, A=app, H=http, P=postgres; archivos codigo+tests, sin F11)

| funcionalidad | D | A | H | P |
|---|---|---|---|---|
| auth / 2FA / recuperacion / sesion (+ `adapters/security` 6+4) | 3+3 | 5+6 | 4+6 | 2+4 |
| reservas + disponibilidad + reserva publica + enlaces + recordatorios | 2+3 | 9+24 | 3+7 | 4+6 |
| lista de espera | 1+2 | 2+4 | 1+2 | 1+2 |
| fidelidad | 1+1 | 1+2 | 1+2 | 1+3 |
| paquetes | 1+1 | 1+2 | 1+3 | 1+3 |
| productos / inventario | 2+2 | 2+2 | 1+1 | 2+2 |
| POS / ventas / cierres de caja / dashboard | 2+2 | 3+4 | 1+2 | 2+1 |
| nomina / comisiones / corridas | 3+4 | 3+3 | 2+5 | 2+4 |
| gastos / ganancia | 2+2 | 2+2 | 2+2 | 2+2 |
| exportes | 1+1 | 1+2 | 1+1 | 1+1 |
| clientes / historial / fotos | 2+2 | 2+2 | 2+1 | 3+3 |
| equipo (miembros / invitaciones / staff / tenants) | 4+5 | 7+7 | 3+6 | 3+4 |
| horarios / ausencias / recursos / servicios | 6+7 | 9+9 | 5+4 | 5+5 |
| ajustes / perfil / galeria | 3+2 | 4+3 | 3+2 | 2+2 |
| auditoria | 1+1 | 1+1 | 1+1 | 1+1 |
| notificaciones / tiempo real | 1+1 | 2+1 | 1+1 | 1+1 |

### Backend: acoplamiento que condiciona el diseno

- **Nucleo compartido** (lo usan casi todas): `domain` + `ports` (Membership en 30 handlers, ServiceRepository en 13 archivos de app, SalonRepository/Settings en ~15 servicios, BookingRepository en 6, ClientRepository en 6, EventPublisher en 10 archivos, MailSender en 9, SessionGate/TokenIssuer compartidos por auth, 2FA, hub de tiempo real y middleware) + `Notifier`/`publish` de `app/events.go`.
- **Ciclo real a resolver:** reservas <-> senas. `public_bookings.go:74,79` usa `DepositCreator` (lo implementa F11) y `deposits.go:73,75,80` usa `BookingCanceller`, `BookingNoticeRepository` y `BookingLinkRepository`. En un solo paquete `app` no se nota; con modulos separados NO compila salvo que esas interfaces vivan en el nucleo. Por eso F11 y reservas se migran juntas y al final.
- **Acoplamientos de un solo sentido (migrables):** lista de espera -> disponibilidad (`waitlist.go:80,149,292`), enlaces de reserva -> disponibilidad, 2FA -> auditoria, recordatorios -> enlaces.
- **Cableado de un solo punto:** `cmd/api/main.go` 665 lineas, ~140 constructores (44 servicios + 39 repos + ~55 entradas de `Deps`); `routes.go` 447 lineas con 130 rutas. Hay que pasar a constructores por funcionalidad `NewX(...)`, y esto va AL FINAL para no tocarlo en cada commit. Hay inyeccion posterior a la construccion en `main.go:460-477` (`waitlistSvc.Mailer`, `coverageSvc.Emails`).
- **`ports.go` (1424+ lineas) por secciones:** repos 14-390, seguridad 389-405, reservas 405-424, servicios 424-434, POS/caja 434-475, exportes 475-497, dashboard 497-511, tiempo real 511-530, notificaciones 530-588, clientes 588-640, nomina 640-743, gastos/ganancia 743-784, historial 784-820, productos 820-855, lista de espera 855-936, paquetes 936-990, salon publico 990-1029, fidelidad 1029-1071, auditoria 1071-1089, 2FA 1089-1158, recuperacion 1158-1222, senas 1222-1424.
- **Tests compartidos que fuerzan un `testutil`:** `app/email_fakes_test.go` (`fakeMailer` en 18+ archivos), `app/round2_fakes_test.go` (`fakeSettings`/`fakeMembers`, 15+ archivos), `app/app_test.go` (fakes de usuarios/salones/membresias), `http/pos_test.go:179 newPOSEnv` (55 llamadas en 30+ archivos), `http/http_test.go:136 testRouter()`. Hay 15 archivos `*_fakes_test.go` con el MISMO nombre en `app` y en `http`, con espacios de nombres distintos (`fake*` vs `m*`): al unificar hay que renombrar. `Notifier`/`publish` y `DepositCreator` estan sin exportar y hay que exportarlos.
- Los tests de `postgres` importan `app` (`two_factor_concurrency_it_test.go:19`, `sale_products_it_test.go:13`, `commission_snapshot_it_test.go:22`): aceptable solo si no cruzan funcionalidades.

### Frontend: clasificacion

- **No hay alias de rutas** (ni `paths` en `tsconfig.json` ni `resolve.alias` en `vite.config.ts`): todo movimiento obliga a reescribir imports relativos. Se recomienda agregar el alias `@/` ANTES de mover (un commit solo para eso).
- **TRAMPA DE LOS TESTS:** `package.json` define `"test": "node --test src/**/*.test.ts"`; `sh` expande `**` como `*`, asi que hoy solo se recogen `src/*/*.test.ts` (o sea `src/lib/`). Si un test se mueve a una carpeta mas profunda, DEJA DE CORRER EN SILENCIO y la suite sigue verde. Arreglo previo obligatorio: entrecomillar el glob (`'src/**/*.test.ts'`, Node 22) y comprobar que la cantidad de tests no baja (hoy 882).
- Grupos: nucleo (api, session, permissions, types, datetime, money, email, phone, connectivity, offlineQueue, offlineData, pwa, realtime, notifications, metrics + contextos y shell) -> `shared/`; auth/recuperacion/2FA; reservas/calendario; clientes; POS/productos/servicios; equipo/nomina; gastos/ganancia/exportes/dashboard; paquetes/fidelidad; lista de espera; horarios/ajustes/galeria; auditoria/actividad; landing publica; senas (F11, en curso).
- Imports cruzados principales: `permissions` lo usan 35 archivos de UI; `expenses` <- `products`, `packages`, `profit`; `payroll` <- `expenses`, `profit`, `payrollRuns`; `publicBooking` <- `waitlist`, `bookingLinks` y paginas de reserva/banco; `members` <- `teamStatus`, `tradeServices`, `attends`; `weeklyHours` <- `members`, `settings`, `publicSalon`, `coverAbsence`; `packages` <- `deposits`, `simbank`; `audit` <- `DepositList.tsx:6`; ciclos de tipos `members<->tradeServices` y `members<->attends`.

### Cosas mal puestas, duplicadas o muertas (limpiar al pasar)

1. `src/pages/` esta vacia.
2. `lib/services.test.ts` importa `./api.ts` y no existe `services.ts`: es un duplicado mal ubicado de `api.test.ts`.
3. `lib/contrast.ts` solo lo usa su test y un comentario de CSS.
4. `http/pos.go` junta tres funcionalidades (SaleHandler :15, CashCloseHandler :175, DashboardHandler :247).
5. `http/handlers.go` mezcla Auth (:17) y Tenants (:74).
6. `domain/email_test.go` prueba `ValidateEmail`, que vive en `domain/invitation.go:221` (no hay `domain/email.go`).
7. `app/public.go` y `app/public_bookings.go`: lo «publico» partido entre dos funcionalidades.
8. `http/round2_test.go` y `settings_frozen_test.go`: tests transversales con nombres sin significado.
9. `src/components/PhoneInput.tsx` es el unico archivo de un `components/` de primer nivel (candidato a `shared/`).
10. `lib/metrics.ts` (solo `DashboardPage`) y `lib/teamStatus.ts` son locales a una funcionalidad.

### Lo que NO se debe romper

- `Makefile` `test-it` y la CI (`.github/workflows/ci.yml`) tienen escrito `./internal/adapters/postgres/` y el glob de `migrations/*.sql`; `backend/Dockerfile` hace `go build ./cmd/api` y copia `migrations`; `migrate.go:115-116` busca `migrations`, `/migrations` o `backend/migrations` desde el directorio actual. Mantener `cmd/api` y `backend/migrations` donde estan, y actualizar `test-it` y la CI al mover los adaptadores de postgres.
- Docs con rutas escritas: `docs/design/fase3-frontend.md:141-152`, `fase4-frontend.md:239-253`, `fase5-frontend.md` (muchas `src/lib/...`), `docs/ops/minio.md:125`, `fase5-frontend.md:1175`, `odd/tasks/fase4.md:14-15` (describe el layout por capas) y `fase3.md:105`. `docs/api/fase5-backend.md` no tiene rutas `internal/`.

## Tasks (orden propuesto)

- **T0** Prerrequisitos del frontend, un commit cada uno: glob de tests entrecomillado y sin perder ni un test; alias `@/`; limpiar `src/pages/`, `lib/services.test.ts` (mover o borrar con justificacion) y `lib/contrast.ts`.
- **T1** Frontend piloto: **fidelidad** (`lib/loyalty.ts` + test, `LoyaltyPage`, `LoyaltyNotice`, `LoyaltyProgramForm`, `ClientLoyaltySection`, `landing/components/LoyaltySection`; solo importa api, permissions y money). Validar con `tsc`, tests y build.
- **T2** Frontend: `shared/` y el resto de las funcionalidades, hojas primero (auditoria, paquetes, lista de espera, exportes, gastos/ganancia, productos), luego los nudos (reservas, clientes, equipo/nomina, POS, ajustes, landing, auth). Senas se migra cuando F11 este publicada.
- **T3** Backend: crear el nucleo compartido y un `testutil` (fakes compartidos con nombres unicos), en un commit aparte.
- **T4** Backend piloto: **fidelidad** (hoja: 11-14 archivos, ~1.2k lineas, solo depende de puertos). Prueba el patron `internal/<feature>/{,postgres,httpapi}`, las pruebas de integracion y los fakes.
- **T5** Backend por orden: auditoria -> exportes -> gastos/ganancia -> productos -> paquetes -> notificaciones/tiempo real -> lista de espera (necesita disponibilidad detras de una interfaz) -> equipo/invitaciones -> horarios/ausencias. LOS NUDOS AL FINAL: reservas+disponibilidad+publicas+enlaces+recordatorios JUNTO CON senas (ciclo), POS/ventas/dashboard, clientes, ajustes/salon, auth/2FA/sesion.
- **T6** Backend: partir `ports.go` por funcionalidad segun las secciones de arriba (se va haciendo en cada tanda), constructores `NewX(...)` por funcionalidad, `main.go` y `routes.go` reducidos a componer modulos.
- **T7** Documentar la estructura nueva (README/AGENTS del repo y `docs/`), actualizar `Makefile`, CI y las rutas de los docs viejos, y registrar en el roadmap.

## Riesgos

1. Ciclo reservas <-> senas: sin un nucleo que tenga `BookingCanceller`, `BookingLinkRepository` y `DepositCreator`, no compila.
2. Los fakes compartidos (`newPOSEnv` x55, `fakeMailer` x18, `fakeSettings` x15) fuerzan un `testutil` o una duplicacion masiva.
3. El glob de tests del frontend pierde tests en silencio si se mueven a carpetas anidadas.
4. Sin alias de rutas, cientos de imports relativos a reescribir.
5. `Makefile`, CI y docs con rutas escritas.
6. `main.go` y `routes.go` son un punto unico de cableado: se tocan al final, no en cada commit.
7. No verificado por el explorador: diferencias entre HEAD y el arbol de trabajo en archivos que no son de F11 (no tuvo shell).

## Log

- **L1** (usuario) «mira en lo que haces te voy a plantear esto, entre al backend no he revisado el frontend pero debe ser lo mismo, estamos usando arquitectura hexagonal para un trabajo mas ordenado pero llegamos al desorden entras a una carpeta y tiene 20 archivos de codigo, vamos a ordenar bien eso, un ejemplo se lo vi a gentleman programing con su curso del frontend que el programa muy ordenado, en componentes hago una carpeta con el nombre del componente y ahi va todo lo de ese componente y asi todo esta en orden, tu valora y usa la mejor forma de mantener un codigo ordenado y que se encuentre todo rapido».
- **L2** (usuario, eleccion) «Despues de F11, antes de F13 (recomendado)».
- **L3** (usuario) «a ver en lo que trabaja el sub agente explora el backend y el frontend y deja anotado lo que encuentres»: este documento.
