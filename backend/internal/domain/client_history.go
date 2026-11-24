// Historial de la clienta (F5, D6/D7): las visitas, los conteos, el gasto y el
// resumen con el día LOCAL del salón. Es dominio PURO: no sabe de SQL ni de
// HTTP; el adaptador le entrega una foto de los datos y acá solo se arma lo que
// ve la pantalla.
package domain

import "time"

// MaxTextAllergies es el tope de las alergias de una ficha (D4): 500 caracteres.
// Es un dato de salud, así que vive en su propia columna y su propio límite.
const MaxTextAllergies = 500

// Mensajes exactos del contrato F5 (D4/D5/D6 y D3-c). Se escriben acá porque
// son parte del contrato con el frontend y valen para cualquier puerta.
const (
	MsgClientFieldsRequired   = "Faltan datos de la ficha: nombre, teléfono, email, notas y alergias"
	MsgClientNotFound         = "Ficha no encontrada"
	MsgClientAllergiesTooLong = "Las alergias no pueden superar los 500 caracteres"
	MsgClientInvalidChars     = "La ficha tiene caracteres no válidos"
	MsgSaleClientInvalid      = "Ficha de clienta inválida"
)

// ClientHistoryBooking es un turno del historial: lo justo para la lista de
// «turnos recientes» (más reciente primero).
type ClientHistoryBooking struct {
	ID               string
	StartsAt         time.Time
	Status           BookingStatus
	ServiceName      string
	ProfessionalName string
}

// ClientHistorySale es una venta VINCULADA a la ficha. El gasto histórico
// anterior a F5 no se atribuye (D2): sin client_id no hay fila acá.
type ClientHistorySale struct {
	ID         string
	CreatedAt  time.Time
	TotalCents int
	Method     PaymentMethod
}

// ClientSpend es el dinero de la ficha: SOLO las ventas vinculadas (D6). Lo ve
// quien puede ver la plata; para un profesional no se serializa.
type ClientSpend struct {
	SalesCount int
	TotalCents int
}

// ClientHistoryData es la FOTO que entrega la lectura del historial (una sola
// transacción REPEATABLE READ READ ONLY en el adaptador): conteos del cliente,
// el último turno hecho, el próximo agendado y las listas acotadas.
type ClientHistoryData struct {
	Visits    int
	NoShows   int
	Cancelled int
	Upcoming  int
	// LastDoneAt es el starts_at del turno `done` más reciente (nil = ninguno).
	LastDoneAt *time.Time
	// NextBookedAt es el próximo `booked` futuro (nil = ninguno).
	NextBookedAt *time.Time
	Bookings     []ClientHistoryBooking
	Spend        ClientSpend
	Sales        []ClientHistorySale
}

// ClientHistorySummary es el resumen del contrato: los conteos, la última visita
// como DÍA LOCAL (YYYY-MM-DD, "" = nunca), la próxima visita y la zona del salón.
type ClientHistorySummary struct {
	Visits    int
	NoShows   int
	Cancelled int
	Upcoming  int
	LastVisit string
	NextVisit *time.Time
	Timezone  string
}

// NewClientHistorySummary arma el resumen con la zona del SALÓN: «última visita»
// es el día local, nunca el día UTC (D7). El día sin hora se escribe con el
// layout YYYY-MM-DD.
func NewClientHistorySummary(d ClientHistoryData, loc *time.Location) ClientHistorySummary {
	if loc == nil {
		loc = time.UTC
	}
	sum := ClientHistorySummary{
		Visits:    d.Visits,
		NoShows:   d.NoShows,
		Cancelled: d.Cancelled,
		Upcoming:  d.Upcoming,
		Timezone:  loc.String(),
	}
	if d.LastDoneAt != nil {
		sum.LastVisit = d.LastDoneAt.In(loc).Format(dateLayout)
	}
	if d.NextBookedAt != nil {
		next := d.NextBookedAt.UTC()
		sum.NextVisit = &next
	}
	return sum
}
