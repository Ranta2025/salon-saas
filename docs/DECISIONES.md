# Decisiones

- D1: Gin + Postgres + Redis, mismo patrón que `chatapp` para reusar experiencia.
- D2: Todo dato cuelga de `salon_id`; sin tenant no hay query ni archivo.
- D3: Reservas con constraint anti-solape en DB + transacción; 409 ante carrera.
- D4: PWA offline con `client_op_id` e idempotencia en servidor (igual que MiPyME).
- D5: Archivos a MinIO/S3 con variantes webp (patrón de stickers de whatsapp-fake).
- D6: Fase 1 = auth + tenants + migraciones; sin eso no se avanza a reservas.
