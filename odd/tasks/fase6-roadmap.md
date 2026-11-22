# Fase 6 — roadmap de funcionalidades (salon-saas)

Se implementa en orden, una por rama/PR con tests, docs y revisión. Empieza cuando cierre la fase 5
(ronda 3, USD + teléfono +53, recordatorios por email). Una funcionalidad no empieza hasta que la anterior está
verificada y commiteada.

## Specs
- S1 (usuario, verbatim): "si ve implementando todo poco a poco y para el pago stripe no lo podemos usar cuando terminemos creamos una pagina que simule un banco y esta se conecta con aquella y se realiza el pago asi"
- S2: el cobro va detrás de un puerto `PaymentGateway`; el adaptador de hoy habla con un banco simulado (servicio y página aparte); un adaptador Stripe futuro no toca el resto.

## Tasks (orden)
- [ ] F1 Link firmado de un solo uso para cancelar o reprogramar desde el email
- [ ] F2a Equipo con roles múltiples: una persona puede tener varios roles en un salón (`memberships` pasa de PK (user_id, salon_id) a PK (user_id, salon_id, role)); los permisos son la unión; SOLO el rol `professional` hace a alguien reservable (hoy owner y admin cuentan como staff y inflan la disponibilidad aunque no atiendan)
- [ ] F2b "Mi agenda": al entrar, quien atiende ve primero SUS turnos (hoy y la semana) y puede alternar a "Todo el salón" si su rol lo permite; recepción y administración ven todo
- [ ] F2c Servicios que hace cada profesional (`professional_services`) y la disponibilidad calculada por profesional (horario propio ∩ sabe hacer el servicio ∩ sin turno) y unida; hoy se asume que todo el equipo hace todos los servicios
- [ ] F2d Horario por día de la semana, descansos y bloqueos (vacaciones, días libres por profesional)
- [ ] F3 Comisiones por profesional + reporte por período
- [ ] F4 Exportar CSV (ventas, cierres, clientes)
- [ ] F5 Historial de la clienta (visitas, gasto, última visita, alergias)
- [ ] F6 Productos e inventario en el POS (stock, alerta de stock bajo)
- [ ] F7 Lista de espera (aviso al liberarse un hueco)
- [ ] F8 Paquetes y bonos prepagos con saldo por clienta
- [ ] F9 Fidelidad (puntos o descuento en la visita N)
- [ ] F10 Registro de auditoría y 2FA para dueño/administración
- [ ] F11 Seña online: puerto PaymentGateway + banco simulado (servicio, página de pago, callback HMAC idempotente, tarjetas de prueba aprobada/rechazada/sin fondos, aviso visible de simulación)
- [ ] F12 WhatsApp como segundo canal de recordatorios (requiere verificar empresa en Meta)
- [ ] F13 Reseñas posvisita
- [ ] F14 Varias sucursales, moneda y marca por salón

## Seguimientos de la revisión nativa de la fase 5 (previos a F1, no bloqueantes)
Salieron de la revisión por tramos (S3 aprobado; S1 y S6 escalados sin texto de hallazgo expuesto). Se atacan antes de F1.
- [ ] H1 (el más serio) `MaxJSONBody` no aplica el tope de 1 MB si el Content-Type empieza con multipart/form-data o image/: un endpoint JSON recibe el encabezado falseado y se salta el límite. Aplicar el tope por ruta, no por Content-Type.
- [ ] H2 El rate limiter en memoria no desaloja los cubos de IPs que no vuelven: purga periódica.
- [ ] H3 Trusted proxies = todos los rangos privados: hacerlo configurable (`TRUSTED_PROXIES`) y documentarlo.
- [ ] H4 `staff` devuelve 403 ante cualquier error de `Find` de membresía: distinguir 500 y loguear.
- [ ] H5 `storage/s3.go` hace `io.ReadAll` del objeto sin tope: agregar `LimitReader` (hoy lo acota el tope de 8 MB de subida).
- [ ] H6 `backend/api` (binario compilado) aparece como archivo sin seguimiento: revisar que `.gitignore` lo cubra y borrarlo.

## Riesgos conocidos
- F11 toca dinero (riesgo alto): el estado del pago lo fija solo el servidor, el callback va firmado y es idempotente, nunca se confía en el navegador.
- F10 y F11 piden revisión nativa RDD propia, cada una en su commit.

## Log
L1: ver S1. El usuario aprobó el orden F1 -> F2 -> F3 -> F5 -> F6 y dejar F11/F12/F14 para el final.

L2 (usuario, verbatim): "va un panel de administracion no ? donde los trabajadores ven sus turnos, mira en los roles cada trabajador aparte de ser uno generico si son varios trabajadores que tengan uno o mas roles , y cuando entren le salen los turnos que tienen planificado para el los turnos disponibles son dependiendo de la cantidad de empleados"
Evidencia al debatirlo (código): memberships tiene PK (user_id, salon_id) = un rol por salon; `Role.IsStaff()` incluye owner/admin/professional; no existe vínculo profesional-servicio; el calendario muestra todas las columnas a todos. El panel ya existe (/panel).
