-- 012_indexes: índices del finding B-19.
--
-- La agenda se consulta SIEMPRE por salón y rango de fechas (calendario,
-- disponibilidad pública, dashboard). Sin este índice compuesto, cada lectura
-- escanea la tabla entera y crece con los turnos de TODOS los salones.
--
-- memberships se busca por salon_id en cada request autenticado (scope de
-- tenant) y en la pantalla de equipo: la PK es (user_id, salon_id), así que un
-- lookup por salón no la usa.
--
-- Idempotente: IF NOT EXISTS sobre el nombre del índice; re-ejecutable.
CREATE INDEX IF NOT EXISTS bookings_salon_starts_idx ON bookings (salon_id, starts_at);
CREATE INDEX IF NOT EXISTS memberships_salon_idx ON memberships (salon_id);
