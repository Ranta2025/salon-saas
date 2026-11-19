# Salón SaaS — Fase 1: base hexagonal + auth/tenants

## Specs
- S1: "arquitectura hexagonal ya que es un proyecto escalable" — puertos y
  adaptadores: dominio puro sin dependencias externas; app orquesta; adapters
  (http/postgres/security) implementan puertos.
- S2: "siempre buenas practicas respetando principios solid y patrones de diseno"
  — SRP por servicio, DIP vía interfaces, factory/composition root en `cmd/api`,
  repository + DTO, middleware auth.
- S3: "vamos a hacer parte por parte y vas probando no ir a lo loco y asi vamos
  aprendiendo" — cada parte con test que se corre antes de avanzar; cada cierre
  explica el qué/por qué.
- S4: "para el diseno de la web cuando vayas a hacerlo, ten en cuenta un diseno
  profesional... paleta de colores que se usen en salones" — Fase 3; buscar
  referencias e imágenes de alta calidad, nada de fotos malas.

## Tasks
- [x] T1: esqueleto hexagonal (domain/ports/app/adapters) + entidades User/Salon/
  Membership + errores — con tests de dominio.
- [x] T2: casos de uso Register/Login + CreateSalon/AddMember con repos fake — TDD.
- [x] T3: adaptadores security (bcrypt+jwt) y http (handlers+middleware+rutas) — tests.
- [x] T4: postgres real + migraciones + compose infra + smoke e2e contra DB real.
- T5: frontend base con paleta salón + landing pública (Fase 3, después).

## Log
- L1: usuario pide hexagonal+SOLID, parte por parte con pruebas y aprendizaje;
  diseño web profesional con paleta de salón cuando toque frontend.
