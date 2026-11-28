# Decisiones

- D1: Gin + Postgres + Redis, mismo patrón que `chatapp` para reusar experiencia.
- D2: Todo dato cuelga de `salon_id`; sin tenant no hay query ni archivo.
- D3: Reservas con constraint anti-solape en DB + transacción; 409 ante carrera.
- D4: PWA offline con `client_op_id` e idempotencia en servidor (igual que MiPyME).
- D5: Archivos a MinIO/S3 con variantes webp (patrón de stickers de whatsapp-fake).
- D6: Fase 1 = auth + tenants + migraciones; sin eso no se avanza a reservas.

## 2026-02 — Organización por funcionalidad

**Qué:** el código pasó de estar ordenado por CAPA (`domain/`, `ports/`, `app/`,
`adapters/{http,postgres}`) a estar ordenado por FUNCIONALIDAD:
`backend/internal/<funcionalidad>/{,postgres,httpapi}`, con el núcleo compartido
en `internal/{domain,ports,kernel}` y los adaptadores compartidos en
`internal/adapters/{http,postgres,realtime,storage,imaging,mail}`. El frontend
quedó igual: `src/features/<nombre>/{pages,components,lib,index.ts}` con
`src/lib/` para lo transversal. Mapa completo en `docs/ARQUITECTURA.md`.

**Por qué:** con el orden por capa, tocar una sola funcionalidad (por ejemplo
fidelidad) obligaba a abrir cuatro carpetas de 80 a 140 archivos; los tests de
una funcionalidad quedaban lejos de su código y el alcance real de un cambio no
se veía en el árbol.

**Cómo (solo mover):** `git mv` por funcionalidad, un commit por funcionalidad,
compilando y con toda la suite verde después de cada uno. Cero cambios de
comportamiento: mismas rutas, mismo orden de middleware, mismo JSON y mismos
status. El composition root (`backend/cmd/api`) se partió en
`main.go` + `wire_<funcionalidad>.go`, y el registro de rutas en
`internal/adapters/http/routes.go` + `routes_<funcionalidad>.go`; un test de
auditoría lee todos los `routes*.go` para seguir vigilando la lista cerrada.

**Tradeoffs:**

- Más archivos chicos y más paquetes (un paquete Go por funcionalidad): el
  alcance de un cambio se ve de un vistazo, pero un cambio transversal toca
  varios paquetes.
- El ciclo real reservas <-> señas no se puede resolver importándose: se resolvió
  con interfaces (depositCreator en `bookings` y `BookingCanceller`,
  `BookingNoticeRepository`, `BookingLinkRepository` en `ports`). Es una
  indirección más a cambio de que los módulos compilen por separado.
- La infraestructura de Postgres que no es de una funcionalidad (pool,
  migraciones, timeout de transacción, identidad) quedó en
  `internal/adapters/postgres` (`pgstore`). No se partió por funcionalidad a
  propósito: la usan todas.
