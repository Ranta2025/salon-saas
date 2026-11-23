// PayrollService: la vista previa de la nómina de un período y «Mis comisiones»
// (F3.2 del contrato F3). El período lo deriva SIEMPRE el servidor en la zona
// del salón (D5) y la comisión sale de la fórmula de D2, que la hace el dominio:
// el adaptador solo suma qty × precio × pct en enteros y acá se divide una vez.
package app

import (
	"sort"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// PayrollService junta el modelo de pago, los agregados de ventas y la zona
// horaria del salón. No escribe nada: la liquidación es F3.3.
type PayrollService struct {
	Pay         ports.MemberPayRepository
	Sales       ports.PayrollSalesRepository
	Memberships ports.MembershipRepository
	// Settings da la zona horaria del salón: el período es local del salón, nunca
	// UTC. nil = configuración de fábrica (zona por defecto).
	Settings ports.SalonSettingsRepository
	// Now es el reloj de «hoy» para decidir si el período cerró. nil = time.Now.
	Now func() time.Time
}

// PayrollLine es una línea de la vista previa: lo que se le va a pagar a UNA
// persona en el período.
type PayrollLine struct {
	UserID          string
	Name            string
	Active          bool
	SalesCount      int
	SalesTotalCents int
	CommissionCents int
	FixedCents      int
	TotalCents      int
	// Estimated marca que al menos una línea de sus ventas no tenía foto y se
	// estimó con la tarifa actual del servicio (D1).
	Estimated bool
}

// PayrollPreview es la respuesta de POST /salons/:id/payroll/preview.
type PayrollPreview struct {
	Period          domain.PayPeriod
	Closed          bool
	Lines           []PayrollLine
	CommissionCents int
	FixedCents      int
	TotalCents      int
}

// PayrollMe es la respuesta de GET /salons/:id/payroll/me: SOLO lo del que llama.
type PayrollMe struct {
	Period          domain.PayPeriod
	Closed          bool
	SalesCount      int
	SalesTotalCents int
	CommissionCents int
	Estimated       bool
}

// Preview arma la vista previa del período que contiene `date`. Solo owner y
// administración ven la nómina (D7): recepción, profesional y quien no es del
// salón reciben 403.
//
// La línea de cada persona es: comisión de sus ventas en el período (0 si su
// modelo la tiene apagada) + el fijo, que se paga UNA vez y solo si el tipo
// liquidado es el suyo y la persona sigue activa (D4). Se listan quienes tienen
// al menos una venta en el período o un fijo que aplicar; los totales son la
// suma de las líneas y el orden es por total descendente y después por nombre.
func (s PayrollService) Preview(actor domain.Membership, salonID, kind, date string) (PayrollPreview, error) {
	if _, err := manageActor(s.Memberships, actor, salonID); err != nil {
		return PayrollPreview{}, err
	}
	p, from, to, err := s.period(salonID, kind, date)
	if err != nil {
		return PayrollPreview{}, err
	}
	members, err := s.Pay.ListPayModels(salonID)
	if err != nil {
		return PayrollPreview{}, err
	}
	totals, err := s.Sales.CommissionTotals(salonID, from, to)
	if err != nil {
		return PayrollPreview{}, err
	}
	sold := make(map[string]domain.PayrollSalesTotal, len(totals))
	for _, t := range totals {
		sold[t.UserID] = t
	}

	lines := make([]PayrollLine, 0, len(members))
	seen := make(map[string]bool, len(members))
	for _, m := range members {
		seen[m.UserID] = true
		if line, ok := payrollLine(m, sold[m.UserID], p.Kind); ok {
			lines = append(lines, line)
		}
	}
	// Un profesional con ventas que ya no está en el equipo (o cuya membresía no
	// existe) aparece igual: la comisión de lo que vendió no se pierde. Sin fila
	// de pago rige el default de D3 y el fijo es 0.
	for _, t := range totals {
		if seen[t.UserID] {
			continue
		}
		model := domain.DefaultMemberPay(salonID, t.UserID)
		view := domain.MemberPayView{MemberPay: model, Name: t.Name, Active: false}
		if line, ok := payrollLine(view, t, p.Kind); ok {
			lines = append(lines, line)
		}
	}

	sortPayrollLines(lines)
	out := PayrollPreview{Period: p, Closed: p.Closed(s.now()), Lines: lines}
	for _, l := range lines {
		out.CommissionCents += l.CommissionCents
		out.FixedCents += l.FixedCents
		out.TotalCents += l.TotalCents
	}
	return out, nil
}

// payrollLine arma la línea de una persona, o ok=false si no corresponde
// listarla (no vendió y no tiene un fijo que aplicar).
func payrollLine(m domain.MemberPayView, t domain.PayrollSalesTotal, kind domain.PayPeriodKind) (PayrollLine, bool) {
	line := PayrollLine{
		UserID: m.UserID, Name: m.Name, Active: m.Active,
		SalesCount: t.SalesCount, SalesTotalCents: t.SalesTotalCents, Estimated: t.Estimated,
	}
	// Comisión apagada (fijo puro): no cobra comisión, pero sus ventas siguen
	// contando en cantidad y total.
	if m.CommissionEnabled {
		line.CommissionCents = t.CommissionCents()
	}
	if m.Active && m.FixedCents > 0 && m.FixedPeriod == kind {
		line.FixedCents = m.FixedCents
	}
	line.TotalCents = line.CommissionCents + line.FixedCents
	if line.SalesCount == 0 && line.FixedCents == 0 {
		return PayrollLine{}, false
	}
	return line, true
}

// sortPayrollLines ordena por total descendente y después por nombre (el id
// desempata para que dos nombres iguales no bailen entre requests).
func sortPayrollLines(lines []PayrollLine) {
	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].TotalCents != lines[j].TotalCents {
			return lines[i].TotalCents > lines[j].TotalCents
		}
		if lines[i].Name != lines[j].Name {
			return lines[i].Name < lines[j].Name
		}
		return lines[i].UserID < lines[j].UserID
	})
}

// Me devuelve las comisiones del que llama (owner, admin o profesional activo).
// Recepción no ve ni las suyas (D7) y un miembro dado de baja no entra: para
// esta ruta no está (403).
//
// La lectura es de UNA persona (CommissionTotalFor): la respuesta nunca se arma
// leyendo los números de los demás, así no hay forma de que se filtren.
func (s PayrollService) Me(actor domain.Membership, salonID, kind, date string) (PayrollMe, error) {
	m, err := s.member(actor, salonID)
	if err != nil {
		return PayrollMe{}, err
	}
	switch m.Role {
	case domain.RoleOwner, domain.RoleAdmin, domain.RoleProfessional:
	default:
		return PayrollMe{}, domain.ErrForbidden
	}
	p, from, to, err := s.period(salonID, kind, date)
	if err != nil {
		return PayrollMe{}, err
	}
	total, err := s.Sales.CommissionTotalFor(salonID, m.UserID, from, to)
	if err != nil {
		return PayrollMe{}, err
	}
	model, err := s.Pay.FindPayModel(salonID, m.UserID)
	if err != nil {
		return PayrollMe{}, err
	}
	commission := 0
	if model.CommissionEnabled {
		commission = total.CommissionCents()
	}
	return PayrollMe{
		Period: p, Closed: p.Closed(s.now()),
		SalesCount: total.SalesCount, SalesTotalCents: total.SalesTotalCents,
		CommissionCents: commission, Estimated: total.Estimated,
	}, nil
}

// period deriva el período en la zona del salón y devuelve su ventana de
// instantes [from, to). La zona sale SIEMPRE de la configuración del salón: un
// salón de CDMX liquida su marzo local, no el marzo UTC.
func (s PayrollService) period(salonID, kind, date string) (domain.PayPeriod, time.Time, time.Time, error) {
	settings, err := loadSalonSettings(s.Settings, salonID)
	if err != nil {
		return domain.PayPeriod{}, time.Time{}, time.Time{}, err
	}
	loc, err := settings.Location()
	if err != nil {
		return domain.PayPeriod{}, time.Time{}, time.Time{}, err
	}
	p, err := domain.NewPayPeriod(kind, date, loc)
	if err != nil {
		return domain.PayPeriod{}, time.Time{}, time.Time{}, err
	}
	from, to := p.Window()
	return p, from, to, nil
}

// member exige una membresía ACTIVA en ese salón (una baja no ve nada).
func (s PayrollService) member(actor domain.Membership, salonID string) (domain.Membership, error) {
	if actor.SalonID != salonID {
		return domain.Membership{}, domain.ErrForbidden
	}
	m, err := s.Memberships.Find(actor.UserID, salonID)
	if err != nil {
		return domain.Membership{}, domain.ErrForbidden
	}
	return m, nil
}

// now es el reloj del período (los tests lo fijan).
func (s PayrollService) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}
