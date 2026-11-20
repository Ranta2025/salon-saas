# Salón SaaS — Fase 3: POS + servicios + web profesional

## Specs
- S1 (usuario): "vamos a hacer parte por parte y vas probando no ir a lo loco y asi
  vamos aprendiendo tambien de lo que se esta haciendo" — dos unidades
  independientes en paralelo (backend POS / frontend web), cada una con sus
  pruebas; el orquestador integra y verifica.
- S2 (usuario): "para el diseno de la web cuando vayas a hacerlo, ten en cuenta un
  diseno profesional busca imagenes en la web con excelente calidad, no fotos
  malas, relacionados con el tema busca una paleta de colores que se usen en
  salones, y disena una pagina web buena llamatica que atraiga clientes" — landing
  pública del salón con paleta de salón, fotos de alta calidad verificadas y
  llamada a la acción de reserva.
- S3 (usuario): "siempre buenas practicas respetando principios solid y patrones
  de diseno" — misma arquitectura hexagonal: dominio, ports, app, adapters.
- S4 (usuario): "rdd a todo" — el orquestador corre assess/revisión por unidad de
  trabajo antes de integrar.

## API contract (acordado; ambos writers deben respetarlo)

Base `/api/v1`, auth `Authorization: Bearer <jwt>` salvo los endpoints públicos.

```
GET    /api/v1/salons/:id/services            -> {services:[...]}
POST   /api/v1/salons/:id/services            -> 201
PUT    /api/v1/salons/:id/services/:sid       -> 200
POST   /api/v1/salons/:id/sales               -> 201 {sale_id, total}
GET    /api/v1/salons/:id/sales?from=&to=     -> {sales:[...]}
POST   /api/v1/salons/:id/cash-closes         -> 201 {close_id, expected_cash}
GET    /api/v1/salons/:id/cash-closes         -> {closes:[...]}
GET    /api/v1/salons/:id/dashboard            -> {revenue_today, appointments_today, occupancy_rate, top_services:[...]}
```

Entidades:

```
Service  { id, salon_id, name, duration_min, price_cents, commission_pct, active }
Sale     { id, salon_id, professional_id, client_name, items:[{service_id, qty, unit_price_cents}], total_cents, method, created_at }
CashClose{ id, salon_id, opened_at, closed_at, system_cash_cents, counted_cents, difference_cents, note }
```

Reglas: precios en centavos (nada de floats para dinero); `total_cents` se calcula
en el servidor, nunca se acepta del cliente; todo scoped por `salon_id`; toda
escritura audita actor.

## Tasks

- [ ] T1 (backend): catálogo de servicios — dominio `Service`, `ports.ServiceRepository`,
  `app.ServiceService` (CRUD con permiso owner/admin), `pgstore.Services`,
  migración 004, HTTP CRUD, tests unit + integración. Route: writer A.
- [ ] T2 (backend): POS — dominio `Sale` (cálculo de total en servidor, ítems con
  qty y precio unitario), `app.SaleService` (registrar venta), cierre de caja
  (`CashClose` con diferencia contable), `pgstore` + migración 005, HTTP,
  dashboard con agregados, tests unit + integración + e2e contra Postgres real.
  Route: writer A. Depende de T1.
- [ ] T3 (frontend): landing pública del salón con diseño profesional — paleta,
  hero, servicios, galería de fotos, CTA de reserva, responsive y accesible.
  Route: writer B. Usa las fotos de `docs/assets/images.md`.
- [ ] T4 (frontend): shell de la app interna — login, panel, calendario de reservas
  del día, POS (punto de venta) consumiendo la API, catálogo de servicios.
  Route: writer B. Depende de T1/T2 para los endpoints.

## Allowed edit surfaces

- Writer A: `backend/**`, `docs/api/**` (no toca `frontend/**`)
- Writer B: `frontend/**`, `docs/design/**` (no toca `backend/**`)

## Paleta (definida por el orquestador, para que no se disperse)

```
--ink:       #1E1418   texto principal sobre claro
--plum:      #3B1E33   fondo oscuro / hero
--wine:      #6B2D4F   primario de marca
--gold:      #C9A227   acento (CTA, íconos, filetes)
--champagne: #E8D9B5   acento suave
--cream:     #FAF5EF   fondo claro
--rose:      #E7C9D2   tinte secundario
```

Tipografía: una serif de display para títulos (Cormorant Garamond / Playfair
Display) + sans del sistema para texto. Sin dependencias de pago ni Google Fonts
obligatorio: usar `@font-face` con fallback de sistema si no hay red.

## Log

- L1 (usuario): "si" — aprueba arrancar Fase 3 con dos writers en paralelo.
- L2 (orquestador): Unsplash y Pexels bloquean el entorno (Anubis anti-bot y
  Cloudflare) y los proveedores de búsqueda fallaron; las fotos se sourcingaron
  de Wikimedia Commons (licencias libres), revisadas visualmente una por una
  antes de elegir. URLs en `docs/assets/images.md`.
- L3 (orquestador): RDD — assess por unidad de trabajo antes de integrar.
