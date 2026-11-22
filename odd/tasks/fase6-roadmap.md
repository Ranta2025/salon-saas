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

## Contrato F2b (Mi agenda) — solo frontend
Quién: es "reservable" quien aparece en `GET /salons/:id/staff` (F2a: rol professional, o dueño/administración con `attends`). No hace falta backend nuevo: los turnos salen del mismo `GET /salons/:id/bookings?from&to` que usa el calendario, filtrados por `professional_id === user.id`, en la zona horaria del salón (`/settings`).
- S-F2b-1 Página `/panel/mi-agenda`: título "Mi agenda" y la fecha de hoy en la zona del salón; resumen ("3 turnos hoy", próximo turno con hora y nombre); lista de MIS turnos de hoy en orden con estado, marca "Ahora" si está en curso y "Siguiente" para el próximo; estado vacío honesto "No tenés turnos hoy". Con la API caída: "Sin conexión: mostrando datos guardados" o el error, nunca el vacío como si fuera dato real (misma regla que E-10).
- S-F2b-2 Tira de la semana (lunes a domingo) con la cantidad de mis turnos por día; tocar un día carga ese día; día de hoy marcado; navegable con teclado.
- S-F2b-3 Acciones sobre mis turnos con la misma regla del calendario (`canManageBooking`: cancelar, no se presentó, atendida); reprogramar solo si el rol lo permite y el estado es "agendada".
- S-F2b-4 Entrada: al iniciar sesión, quien tiene rol `professional` aterriza en `/panel/mi-agenda` (hoy va al calendario por E-02); dueño/administración que atienden siguen en `/panel` con una tarjeta/enlace "Mi agenda"; recepción y administración que no atienden no cambian.
- S-F2b-5 Menú: la entrada "Mi agenda" aparece solo si el usuario es reservable en el salón activo; en el cambio de salón se recalcula.
- S-F2b-6 Calendario: botón/alternador "Mi agenda / Todo el salón"; quien atiende lo abre resaltando su propia columna; recepción y administración sin atender siguen viendo todo el salón por defecto.
- Reglas transversales: móvil 390 px y escritorio 1280 px sin desborde, objetivos de 44 px, foco visible, textos en español, lógica pura en `src/lib` con tests (selección de mis turnos, órdenes, "ahora/siguiente", conteo semanal, reglas de quién es reservable).
- Fuera de alcance: privacidad de datos de otras profesionales, oficios y servicios (F2c), horarios (F2d).

## Contrato F2c (oficio y servicios por profesional)
Regla: un turno público solo se ofrece y se asigna a una profesional RESERVABLE (F2a) que HACE ese servicio. El oficio es un texto visible (barbero, estilista...), no un permiso. La clienta sigue eligiendo solo el servicio (L4).
- S-F2c-1 Migración 016 idempotente: `memberships.trade text not null default ''` (máx 40); tabla `professional_services(salon_id, user_id, service_id)` con PK (salon_id, user_id, service_id), FK a memberships(user_id, salon_id) y a services(id) con ON DELETE CASCADE, índice por (salon_id, service_id); `bookings.service_id text null` (FK a services ON DELETE SET NULL). Backfill SOLO al crear la tabla: cada miembro reservable (professional, o owner/admin con attends) x cada servicio existente de su salón, para no cambiar lo que hay hoy. Válida en base fresca y migrada; reaplicar no duplica ni pisa cambios.
- S-F2c-2 Dominio: `Membership.Trade` (validación: trim, máx 40, sin saltos de línea); puerto `ProfessionalServiceRepository` (ListServiceIDs(salonID, userID), SetServices(salonID, userID, serviceIDs) reemplazo atómico en una transacción, ListProfessionalsFor(salonID, serviceID)); `Booking.ServiceID` opcional.
- S-F2c-3 API: `PATCH /salons/:id/members/:uid` acepta `trade` (string) además de `attends`, ambos opcionales y al menos uno obligatorio (si no, 400 {"error":"Indicá qué cambiar"}); `trade` solo en miembros reservables (si no, 400 {"error":"Ese rol no tiene oficio"}); `PUT /salons/:id/members/:uid/services` body `{"service_ids":["..."]}` -> 200 `{"service_ids":[...]}`; solo owner/admin; solo miembros reservables (400 {"error":"Ese rol no hace servicios"}); un service_id de otro salón o inexistente -> 400 {"error":"Servicio inválido"}; miembro de otro salón -> 404; sin permiso -> 403; lista vacía permitida. `GET /salons/:id/members` y `GET /salons/:id/staff` agregan `trade` y `service_ids` (staff: solo `trade`, sin ids, porque lo ve cualquier miembro). Los JSON de turno de staff agregan `service_id` (puede ser null).
- S-F2c-4 Disponibilidad y reserva pública: los profesionales elegibles de un servicio son los reservables que lo hacen; `AvailabilityService` y `freeProfessional` filtran por eso; si NADIE hace el servicio -> disponibilidad `{"slots":[]}` y la reserva 409 (nunca 500). La reserva pública guarda `service_id` en el turno. La reserva de staff (calendario) NO exige servicio (no cambia).
- S-F2c-5 Altas: al crear un servicio NO se asigna a nadie por defecto salvo que el body traiga `professional_ids` opcional (máx 50, deben ser reservables del salón); un profesional nuevo nace sin servicios y se le asignan desde Equipo. Esto es una excepción consciente: el aviso de "Nadie hace este servicio" (S-F2c-7) evita que el salón quede sin turnos sin darse cuenta.
- S-F2c-6 Frontend Equipo (`/panel/equipo`): campo "Oficio" editable en cada reservable (guarda al salir del campo o con Enter, mensaje del servidor verbatim en error), y por cada reservable un control "Servicios que hace" (lista de casillas con los servicios activos, guardar con un botón, estado optimista con vuelta atrás), accesible por teclado, 44 px, sin desborde a 390 px.
- S-F2c-7 Frontend Servicios (`/panel/servicios`): cada servicio muestra "N profesionales lo hacen" y un aviso visible "Nadie hace este servicio: no se ofrecen turnos" cuando N = 0; el formulario de alta permite marcar qué profesionales lo hacen (envía `professional_ids`). El conteo sale de `GET /members` (owner/admin).
- S-F2c-8 Landing y panel público: sin cambios visuales; si el servicio no tiene profesionales ofrece "sin horarios" como hoy cuando no hay hueco.
- Fuera de alcance: horario por día y descansos (F2d), puestos y equipamiento (F2e), reasignar por ausencias (F2d), elegir profesional al reservar.

## Contrato F2d1 (horario semanal del salón y de cada profesional)
Convenciones: día de la semana ISO 1..7 (1 = lunes, 7 = domingo) calculado en la zona horaria del salón; horas "HH:MM" de 24 h.
- S-F2d1-1 Migración 017 idempotente: `salon_weekly_hours(salon_id, weekday smallint 1..7, open_time, close_time, PK(salon_id, weekday), CHECK open<close)` y `professional_hours(salon_id, user_id, weekday smallint 1..7, start_time, end_time, PK(salon_id, user_id, weekday, start_time), CHECK start<end, FK a memberships ON DELETE CASCADE)`. Sin backfill: la ausencia de filas significa el comportamiento actual.
- S-F2d1-2 Semántica. Salón: sin NINGUNA fila = ventana única `open_time`/`close_time` de `/settings` todos los días (hoy); con 1 o más filas, el día sin fila está CERRADO. Profesional: sin NINGUNA fila = sigue el horario del salón; con filas, solo trabaja en esos tramos (un día sin tramo es día libre); hasta 4 tramos por día, sin solaparse; los descansos son los huecos entre tramos. La ventana efectiva de una profesional un día dado = ventana del salón ∩ sus tramos.
- S-F2d1-3 Dominio puro con tests: validación (formato, inicio < fin, sin solape, máx 4 tramos por día, día 1..7, duplicados), cálculo de la ventana efectiva y de "el turno [inicio, fin] cabe entero en un tramo" (un turno NO puede cruzar un descanso ni el cierre), incluyendo cambio de día por la zona horaria.
- S-F2d1-4 API: `GET /salons/:id/hours` (cualquier miembro) -> `{"days":[{"weekday":1,"open":"09:00","close":"19:00"}],"custom":true|false}` (`custom=false` = sin filas, se devuelve la ventana única expandida a los 7 días); `PUT /salons/:id/hours` body `{"days":[...]}` solo owner/admin, reemplazo atómico, lista vacía = volver al horario único; `GET /salons/:id/members/:uid/hours` (owner/admin, o la propia persona) -> `{"segments":[{"weekday":1,"start":"09:00","end":"13:00"}],"custom":bool}`; `PUT` igual body, solo owner/admin, solo miembros reservables; errores en español exactos: "Horario inválido: el inicio debe ser anterior al fin", "Los tramos de un mismo día no pueden solaparse", "Máximo 4 tramos por día", "Día inválido", miembro de otro salón 404, sin permiso 403. `GET /public/salons/:slug` agrega `hours: [{"weekday":1,"open":"09:00","close":"19:00"}]` (solo los días abiertos, ya expandido).
- S-F2d1-5 Disponibilidad y reserva pública: un horario se ofrece a una profesional solo si el turno cabe entero en la ventana efectiva de ese día; la asignación automática aplica la misma regla; sin profesional con ventana ese día = `{"slots":[]}` y 409 al reservar (nunca 500). Un día cerrado del salón no ofrece nada. La reserva de staff desde el calendario NO se restringe por horario (como hoy: solo el choque).
- S-F2d1-6 Frontend Ajustes (`/panel/ajustes`): editor de horario semanal del salón (7 filas: interruptor Abierto/Cerrado, apertura, cierre; botón "Guardar horario" y "Volver al horario único"); Frontend Equipo: por cada reservable un bloque "Horario" (7 días, hasta 4 tramos por día, "Igual que el salón" por defecto, copiar un día a otros, descansos visibles), errores del servidor verbatim, accesible por teclado, 44 px, sin desborde a 390 px. Landing: el bloque de horario usa `hours` de la API (reemplaza el texto "indicativo" de E-09 y la constante), con "Cerrado" en los días sin horario y el día de hoy resaltado.
- Fuera de alcance: vacaciones y días libres puntuales (F2d2), cubrir ausencias (F2d3), puestos (F2e).

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
