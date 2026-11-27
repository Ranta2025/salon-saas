// AuditService: registro de auditoría (F10, parte A del contrato de la fase 6).
// Decisiones D1..D3:
//
//   - El registro lo escribe un MIDDLEWARE de HTTP (`Audit(action)`) después de
//     una respuesta 2xx: ningún servicio ni handler existente se toca. Escribir
//     es de MEJOR ESFUERZO: este servicio devuelve el error y el middleware lo
//     loguea y lo traga (limitación conocida: no es transaccional).
//   - Guarda solo quién (id + una FOTO del nombre), qué acción, sobre qué id y
//     cuándo: sin valores antes/después ni datos personales.
//   - Leerlo: SOLO owner/admin (CanManage), y el permiso va ANTES de mirar los
//     parámetros o tocar la base. Toda consulta va scopeada por salon_id.
package audit

import (
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// AuditPage es una página del registro. NextCursor vacío = última página.
type AuditPage struct {
	Events     []domain.AuditEvent
	NextCursor string
}

type AuditService struct {
	// Events es el registro. nil = auditoría apagada: Record no hace nada (así los
	// routers de los tests unitarios que no la montan siguen igual).
	Events ports.AuditRepository
	// Users resuelve el nombre del actor para la foto. Si falla, el evento se
	// registra con el nombre vacío.
	Users ports.UserRepository
	// Memberships valida al actor contra la base (no confía en el rol del token).
	Memberships ports.MembershipRepository
	// Now es el reloj (nil = time.Now); los tests lo fijan.
	Now func() time.Time
}

func (s AuditService) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}

// Enabled dice si hay dónde escribir.
func (s AuditService) Enabled() bool { return s.Events != nil }

// Record escribe un evento: `actorUserID` hizo `action` en `salonID` sobre
// `entityID` (vacío = la acción no tiene id propio). El nombre del actor sale
// del repositorio de usuarios; si no se puede resolver queda vacío y NUNCA hace
// fallar el registro. Un evento inválido o un fallo del repositorio vuelve como
// error para que el llamador lo loguee: el middleware lo traga.
func (s AuditService) Record(salonID, actorUserID, action, entityID string) error {
	if s.Events == nil {
		return nil
	}
	name := ""
	if s.Users != nil && actorUserID != "" {
		if u, err := s.Users.FindByID(actorUserID); err == nil {
			name = u.Name
		}
	}
	ev, err := domain.NewAuditEvent(salonID, actorUserID, name, action, entityID, s.now())
	if err != nil {
		return err
	}
	return s.Events.Append(ev)
}

// RequireViewer es el permiso SOLO (sin tocar datos): el handler lo llama ANTES
// de leer los parámetros. Membresía ACTIVA del salón y rol owner/admin; si no,
// 403.
func (s AuditService) RequireViewer(actor domain.Membership, salonID string) error {
	if actor.SalonID != salonID {
		return domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.ErrForbidden
	}
	if !m.CanManage() {
		return domain.ErrForbidden
	}
	return nil
}

// List devuelve una página del registro del salón, de la más nueva a la más
// vieja. Los parámetros llegan como texto (vacío = no se pasó) y se validan
// DESPUÉS del permiso: límite (1..100, por defecto 50), cursor opaco y filtro
// de acción. Pide limit+1 filas para saber si hay otra página sin un COUNT.
func (s AuditService) List(actor domain.Membership, salonID, limit, before, action string) (AuditPage, error) {
	if err := s.RequireViewer(actor, salonID); err != nil {
		return AuditPage{}, err
	}
	q, err := domain.ParseAuditQuery(limit, before, action)
	if err != nil {
		return AuditPage{}, err
	}
	page := q.Limit
	q.Limit = page + 1
	rows, err := s.Events.List(salonID, q)
	if err != nil {
		return AuditPage{}, err
	}
	out := AuditPage{Events: []domain.AuditEvent{}}
	if len(rows) > page {
		rows = rows[:page]
		last := rows[len(rows)-1]
		out.NextCursor = domain.EncodeAuditCursor(domain.AuditCursor{CreatedAt: last.CreatedAt, ID: last.ID})
	}
	out.Events = append(out.Events, rows...)
	return out, nil
}
