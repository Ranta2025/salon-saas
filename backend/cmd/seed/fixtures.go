package main

// Contenido de la demo: todo lo que se puede leer en el panel. Está acá (y no
// disperso en los pasos) para poder revisarlo de un vistazo y cambiar un precio
// o un nombre sin tocar la lógica.

// demoUser es una cuenta de la demo. Todas comparten la misma clave
// (demoPassword) y NINGUNA tiene 2FA.
type demoUser struct {
	Email string
	Name  string
	Role  string
	// Attends: atiende clientes (la marca de F2a). Recepción y administración no.
	Attends bool
	Trade   string
	// Services son los servicios que hace (por nombre del catálogo).
	Services []string
	// PayModel: cómo cobra (F3.2).
	PayModel payModel
}

// payModel es cómo cobra un miembro: un fijo por período y, si corresponde,
// comisión por sus ventas. Los fijos están calibrados con el volumen del seed
// (≈70 turnos por mes): la nómina queda cerca de la mitad de los ingresos y el
// período da una ganancia positiva y creíble.
type payModel struct {
	FixedCents   int
	FixedPeriod  string
	CommissionOn bool
}

// demoUsers son los cinco logins del equipo: dueña, administración, recepción y
// cuatro profesionales (la dueña también atiende: es la peluquera del salón).
var demoUsers = []demoUser{
	{
		Email: "dueno@salon.local", Name: "Valentina Ríos", Role: "owner", Attends: true,
		Trade:    "Peluquera y colorista",
		Services: []string{"Corte de pelo", "Corte y barba", "Color completo", "Brushing", "Peinado de fiesta"},
		PayModel: payModel{FixedCents: 0, FixedPeriod: "month", CommissionOn: true},
	},
	{
		Email: "admin@salon.local", Name: "Malena Ortiz", Role: "admin", Attends: false,
		PayModel: payModel{FixedCents: 18000000, FixedPeriod: "month", CommissionOn: false},
	},
	{
		Email: "recepcion@salon.local", Name: "Bruno Salas", Role: "reception", Attends: false,
		PayModel: payModel{FixedCents: 12000000, FixedPeriod: "month", CommissionOn: false},
	},
	{
		Email: "camila@salon.local", Name: "Camila Ferreyra", Role: "professional", Attends: true,
		Trade:    "Colorista",
		Services: []string{"Color completo", "Retoque de raíz", "Brushing", "Peinado de fiesta"},
		PayModel: payModel{FixedCents: 3000000, FixedPeriod: "month", CommissionOn: true},
	},
	{
		Email: "lucia@salon.local", Name: "Lucía Benítez", Role: "professional", Attends: true,
		Trade:    "Manicura y pedicura",
		Services: []string{"Manicura semipermanente", "Pedicura", "Esculpidas"},
		PayModel: payModel{FixedCents: 0, FixedPeriod: "month", CommissionOn: true},
	},
	{
		Email: "marcos@salon.local", Name: "Marcos Duarte", Role: "professional", Attends: true,
		Trade:    "Barbero",
		Services: []string{"Corte de pelo", "Corte y barba", "Perfilado de barba"},
		PayModel: payModel{FixedCents: 2000000, FixedPeriod: "month", CommissionOn: true},
	},
	{
		Email: "sofia@salon.local", Name: "Sofía Aguirre", Role: "professional", Attends: true,
		Trade:    "Estilista y estética",
		Services: []string{"Corte de pelo", "Brushing", "Facial hidratante", "Depilación con cera"},
		PayModel: payModel{FixedCents: 0, FixedPeriod: "month", CommissionOn: true},
	},
}

// demoService es un servicio del catálogo.
type demoService struct {
	Name          string
	Minutes       int
	PriceCents    int
	CommissionPct int
	Description   string
	ResourceType  string
}

// demoResourceTypes son los puestos y el equipamiento con capacidad (F2e).
var demoResourceTypes = []struct {
	Name  string
	Units []string
}{
	{Name: "Puesto de peluquería", Units: []string{"Puesto 1", "Puesto 2", "Puesto 3"}},
	{Name: "Gabinete de uñas", Units: []string{"Gabinete 1", "Gabinete 2"}},
	{Name: "Camilla", Units: []string{"Camilla de estética"}},
}

// demoServices son los doce servicios del salón, con precio y duración reales.
var demoServices = []demoService{
	{Name: "Corte de pelo", Minutes: 45, PriceCents: 1800000, CommissionPct: 30, ResourceType: "Puesto de peluquería",
		Description: "Corte a tijera y máquina con lavado y peinado incluido."},
	{Name: "Corte y barba", Minutes: 60, PriceCents: 2400000, CommissionPct: 30, ResourceType: "Puesto de peluquería",
		Description: "Corte completo más perfilado y arreglo de barba con toalla caliente."},
	{Name: "Color completo", Minutes: 120, PriceCents: 6500000, CommissionPct: 35, ResourceType: "Puesto de peluquería",
		Description: "Coloración de raíz a puntas con productos sin amoníaco."},
	{Name: "Retoque de raíz", Minutes: 90, PriceCents: 4200000, CommissionPct: 35, ResourceType: "Puesto de peluquería",
		Description: "Retoque de color en la raíz para mantener el tono entre coloraciones."},
	{Name: "Brushing", Minutes: 40, PriceCents: 1500000, CommissionPct: 30, ResourceType: "Puesto de peluquería",
		Description: "Lavado y cepillado con secador o plancha, según el estilo."},
	{Name: "Peinado de fiesta", Minutes: 60, PriceCents: 2200000, CommissionPct: 30, ResourceType: "Puesto de peluquería",
		Description: "Recogidos, ondas y trenzas para eventos, con fijación de larga duración."},
	{Name: "Manicura semipermanente", Minutes: 60, PriceCents: 1600000, CommissionPct: 40, ResourceType: "Gabinete de uñas",
		Description: "Esmaltado semipermanente con preparación de cutícula y dos capas de color."},
	{Name: "Pedicura", Minutes: 60, PriceCents: 1800000, CommissionPct: 40, ResourceType: "Gabinete de uñas",
		Description: "Pedicura completa con exfoliación, cutícula y esmaltado a elección."},
	{Name: "Esculpidas", Minutes: 90, PriceCents: 2600000, CommissionPct: 40, ResourceType: "Gabinete de uñas",
		Description: "Uñas esculpidas en gel con forma y largo a elección."},
	{Name: "Facial hidratante", Minutes: 60, PriceCents: 2800000, CommissionPct: 35, ResourceType: "Camilla",
		Description: "Limpieza, exfoliación y máscara hidratante con masaje facial."},
	{Name: "Depilación con cera", Minutes: 45, PriceCents: 1400000, CommissionPct: 35, ResourceType: "Camilla",
		Description: "Depilación de piernas completas con cera tibia y cuidado posterior."},
	{Name: "Perfilado de barba", Minutes: 30, PriceCents: 1200000, CommissionPct: 30, ResourceType: "Puesto de peluquería",
		Description: "Perfilado de barba con navaja, aceite y bálsamo."},
}

// demoClient es una clienta ficticia. Los emails son @demo.test y los teléfonos
// son del rango de ficción argentino (+54 9 11).
type demoClient struct {
	Name      string
	Phone     string
	EmailName string
	Notes     string
	Allergies string
}

// demoClients son las cuarenta fichas con las que se llena el listado.
var demoClients = []demoClient{
	{Name: "Agustina Molina", Phone: "+54 9 11 5342-1180", EmailName: "agustina.molina", Notes: "Prefiere turnos después de las 18."},
	{Name: "Belén Sosa", Phone: "+54 9 11 5342-1181", EmailName: "belen.sosa", Allergies: "Alergia al amoníaco: usar color sin amoníaco."},
	{Name: "Camila Ibáñez", Phone: "+54 9 11 5342-1182", EmailName: "camila.ibanez"},
	{Name: "Daniela Ferro", Phone: "+54 9 11 5342-1183", EmailName: "daniela.ferro", Notes: "Viene con su hija, reservar dos turnos seguidos."},
	{Name: "Emilia Cabrera", Phone: "+54 9 11 5342-1184", EmailName: "emilia.cabrera"},
	{Name: "Florencia Vidal", Phone: "+54 9 11 5342-1185", EmailName: "florencia.vidal", Allergies: "Piel sensible: cera hipoalergénica."},
	{Name: "Gabriela Núñez", Phone: "+54 9 11 5342-1186", EmailName: "gabriela.nunez"},
	{Name: "Helena Prieto", Phone: "+54 9 11 5342-1187", EmailName: "helena.prieto"},
	{Name: "Inés Ramos", Phone: "+54 9 11 5342-1188", EmailName: "ines.ramos", Notes: "Llegó tarde una vez, avisar por WhatsApp."},
	{Name: "Josefina Paz", Phone: "+54 9 11 5342-1189", EmailName: "josefina.paz"},
	{Name: "Karen Duarte", Phone: "+54 9 11 5342-1190", EmailName: "karen.duarte"},
	{Name: "Ludmila Rojas", Phone: "+54 9 11 5342-1191", EmailName: "ludmila.rojas", Allergies: "Sensibilidad al esmalte con formol."},
	{Name: "Malena Cáceres", Phone: "+54 9 11 5342-1192", EmailName: "malena.caceres"},
	{Name: "Natalia Vera", Phone: "+54 9 11 5342-1193", EmailName: "natalia.vera"},
	{Name: "Olivia Medina", Phone: "+54 9 11 5342-1194", EmailName: "olivia.medina", Notes: "Le gusta el café cortado."},
	{Name: "Paula Godoy", Phone: "+54 9 11 5342-1195", EmailName: "paula.godoy"},
	{Name: "Quimey Álvarez", Phone: "+54 9 11 5342-1196", EmailName: "quimey.alvarez"},
	{Name: "Rocío Salinas", Phone: "+54 9 11 5342-1197", EmailName: "rocio.salinas", Allergies: "Alergia al níquel: evitar pinzas metálicas."},
	{Name: "Sabrina Leiva", Phone: "+54 9 11 5342-1198", EmailName: "sabrina.leiva"},
	{Name: "Tamara Quiroga", Phone: "+54 9 11 5342-1199", EmailName: "tamara.quiroga"},
	{Name: "Valentina Suárez", Phone: "+54 9 11 5342-1200", EmailName: "valentina.suarez"},
	{Name: "Wanda Benítez", Phone: "+54 9 11 5342-1201", EmailName: "wanda.benitez"},
	{Name: "Ximena Correa", Phone: "+54 9 11 5342-1202", EmailName: "ximena.correa", Notes: "Trabaja cerca: prefiere el mediodía."},
	{Name: "Yamila Ferrari", Phone: "+54 9 11 5342-1203", EmailName: "yamila.ferrari"},
	{Name: "Zoe Arce", Phone: "+54 9 11 5342-1204", EmailName: "zoe.arce"},
	{Name: "Antonella Rivas", Phone: "+54 9 11 5342-1205", EmailName: "antonella.rivas", Allergies: "Dermatitis de contacto: guantes de nitrilo."},
	{Name: "Bianca Toledo", Phone: "+54 9 11 5342-1206", EmailName: "bianca.toledo"},
	{Name: "Carla Ojeda", Phone: "+54 9 11 5342-1207", EmailName: "carla.ojeda"},
	{Name: "Dalma Retamozo", Phone: "+54 9 11 5342-1208", EmailName: "dalma.retamozo"},
	{Name: "Estefanía Barrios", Phone: "+54 9 11 5342-1209", EmailName: "estefania.barrios"},
	{Name: "Fátima Ledesma", Phone: "+54 9 11 5342-1210", EmailName: "fatima.ledesma", Notes: "Vino por recomendación de Agustina."},
	{Name: "Guadalupe Ibarra", Phone: "+54 9 11 5342-1211", EmailName: "guadalupe.ibarra"},
	{Name: "Higor Paredes", Phone: "+54 9 11 5342-1212", EmailName: "higor.paredes", Notes: "Cliente de barbería: corte con máquina 3."},
	{Name: "Ignacio Vallejos", Phone: "+54 9 11 5342-1213", EmailName: "ignacio.vallejos"},
	{Name: "Julián Ocampo", Phone: "+54 9 11 5342-1214", EmailName: "julian.ocampo"},
	{Name: "Kevin Almada", Phone: "+54 9 11 5342-1215", EmailName: "kevin.almada"},
	{Name: "Lautaro Nieva", Phone: "+54 9 11 5342-1216", EmailName: "lautaro.nieva", Notes: "Prefiere a Marcos."},
	{Name: "Matías Ferreyra", Phone: "+54 9 11 5342-1217", EmailName: "matias.ferreyra"},
	{Name: "Nicolás Godoy", Phone: "+54 9 11 5342-1218", EmailName: "nicolas.godoy", Allergies: "Alergia al alcohol: after shave sin alcohol."},
	{Name: "Ramiro Cejas", Phone: "+54 9 11 5342-1219", EmailName: "ramiro.cejas"},
}

// demoProduct es un producto del POS con su stock inicial y el umbral de aviso.
type demoProduct struct {
	Name       string
	PriceCents int
	Stock      int
	Threshold  int
}

// demoProducts son los productos de reventa: dos quedan por debajo del umbral
// para que la pantalla de inventario muestre sus alertas.
var demoProducts = []demoProduct{
	{Name: "Shampoo nutritivo 500 ml", PriceCents: 1290000, Stock: 24, Threshold: 6},
	{Name: "Acondicionador reparador 500 ml", PriceCents: 1190000, Stock: 18, Threshold: 5},
	{Name: "Máscara capilar de keratina 250 ml", PriceCents: 1450000, Stock: 9, Threshold: 4},
	{Name: "Aceite de argán 30 ml", PriceCents: 980000, Stock: 12, Threshold: 4},
	{Name: "Sérum antifrizz 50 ml", PriceCents: 1120000, Stock: 3, Threshold: 5},
	{Name: "Laca de fijación fuerte 300 ml", PriceCents: 740000, Stock: 30, Threshold: 8},
	{Name: "Espuma modeladora 250 ml", PriceCents: 690000, Stock: 22, Threshold: 6},
	{Name: "Esmalte semipermanente rojo pasión", PriceCents: 850000, Stock: 11, Threshold: 4},
	{Name: "Esmalte semipermanente nude", PriceCents: 850000, Stock: 2, Threshold: 4},
	{Name: "Crema para manos 150 ml", PriceCents: 560000, Stock: 40, Threshold: 10},
	{Name: "Cera modeladora 100 g", PriceCents: 780000, Stock: 14, Threshold: 5},
	{Name: "Kit de uñas (lima y buffer)", PriceCents: 430000, Stock: 4, Threshold: 6},
}

// demoPackage es un paquete prepago del catálogo.
type demoPackage struct {
	Name       string
	Service    string
	Sessions   int
	PriceCents int
	Validity   int
}

// demoPackages son los tres bonos del salón.
var demoPackages = []demoPackage{
	{Name: "Bono 5 cortes", Service: "Corte de pelo", Sessions: 5, PriceCents: 8100000, Validity: 180},
	{Name: "Pack 4 manicuras", Service: "Manicura semipermanente", Sessions: 4, PriceCents: 5760000, Validity: 120},
	{Name: "Bono 3 coloraciones", Service: "Color completo", Sessions: 3, PriceCents: 9750000, Validity: 240},
}

// demoExpense es un gasto operativo de los últimos 60 días.
type demoExpense struct {
	Category    string
	AmountCents int
	Description string
	// DaysAgo es la antigüedad del gasto.
	DaysAgo int
}

// demoExpenses son los gastos del período: alquiler, insumos, servicios y varios.
var demoExpenses = []demoExpense{
	{Category: "rent", AmountCents: 34000000, Description: "Alquiler del local", DaysAgo: 52},
	{Category: "rent", AmountCents: 34000000, Description: "Alquiler del local", DaysAgo: 22},
	{Category: "supplies", AmountCents: 7800000, Description: "Coloración y oxidantes", DaysAgo: 57},
	{Category: "supplies", AmountCents: 4200000, Description: "Shampoo y acondicionador de salón (bidón)", DaysAgo: 49},
	{Category: "supplies", AmountCents: 3150000, Description: "Esmaltes semipermanentes", DaysAgo: 44},
	{Category: "supplies", AmountCents: 2600000, Description: "Guantes, algodón y descartables", DaysAgo: 38},
	{Category: "utilities", AmountCents: 4200000, Description: "Luz", DaysAgo: 41},
	{Category: "utilities", AmountCents: 1800000, Description: "Agua", DaysAgo: 41},
	{Category: "utilities", AmountCents: 3200000, Description: "Internet y teléfono", DaysAgo: 33},
	{Category: "utilities", AmountCents: 4400000, Description: "Luz", DaysAgo: 11},
	{Category: "supplies", AmountCents: 6700000, Description: "Reposición de color", DaysAgo: 30},
	{Category: "supplies", AmountCents: 2900000, Description: "Toallas nuevas", DaysAgo: 25},
	{Category: "other", AmountCents: 1900000, Description: "Publicidad en redes", DaysAgo: 47},
	{Category: "other", AmountCents: 3400000, Description: "Sistema de turnos y web", DaysAgo: 44},
	{Category: "supplies", AmountCents: 5100000, Description: "Productos de estética", DaysAgo: 19},
	{Category: "other", AmountCents: 1500000, Description: "Cartelería y folletos", DaysAgo: 17},
	{Category: "supplies", AmountCents: 3300000, Description: "Limas, buffers y repsuestos de torno", DaysAgo: 14},
	{Category: "utilities", AmountCents: 2100000, Description: "Agua", DaysAgo: 11},
	{Category: "other", AmountCents: 2100000, Description: "Café y agua para clientas", DaysAgo: 9},
	{Category: "supplies", AmountCents: 4600000, Description: "Tinturas y decolorante", DaysAgo: 7},
	{Category: "other", AmountCents: 1200000, Description: "Regalos de fin de año para el equipo", DaysAgo: 5},
	{Category: "supplies", AmountCents: 2700000, Description: "Productos de barbería", DaysAgo: 4},
	{Category: "utilities", AmountCents: 3300000, Description: "Internet y teléfono", DaysAgo: 3},
	{Category: "supplies", AmountCents: 1850000, Description: "Cera y bandas de depilación", DaysAgo: 2},
	{Category: "other", AmountCents: 2500000, Description: "Mantenimiento del aire acondicionado", DaysAgo: 1},
}

// demoReview es una reseña posvisita ya enviada.
type demoReview struct {
	Rating  int
	Comment string
	Author  string
	// Hidden marca la reseña oculta (el panel la muestra aparte).
	Hidden bool
	// DaysAgo es la antigüedad de la reseña.
	DaysAgo int
}

// demoReviews son catorce reseñas: nueve de 5, cuatro de 4 y una de 3 (promedio
// 4,57), con una oculta a propósito.
var demoReviews = []demoReview{
	{Rating: 5, Comment: "Salí con el color exactamente como lo pedí. Valentina escucha de verdad.", Author: "Belén S.", DaysAgo: 54},
	{Rating: 5, Comment: "El mejor corte que me hice en años. Súper prolija y sin apuro.", Author: "Ignacio V.", DaysAgo: 47},
	{Rating: 4, Comment: "Muy lindo el lugar y la atención. Solo esperé diez minutos de más.", Author: "Agustina M.", DaysAgo: 42},
	{Rating: 5, Comment: "Marcos es un crack con la barba. Ya soy cliente fijo.", Author: "Lautaro N.", DaysAgo: 39},
	{Rating: 5, Comment: "La manicura me duró cuatro semanas impecable.", Author: "Ludmila R.", DaysAgo: 35},
	{Rating: 4, Comment: "Buen trabajo y buen precio. La sala de uñas podría ser más grande.", Author: "Karen D.", DaysAgo: 30},
	{Rating: 5, Comment: "Pedí un peinado para un casamiento y quedó espectacular.", Author: "Rocío S.", DaysAgo: 27},
	{Rating: 3, Comment: "El color quedó bien, pero tardaron bastante más de lo que me dijeron.", Author: "Florencia V.", DaysAgo: 24},
	{Rating: 5, Comment: "El facial me dejó la piel nueva. Súper recomendable.", Author: "Olivia M.", DaysAgo: 20},
	{Rating: 5, Comment: "Muy buena onda y el turno empezó puntual.", Author: "Ramiro C.", DaysAgo: 16},
	{Rating: 5, Comment: "Volví por el bono de cortes y ya lo estoy usando.", Author: "Matías F.", DaysAgo: 12},
	{Rating: 4, Comment: "Muy conforme con el brushing. Le falta un poco de música más tranquila.", Author: "Ximena C.", DaysAgo: 9},
	{Rating: 5, Comment: "Me hicieron un lugar a último momento y salí feliz.", Author: "Josefina P.", DaysAgo: 6},
	{Rating: 5, Comment: "Excelente atención de todo el equipo. El salón impecable.", Author: "Natalia V.", DaysAgo: 3},
	{Rating: 2, Comment: "Me cobraron un servicio que no pedí. Lo hablé y me lo devolvieron.", Author: "Inés R.", Hidden: true, DaysAgo: 14},
}

// demoWaitlist son las entradas de la lista de espera (las carga el público).
type demoWaitlist struct {
	Name    string
	Email   string
	Phone   string
	Service string
	// DayOffset: dentro de cuántos días quiere el turno.
	DayOffset int
}

// demoWaitlistEntries son cinco entradas, todas para los próximos días.
var demoWaitlistEntries = []demoWaitlist{
	{Name: "Malena Cáceres", Email: "malena.caceres@demo.test", Phone: "+54 9 11 5342-1192", Service: "Color completo", DayOffset: 1},
	{Name: "Tamara Quiroga", Email: "tamara.quiroga@demo.test", Phone: "+54 9 11 5342-1199", Service: "Manicura semipermanente", DayOffset: 2},
	{Name: "Kevin Almada", Email: "kevin.almada@demo.test", Phone: "+54 9 11 5342-1215", Service: "Corte de pelo", DayOffset: 2},
	{Name: "Sofía Aguirre", Email: "sofia.aguirre@demo.test", Phone: "+54 9 11 5342-1220", Service: "Facial hidratante", DayOffset: 3},
	{Name: "Guadalupe Ibarra", Email: "guadalupe.ibarra@demo.test", Phone: "+54 9 11 5342-1211", Service: "Pedicura", DayOffset: 4},
}

// demoNotifications son los avisos que ve la dueña al entrar.
type demoNotification struct {
	Title   string
	Body    string
	Kind    string
	DaysAgo int
	Unread  bool
}

var demoNotifications = []demoNotification{
	{Title: "Cierre de caja del día", Body: "La caja quedó cerrada con una diferencia de $0.", Kind: "cash", DaysAgo: 0, Unread: true},
	{Title: "Seña acreditada", Body: "Entró la seña de un turno de color para mañana.", Kind: "booking", DaysAgo: 0, Unread: true},
	{Title: "Venta de bono", Body: "Lucía vendió el Bono 5 cortes a una clienta.", Kind: "sale", DaysAgo: 1, Unread: true},
	{Title: "Stock bajo", Body: "El esmalte nude y el kit de uñas están por debajo del mínimo.", Kind: "info", DaysAgo: 1},
	{Title: "Reseña nueva", Body: "Una clienta dejó una reseña de 5 estrellas.", Kind: "info", DaysAgo: 2},
	{Title: "Lista de espera", Body: "Hay cinco personas esperando un turno esta semana.", Kind: "booking", DaysAgo: 2},
	{Title: "Gastos cargados", Body: "Se registraron los gastos de insumos del mes.", Kind: "info", DaysAgo: 4},
	{Title: "Liquidación pagada", Body: "Se pagó la liquidación del mes pasado al equipo.", Kind: "info", DaysAgo: 5},
}
