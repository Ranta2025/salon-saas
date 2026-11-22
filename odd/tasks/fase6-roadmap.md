# Fase 6 — roadmap de funcionalidades (salon-saas)

Se implementa en orden, una por rama/PR con tests, docs y revisión. Empieza cuando cierre la fase 5
(ronda 3, USD + teléfono +53, recordatorios por email). Una funcionalidad no empieza hasta que la anterior está
verificada y commiteada.

## Specs
- S1 (usuario, verbatim): "si ve implementando todo poco a poco y para el pago stripe no lo podemos usar cuando terminemos creamos una pagina que simule un banco y esta se conecta con aquella y se realiza el pago asi"
- S2: el cobro va detrás de un puerto `PaymentGateway`; el adaptador de hoy habla con un banco simulado (servicio y página aparte); un adaptador Stripe futuro no toca el resto.

## Tasks (orden)
- [ ] F1 Link firmado de un solo uso para cancelar o reprogramar desde el email
- [ ] F2a Solo atiende quien debe: un rol por persona (sin roles múltiples; decisión del usuario). Es reservable quien tiene el rol `professional`, o un dueño/administración con la marca `attends` (el dueño-barbero); hoy owner y admin cuentan siempre como staff y inflan la disponibilidad. Migración idempotente: columna `attends` en memberships, true para los `professional` existentes y para owner/admin ya existentes (no cambia lo que hay hoy), false para el resto
- [ ] F2b "Mi agenda": al entrar, quien atiende ve primero SUS turnos (hoy y la semana) y puede alternar a "Todo el salón" si su rol lo permite; recepción y administración ven todo
- [ ] F2c Oficio y servicios por profesional: cada profesional tiene un oficio visible (barbero, estilista, colorista, manicurista; texto libre) y la lista de servicios que hace (`professional_services`); la disponibilidad se calcula por profesional (sabe hacer el servicio ∩ sin turno ∩ su horario) y se une; hoy se asume que todo el equipo hace todos los servicios
- [ ] F2e Puestos y equipamiento con capacidad: el administrador define tipos de recurso y cuántas unidades hay (p. ej. 3 puestos de peluquería, 2 sillas de barbería, 1 lavacabezas); cada servicio indica qué tipo de recurso usa; un turno ocupa una profesional Y una unidad del recurso. Cada unidad es una fila con nombre y su propia EXCLUDE (unit_id, rango) igual que las profesionales: la capacidad queda garantizada por la base y no por un contador
- [ ] F2d Horario por día de la semana, descansos y bloqueos (vacaciones, días libres por profesional)
- [ ] F2g Contratar por invitación: el dueño/administración invita por email con enlace de un solo uso y vencimiento (7 días), la persona crea su cuenta y entra con el rol ya asignado (hoy exige que ya tenga cuenta). Quitar a alguien DESACTIVA su membresía (`ended_at`) en vez de borrarla: conserva el historial de turnos y ventas y puede reactivarse
- [ ] F3 Nómina y comisiones: cada profesional tiene su modelo de pago (sueldo fijo, % de comisión o mixto); la comisión sale de `services.commission_pct`, que YA existe; liquidación por período (semana, quincena o mes), detalle por profesional, marca de pagado y exportación
- [ ] F3b Gastos y ganancias: registrar gastos (alquiler, insumos, servicios, otros) con fecha y categoría; ganancia neta = ingresos - gastos - nómina por período; panel de ganancias por día/semana/mes
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

## Contrato F2a (solo atiende quien debe)
Regla: una persona es RESERVABLE (aparece en agenda, disponibilidad y selectores) si su rol es `professional` O tiene `attends = true`. Recepción y cliente nunca atienden. Un solo rol por persona (L3).
- S-F2a-1 Migración 015 idempotente: `memberships.attends boolean not null default false`; backfill `true` para las filas existentes con rol owner, admin o professional (no cambia lo que hay hoy). Válida en base fresca y migrada.
- S-F2a-2 Dominio: `Membership.Attends` y `Membership.Bookable()` (professional siempre; owner/admin si attends). `Role.IsStaff()` deja de decidir quién atiende: los 4 usos (public_bookings.go x2, professional.go, availability.go) pasan a la regla nueva. `StaffMember` lleva `Attends`.
- S-F2a-3 Postgres: `ListStaff` devuelve `role='professional' OR attends`; `Find` de membership lee `attends`. Consultas siempre scopeadas por salon_id.
- S-F2a-4 Alta: al crear un salón el owner nace con `attends=true` (salón de una persona); un profesional agregado queda con `attends=true`; admin/recepción con `false`.
- S-F2a-5 API: `GET /salons/:id/members` agrega `attends` (bool) a cada miembro. Nuevo `PATCH /salons/:id/members/:uid` body `{"attends": true|false}` -> 200 con el miembro. Solo owner/admin. Rol `professional` no se puede poner en false -> 400 {"error":"Las profesionales siempre atienden"}; rol reception/client -> 400 {"error":"Ese rol no atiende clientes"}; miembro de otro salón -> 404; sin permiso -> 403. `POST /members` acepta `attends` opcional (default según S-F2a-4).
- S-F2a-6 UI Equipo (`/panel/equipo`): interruptor "Atiende clientes" en dueño y administración (llama al PATCH, estado optimista con vuelta atrás ante error), etiqueta fija "Atiende" en profesionales, nada en recepción; aviso visible si NADIE atiende ("Nadie atiende todavía: activá 'Atiende clientes' en alguien para recibir turnos"). Textos en español, accesible por teclado, objetivos de 44 px.
- Fuera de alcance de F2a: oficios, servicios por profesional, horarios, puestos (F2b..F2e).

## Permisos de dinero y gestión (decisión del usuario, L6)
| Acción | dueño | admin | recepción | profesional |
|---|---|---|---|---|
| Contratar / desactivar personal, equipamiento (F2e) | sí | sí | no | no |
| Ventas y cierre de caja | sí | sí | sí | no |
| Sueldos de todos y ganancia neta (F3, F3b) | sí | sí | NO | NO |
| Gastos (registrar y ver) | sí | sí | no | no |
| Sus propias comisiones | sí | sí | no | sí (solo las suyas) |
Frontera de alcance: salon-saas lleva el dinero operativo (caja, comisiones, gastos, nómina del período); la contabilidad completa queda para mipyme-os.
Riesgo: F3 y F3b tocan dinero y permisos (item 2 de alto riesgo): cada una en su commit y con revisión nativa propia.

## Riesgos conocidos
- F11 toca dinero (riesgo alto): el estado del pago lo fija solo el servidor, el callback va firmado y es idempotente, nunca se confía en el navegador.
- F10 y F11 piden revisión nativa RDD propia, cada una en su commit.

## Log
L1: ver S1. El usuario aprobó el orden F1 -> F2 -> F3 -> F5 -> F6 y dejar F11/F12/F14 para el final.

L2 (usuario, verbatim): "va un panel de administracion no ? donde los trabajadores ven sus turnos, mira en los roles cada trabajador aparte de ser uno generico si son varios trabajadores que tengan uno o mas roles , y cuando entren le salen los turnos que tienen planificado para el los turnos disponibles son dependiendo de la cantidad de empleados"
Evidencia al debatirlo (código): memberships tiene PK (user_id, salon_id) = un rol por salon; `Role.IsStaff()` incluye owner/admin/professional; no existe vínculo profesional-servicio; el calendario muestra todas las columnas a todos. El panel ya existe (/panel).

L3 (usuario, verbatim): "no cuando me referia a varios roles me referia barbero estilista y eso pero como lo dices es mejor uno solo de profesional que es el que atiende y ya"
Decisión: un solo rol de acceso `professional` para quien atiende; el oficio (barbero, estilista...) es un dato aparte ligado a servicios (F2c). Se descarta la propuesta de roles múltiples. Queda la marca `attends` solo para el dueño/administración que también atiende.

L4 (usuario, verbatim): "el estilista hace keratina tratamiento alisado etc, pero el barbero pela, si para las 10 se cogio un turno de planchado ejemplo si hay una estilista esta ocupada, pero si alguien biene a pelarse puede sacar turno, y si hay dos estilistas trabajando en el dia dos personas pueden pedir turnos para las diez ... se define por norma cuantos van a trabajar por ejemplo tienes seis estilistas pero trabajan 3 porque tres descansan siempre tienes un numero fijo de personas qeu trabajan, cuando el cliente elija el tipo de servicio que quiere ahi se filtra y se ve si pa lo que quiere los turnos que hay y la hora"
Decisión (debatida con evidencia): NO se usa un cupo anónimo por número fijo. Se calcula desde profesionales con nombre: oficio + servicios que hace (F2c) + horario semanal propio con días de descanso (F2d). "Seis estilistas, trabajan tres" sale de los horarios (plantilla semanal para cargarlo rápido). Motivos: (1) los servicios duran distinto, hay que contar intervalos y no solo la hora de inicio; (2) la EXCLUDE de Postgres por profesional garantiza que no haya solape ni con reservas simultáneas y un cupo anónimo la perdería; (3) "mi agenda" y las comisiones (F3) necesitan saber quién hace cada turno.
Respuesta del usuario: la clienta elige SOLO el servicio; el sistema asigna a una profesional libre que lo haga (hoy ya lo hace `freeProfessional`, falta el filtro por servicio); el salón puede reasignar desde el panel. Elegir profesional queda fuera de alcance por ahora.

L5 (usuario, verbatim): "puedo tener 5 trabajadoras pero en la instalacion las roto porque tengo equipamiendo para que trabajen 3 a eso me refiero que eso se puede definir en la app el administrador , tantos de esto y tantos del otro, y el lo del turno no son varios turnos es un turno y si necesita mas tiempo se registra en el turno pero ejemplo si tengo un turno a las 11 ya dado a las 10:30 no puedo dar un turno de una hora"
Decisión: el límite viene del EQUIPAMIENTO, no de un número anónimo de personas. Se modela con unidades de recurso con nombre (F2e), cada una con su EXCLUDE; un turno es UNO solo con su duración total y ocupa profesional + unidad. El choque de intervalos ("un turno a las 11 impide uno de 1 h a las 10:30") ya lo cubre hoy `FindOverlapping` + la EXCLUDE por profesional.

L6 (usuario, verbatim): "no esta de mas decirlo que el dueno o administrador deben poder contratar gente y agregar o quitar equipamiento y todas esas cosas que ban por la canaleta , ver las ganancias salarios a pagar todo eso"
Decisión (respuesta del usuario): sueldos y ganancia neta los ven dueño Y administración; el profesional solo sus comisiones; recepción no ve sueldos. Verificado en el código: `services.commission_pct` ya existe; no existen tablas de gastos ni de nómina; `POST /members` falla si la persona no tiene cuenta; quitar un miembro hoy hace DELETE.
