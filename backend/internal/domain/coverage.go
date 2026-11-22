package domain

import (
	"strings"
	"unicode/utf8"
)

// Cubrir una ausencia (F2d3): cuando una profesional falta, el salón reasigna sus
// turnos a otra que pueda tomarlos. La decisión es MANUAL (elegir quién cubre es
// del salón), así que el dominio no elige: dice si un candidato puede y, si no,
// con qué mensaje del contrato se lo explica a la persona.
//
// La garantía de que no haya doble turno NO está acá: la EXCLUDE de la tabla
// bookings (migración 002) es la que decide, y su 23P01 se traduce a 409
// (S-F2d3-1). Esto de acá es la regla de negocio y el filtro amable.
const (
	// MsgCoverNotBookable: el destino no atiende clientes (F2a): recepción,
	// cliente, dueño/administración sin la marca o alguien de otro salón (que en
	// este salón no es nadie).
	MsgCoverNotBookable = "Esa persona no atiende clientes"
	// MsgCoverNotScheduled: no está de turno ese día (F2d1: descanso semanal, día
	// libre, salón cerrado) o está ausente (F2d2).
	MsgCoverNotScheduled = "Esa persona no está de turno ese día"
	// MsgCoverWrongService: el turno tiene servicio y esa persona no lo hace
	// (F2c).
	MsgCoverWrongService = "Esa persona no hace ese servicio"
	// MsgCoverNotThisAbsence: el turno es de otra persona o no cae dentro de la
	// ausencia.
	MsgCoverNotThisAbsence = "Ese turno no es de esa ausencia"
	// MsgCoverNotBooked: el turno ya se canceló, se atendió o la clienta no vino:
	// liberó su hueco y no se reasigna.
	MsgCoverNotBooked = "El turno ya no está reservado"
	// MsgCoverNoCandidate: el pedido no nombra a nadie a quien reasignar.
	MsgCoverNoCandidate = "No hay a quién reasignar"
	// MsgCoverBusy: la candidata ya tiene un turno en ese horario (la EXCLUDE
	// lo rechaza: 409). Es el motivo que la dueña lee al lado del nombre, así
	// que dice QUÉ le pasa a esa persona, no un "no hay nadie".
	MsgCoverBusy = "Ya tiene un turno a esa hora"
)

// CoverageCandidate son los HECHOS que la regla necesita de una persona para un
// turno concreto. El caso de uso los arma (los lee de la membresía, de sus
// servicios, de su horario y de sus ausencias); el dominio solo decide.
type CoverageCandidate struct {
	// Professional es la persona tal como la ve la agenda. Un cero (o alguien de
	// otro salón, que no se puede ni cargar) no atiende acá.
	Professional StaffMember
	// DoesService dice si hace el servicio DEL TURNO. Irrelevante si el turno no
	// tiene servicio (S-F2d3-3).
	DoesService bool
	// Scheduled dice si el turno entra entero en su ventana efectiva de ese día
	// (F2d1: ventana del salón ∩ sus tramos, sin cruzar descansos).
	Scheduled bool
	// Away dice si una ausencia suya pisa el horario del turno (F2d2).
	Away bool
	// Busy dice si ya tiene otro turno reservado en ese horario. Lo usa el
	// display del selector; en la escritura el árbitro es la EXCLUDE.
	Busy bool
}

// Covers devuelve nil si esta persona puede tomar el turno, o el error de dominio
// con el mensaje exacto del contrato. El orden de los chequeos es el orden en
// que la persona los entiende: primero quién es, después si está, después si
// sabe hacerlo y al final si puede en ese horario.
func (c CoverageCandidate) Covers(b Booking) error {
	if !c.Professional.Bookable() {
		return NewMessage(ErrValidation, MsgCoverNotBookable)
	}
	if c.Away || !c.Scheduled {
		return NewMessage(ErrValidation, MsgCoverNotScheduled)
	}
	if b.ServiceID != "" && !c.DoesService {
		return NewMessage(ErrValidation, MsgCoverWrongService)
	}
	if c.Busy {
		return NewMessage(ErrAlreadyExists, MsgCoverBusy)
	}
	return nil
}

// Unavailable es el motivo para MOSTRAR en el selector de candidatas ("" =
// puede tomarlo). Solo cubre lo que no depende del turno: el servicio se evalúa
// con Covers, que sí lo mira.
func (c CoverageCandidate) Unavailable() string {
	if !c.Professional.Bookable() {
		return MsgCoverNotBookable
	}
	if c.Away || !c.Scheduled {
		return MsgCoverNotScheduled
	}
	if c.Busy {
		return MsgCoverBusy
	}
	return ""
}

// BelongsToTimeOff dice si el turno es de la persona de la ausencia y cae dentro
// del período: es el scope del "Reasignar todo" (S-F2d3-2). Se pisa con el mismo
// criterio semiabierto que todo lo demás.
func (b Booking) BelongsToTimeOff(off TimeOff) bool {
	if b.ProfessionalID != off.UserID {
		return false
	}
	return off.Overlaps(b.StartsAt, b.EndsAt)
}

// Coverable dice si el turno todavía ocupa un hueco. Un turno cerrado (cancelado,
// atendido o no-show) no se reasigna: su hueco ya está libre y mover el estado
// sería reabrir algo cerrado (S-F2d3-3).
func (b Booking) Coverable() error {
	if b.Status != BookingBooked {
		return NewMessage(ErrAlreadyExists, MsgCoverNotBooked)
	}
	return nil
}

// Reassign devuelve el turno con otra profesional y el MISMO horario, cliente y
// estado. No usa Reschedule a propósito: cubrir una ausencia no mueve la hora
// (la clienta la conserva, S-F2d3-4) y un turno en curso tiene que poder
// cubrirse, cosa que la validación de "no agendar en el pasado" impediría.
//
// S-F2d3-6: también deja escrito quién TENÍA el turno. Si CoveredFrom está
// vacío se guarda la profesional anterior; si ya tenía valor se conserva (Ana ->
// Cata -> Bea sigue diciendo Ana); si el turno vuelve a quien figura ahí, se
// limpia. Reasignar a la misma persona no es una cobertura y no toca nada.
func (b Booking) Reassign(professionalID string) (Booking, error) {
	p := strings.TrimSpace(professionalID)
	if p == "" {
		return Booking{}, NewMessage(ErrValidation, MsgCoverNoCandidate)
	}
	if p == b.ProfessionalID {
		return b, nil
	}
	switch b.CoveredFrom {
	case "":
		b.CoveredFrom = b.ProfessionalID
	case p:
		// Vuelve a quien lo tenía: ya no lo cubre nadie.
		b.CoveredFrom = ""
	}
	b.ProfessionalID = p
	return b, nil
}

// NormalizeCoverReason recorta el motivo de una cancelación y lo acota: mismo
// criterio que el motivo de una ausencia (una línea, máximo 120 caracteres). El
// motivo se guarda en el aviso interno; la clienta no lo recibe.
func NormalizeCoverReason(reason string) (string, error) {
	// El salto se busca en el texto CRUDO: recortar primero se llevaría el salto
	// final y lo dejaría pasar.
	if strings.ContainsAny(reason, "\r\n") {
		return "", NewMessage(ErrValidation, "El motivo no puede tener saltos de línea")
	}
	clean := strings.TrimSpace(reason)
	if utf8.RuneCountInString(clean) > MaxTextTimeOffReason {
		return "", NewMessage(ErrValidation, "El motivo no puede superar los 120 caracteres")
	}
	return clean, nil
}
