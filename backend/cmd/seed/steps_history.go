package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"
)

// pastBooking es un turno del historial: lo que necesitan las ventas, la caja,
// la fidelidad y las reseñas.
type pastBooking struct {
	ID          string
	ClientID    string
	ClientName  string
	ClientPhone string
	ProUserID   string
	ProName     string
	ServiceID   string
	ServiceName string
	PriceCents  int
	StartsAt    time.Time
	Status      string
}

// stepFutureBookings arma la agenda de HOY y de los próximos catorce días. Los primeros
// turnos entran por la reserva PÚBLICA (la que valida horarios y crea la seña en
// el banco simulado); el resto, por SQL con estado `booked` para no gastar el
// límite por IP de las escrituras sin sesión con treinta pedidos seguidos.
func (s *Seed) stepFutureBookings(ctx context.Context) error {
	candidates, err := s.futureCandidates(14)
	if err != nil {
		return err
	}
	cursor := 0

	// Fase 1: política apagada. Turnos públicos sin seña, con email para que la
	// confirmación quede en Mailpit.
	if err := s.setDepositPolicy(ctx, false); err != nil {
		return err
	}
	publicWithoutDeposit := 5
	created, err := s.createPublicBookings(ctx, candidates, &cursor, publicWithoutDeposit)
	if err != nil {
		return err
	}

	// Fase 2: política encendida (30 % con 30 minutos de retención). Los turnos
	// de acá en adelante crean su cargo en el banco simulado.
	if err := s.setDepositPolicy(ctx, true); err != nil {
		return err
	}
	depositPlans := []struct {
		card string
		pay  bool
	}{
		{"4242424242424242", true}, // aprobada
		{"4242424242424242", true}, // aprobada
		{"4242424242424242", true}, // aprobada
		{"4000000000000002", true}, // rechazada: queda pendiente con un intento
		{"", false},                // sin pagar: queda pendiente
	}
	for i, plan := range depositPlans {
		bookings, err := s.createPublicBookings(ctx, candidates, &cursor, 1)
		if err != nil {
			return err
		}
		if bookings == 0 {
			return fmt.Errorf("seed: no encontré un hueco libre para el turno con seña %d", i+1)
		}
		created += bookings
		if !plan.pay {
			continue
		}
		if err := s.payLastDeposit(ctx, plan.card); err != nil {
			s.log.Printf("aviso: no pude cobrar una seña con la tarjeta de prueba: %v", err)
		}
	}

	// Fase 3: el resto de la agenda por SQL (estado `booked`). Son turnos con
	// service_id y clienta, sin seña: el calendario queda lleno sin depender del
	// límite de la reserva pública.
	return s.createUpcomingSQL(ctx, candidates, &cursor, 25-created)
}

// setDepositPolicy guarda la política de señas.
func (s *Seed) setDepositPolicy(ctx context.Context, enabled bool) error {
	return s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/deposit-policy", map[string]any{
		"enabled": enabled, "percent": 30, "hold_minutes": 30, "refund_hours": 24,
	}, nil)
}

// bookingCandidate es un hueco concreto: quién, qué servicio y cuándo.
type bookingCandidate struct {
	pro   staffRef
	svc   serviceRef
	start time.Time
}

// futureCandidates arma la lista de huecos posibles desde HOY hasta dentro de
// `days` días, ya despegados por profesional (sin solapes), en orden
// cronológico. Incluye hoy a propósito: si el seed corre temprano, la agenda de
// hoy queda con turnos por delante y el panel no arranca vacío.
func (s *Seed) futureCandidates(days int) ([]bookingCandidate, error) {
	var out []bookingCandidate
	for offset := 0; offset <= days; offset++ {
		day := s.clk.daysAhead(offset)
		wd := weekday(day)
		for _, pro := range s.staff {
			segments := pro.segments[wd]
			if len(segments) == 0 {
				continue
			}
			svc := s.serviceOf(pro, pick(s.r, pro.Services))
			if svc.Minutes == 0 {
				continue
			}
			// Dos huecos por profesional y día, en tramos distintos si se puede.
			for slot := 0; slot < 2; slot++ {
				seg := segments[slot%len(segments)]
				start := s.roundSlot(day, seg.Start, seg.End, svc.Minutes)
				if start.IsZero() {
					continue
				}
				out = append(out, bookingCandidate{pro: pro, svc: svc, start: start})
			}
		}
	}
	// Sin solapes por profesional: si dos huecos del mismo día se pisan, queda el
	// primero (los turnos son de 30 a 120 minutos). Se arma una lista nueva: usar
	// out[:0] pisaría las entradas que faltan leer.
	kept := make([]bookingCandidate, 0, len(out))
	for _, cand := range out {
		overlap := false
		for _, previous := range kept {
			if previous.pro.UserID != cand.pro.UserID {
				continue
			}
			if cand.start.Before(addMinutes(previous.start, previous.svc.Minutes)) && previous.start.Before(addMinutes(cand.start, cand.svc.Minutes)) {
				overlap = true
				break
			}
		}
		if !overlap {
			kept = append(kept, cand)
		}
	}
	return kept, nil
}

// roundSlot elige un comienzo alineado a 15 minutos dentro del tramo, dejando
// lugar para toda la duración del servicio.
func (s *Seed) roundSlot(day time.Time, open, close string, minutes int) time.Time {
	startMin := hourOf(open)*60 + minuteOf(open)
	endMin := hourOf(close)*60 + minuteOf(close)
	lastStart := endMin - minutes
	if lastStart < startMin {
		return time.Time{}
	}
	steps := (lastStart-startMin)/15 + 1
	pick := startMin + s.r.Intn(steps)*15
	return s.clk.at(day, pick/60, pick%60)
}

// serviceOf busca el servicio por nombre dentro de los que hace la profesional.
func (s *Seed) serviceOf(pro staffRef, name string) serviceRef {
	if svc, ok := s.services[name]; ok {
		return svc
	}
	for _, candidate := range pro.Services {
		if svc, ok := s.services[candidate]; ok {
			return svc
		}
	}
	return serviceRef{}
}

// createPublicBookings crea hasta `want` turnos por la reserva pública y
// devuelve cuántos creó (un hueco que el servidor rechaza no corta la corrida).
// El cursor avanza SIEMPRE, así no se reintenta el mismo hueco.
func (s *Seed) createPublicBookings(ctx context.Context, candidates []bookingCandidate, cursor *int, want int) (int, error) {
	created := 0
	now := s.clk.now
	for *cursor < len(candidates) {
		if created >= want {
			break
		}
		cand := candidates[*cursor]
		*cursor++
		if cand.start.Before(now.Add(45 * time.Minute)) {
			continue
		}
		if s.publicBookingExists(ctx, cand) {
			continue
		}
		client := s.pickClient()
		body := map[string]any{
			"service_id":      cand.svc.ID,
			"professional_id": cand.pro.UserID,
			"client_name":     client.Name,
			"client_phone":    client.Phone,
			"client_email":    client.Email,
			"starts_at":       cand.start.Format(timeRFC3339),
			"client_op_id":    "seed-" + cand.svc.ID[len(cand.svc.ID)-6:] + "-" + cand.start.Format("20060102T1504"),
		}
		res, err := s.api.do(ctx, http.MethodPost, "/public/salons/"+s.salonSlug+"/bookings", body)
		if err != nil {
			return created, err
		}
		if !res.ok() {
			s.log.Printf("aviso: la reserva pública de %s con %s el %s dio %d: %s",
				cand.pro.Name, cand.svc.Name, cand.start.Format("02/01 15:04"), res.status, res.errorText())
			continue
		}
		created++
	}
	if created < want {
		s.log.Printf("aviso: pedí %d turnos públicos y pude crear %d", want, created)
	}
	return created, nil
}

// createUpcomingSQL inserta los turnos futuros que faltan directo en la agenda
// (estado `booked`, con service_id y clienta). Respeta la EXCLUDE de Postgres:
// antes de insertar mira que la profesional no tenga otro turno en ese rango.
func (s *Seed) createUpcomingSQL(ctx context.Context, candidates []bookingCandidate, cursor *int, want int) error {
	created := 0
	seq := 0
	for *cursor < len(candidates) {
		if created >= want {
			break
		}
		cand := candidates[*cursor]
		*cursor++
		if cand.start.Before(s.clk.now.Add(30 * time.Minute)) {
			continue
		}
		ends := addMinutes(cand.start, cand.svc.Minutes)
		busy, err := s.db.count(ctx, "bookings",
			`salon_id=$1 AND professional_id=$2 AND status='booked' AND tstzrange(starts_at,ends_at) && tstzrange($3,$4)`,
			s.salonID, cand.pro.UserID, cand.start, ends)
		if err != nil {
			return err
		}
		if busy > 0 {
			continue
		}
		seq++
		client := s.pickClient()
		createdAt := s.clk.now.AddDate(0, 0, -s.r.Between(1, 6))
		if _, err := s.db.exec(ctx,
			`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,
			   status,created_at,client_op_id,service_id,client_id)
			 VALUES($1,$2,$3,$4,$5,$6,$7,'booked',$8,'',$9,$10) ON CONFLICT (id) DO NOTHING`,
			seedID("up", seq), s.salonID, cand.pro.UserID, client.Name, client.Phone,
			cand.start, ends, createdAt, cand.svc.ID, client.ID,
		); err != nil {
			return err
		}
		created++
	}
	if created < want {
		s.log.Printf("aviso: pedí %d turnos futuros por SQL y creé %d", want, created)
	}
	return nil
}

// publicBookingExists dice si ya hay un turno de esa profesional a esa hora (la
// reserva pública no es idempotente: el guardado es nuestro).
func (s *Seed) publicBookingExists(ctx context.Context, cand bookingCandidate) bool {
	n, err := s.db.count(ctx, "bookings",
		"salon_id=$1 AND professional_id=$2 AND starts_at=$3", s.salonID, cand.pro.UserID, cand.start)
	return err == nil && n > 0
}

// payLastDeposit cobra la última seña creada con la tarjeta de prueba.
func (s *Seed) payLastDeposit(ctx context.Context, card string) error {
	chargeID, err := s.lastDepositCharge(ctx, "pending")
	if err != nil {
		return err
	}
	if chargeID == "" {
		return fmt.Errorf("no encontré la seña pendiente recién creada")
	}
	_, err = s.api.do(ctx, http.MethodPost, "/simbank/charges/pay", map[string]any{
		"charge_id": chargeID,
		"card":      map[string]string{"number": card, "exp": "12/30", "cvc": "123", "name": "Demo Aurelia"},
	})
	return err
}

// lastDepositCharge devuelve el charge_id de la seña más nueva en ese estado.
func (s *Seed) lastDepositCharge(ctx context.Context, status string) (string, error) {
	var charge string
	row := s.db.pool.QueryRow(ctx,
		`SELECT charge_id FROM booking_deposits WHERE salon_id=$1 AND status=$2
		 ORDER BY created_at DESC LIMIT 1`, s.salonID, status)
	if err := row.Scan(&charge); err != nil {
		return "", nil
	}
	return charge, nil
}

// stepPastBookings crea el historial de los últimos 60 días por SQL: la API no
// deja crear turnos con fecha pasada ni con estado `done`.
func (s *Seed) stepPastBookings(ctx context.Context) error {
	s.past = s.past[:0]
	seq := 0
	now := s.clk.now
	for offset := 0; offset < 60; offset++ {
		day := s.clk.daysAgo(offset)
		if weekday(day) == 7 {
			continue // el salón no abre los domingos
		}
		for i, perDay := 0, s.bookingsPerDay(weekday(day)); i < perDay; i++ {
			seq++
			pro := pick(s.r, s.staff)
			svc := s.serviceOf(pro, pick(s.r, pro.Services))
			if svc.Minutes == 0 {
				continue
			}
			start := s.pastStart(day, pro, svc.Minutes)
			if start.IsZero() || start.Add(time.Duration(svc.Minutes)*time.Minute).After(now) {
				continue
			}
			client := s.pickClient()
			status := "done"
			switch roll := s.r.Intn(100); {
			case roll >= 95:
				status = "cancelled"
			case roll >= 88:
				status = "no_show"
			}
			id := seedID("bk", seq)
			created := start.AddDate(0, 0, -s.r.Between(2, 12))
			if _, err := s.db.exec(ctx,
				`INSERT INTO bookings(id,salon_id,professional_id,client_name,client_phone,starts_at,ends_at,
				   status,created_at,client_op_id,service_id,client_id)
				 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,'',$10,$11) ON CONFLICT (id) DO NOTHING`,
				id, s.salonID, pro.UserID, client.Name, client.Phone, start,
				start.Add(time.Duration(svc.Minutes)*time.Minute), status, created, svc.ID, client.ID,
			); err != nil {
				return err
			}
			s.past = append(s.past, pastBooking{
				ID: id, ClientID: client.ID, ClientName: client.Name, ClientPhone: client.Phone,
				ProUserID: pro.UserID, ProName: pro.Name, ServiceID: svc.ID, ServiceName: svc.Name,
				PriceCents: svc.PriceCents, StartsAt: start, Status: status,
			})
		}
	}
	// Un par de turnos cancelados desde el enlace firmado: el historial queda
	// con enlaces usados y sin turnos futuros colgados.
	return nil
}

// bookingsPerDay es cuántos turnos tiene un día según el día de la semana:
// viernes y sábado llenos, lunes flojo.
func (s *Seed) bookingsPerDay(wd int) int {
	switch wd {
	case 1:
		return 1 + s.r.Intn(2)
	case 5, 6:
		return 4 + s.r.Intn(2)
	case 7:
		return 0
	}
	return 2 + s.r.Intn(2)
}

// pastStart ubica un turno dentro de los tramos de la profesional.
func (s *Seed) pastStart(day time.Time, pro staffRef, minutes int) time.Time {
	segments := pro.segments[weekday(day)]
	if len(segments) == 0 {
		return time.Time{}
	}
	seg := segments[s.r.Intn(len(segments))]
	return s.roundSlot(day, seg.Start, seg.End, minutes)
}

// stepSales crea la venta de cada turno atendido (con productos a veces), sus
// líneas, el movimiento de stock y los cierres de caja de los últimos días.
func (s *Seed) stepSales(ctx context.Context) error {
	productNames := make([]string, 0, len(demoProducts))
	for _, p := range demoProducts {
		productNames = append(productNames, p.Name)
	}
	seq := 0
	for _, b := range s.past {
		if b.Status != "done" {
			continue
		}
		seq++
		saleID := seedID("sl", seq)
		method := s.saleMethod()
		item := s.services[b.ServiceName]
		total := b.PriceCents
		var productName string
		var productQty, productCents int
		if s.r.Chance(28) {
			productName = pick(s.r, productNames)
			productQty = 1
			for _, p := range demoProducts {
				if p.Name == productName {
					productCents = p.PriceCents
				}
			}
			total += productCents * productQty
		}
		at := b.StartsAt.Add(time.Duration(item.Minutes) * time.Minute).Add(time.Duration(s.r.Between(2, 9)) * time.Minute)
		if _, err := s.db.exec(ctx,
			`INSERT INTO sales(id,salon_id,professional_id,client_name,total_cents,method,client_op_id,
			   created_at,client_id,products_cents)
			 VALUES($1,$2,$3,$4,$5,$6,'',$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
			saleID, s.salonID, b.ProUserID, b.ClientName, total, method, at, b.ClientID, productCents*productQty,
		); err != nil {
			return err
		}
		version := seq
		if _, err := s.db.exec(ctx,
			`INSERT INTO sale_items(id,sale_id,service_id,qty,unit_price_cents,commission_pct)
			 VALUES($1,$2,$3,1,$4,$5) ON CONFLICT (id) DO NOTHING`,
			seedID("si", version), saleID, b.ServiceID, b.PriceCents, item.CommissionPct,
		); err != nil {
			return err
		}
		if productName == "" {
			continue
		}
		productID := s.products[productName]
		if productID == "" {
			continue
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO sale_products(id,sale_id,product_id,qty,unit_price_cents)
			 VALUES($1,$2,$3,$4,$5) ON CONFLICT (id) DO NOTHING`,
			seedID("sp", version), saleID, productID, productQty, productCents,
		); err != nil {
			return err
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO stock_movements(id,salon_id,product_id,delta,reason,note,sale_id,created_by,created_at)
			 VALUES($1,$2,$3,$4,'sale','',$5,$6,$7) ON CONFLICT (id) DO NOTHING`,
			seedID("sm", version), s.salonID, productID, -productQty, saleID, b.ProUserID, at,
		); err != nil {
			return err
		}
		if _, err := s.db.exec(ctx,
			`UPDATE products SET stock = stock - $2 WHERE id=$1 AND stock >= $2`, productID, productQty); err != nil {
			return err
		}
	}
	return s.stepCashCloses(ctx)
}

// saleMethod reparte los medios de pago de forma realista.
func (s *Seed) saleMethod() string {
	return []string{"cash", "card", "card", "transfer"}[s.r.Intn(4)]
}

// stepCashCloses cierra la caja de los últimos catorce días (el sistema cierra,
// el conteo tiene una diferencia chica). El día de hoy queda abierto.
func (s *Seed) stepCashCloses(ctx context.Context) error {
	for offset := 14; offset >= 1; offset-- {
		day := s.clk.daysAgo(offset)
		if weekday(day) == 7 {
			continue
		}
		id := seedID("cc", offset)
		opened := s.clk.at(day, 9, 0)
		closed := s.clk.at(day, 20, 30)
		cash, err := s.db.scalar(ctx,
			`SELECT COALESCE(sum(total_cents),0) FROM sales
			 WHERE salon_id=$1 AND method='cash' AND created_at >= $2 AND created_at < $3`,
			s.salonID, opened, closed)
		if err != nil {
			return err
		}
		// La diferencia del conteo: casi siempre cero, a veces sobra o falta algo.
		// Nunca deja el conteo en negativo (la DB lo prohíbe y sería absurdo).
		delta := 0
		if s.r.Chance(30) {
			delta = pick(s.r, []int{-200000, -100000, 100500, 50000})
		}
		if cash+int64(delta) < 0 {
			delta = 0
		}
		note := ""
		if delta != 0 {
			note = "Diferencia del conteo de la tarde"
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO cash_closes(id,salon_id,opened_at,closed_at,system_cash_cents,counted_cents,difference_cents,note)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
			id, s.salonID, opened, closed, cash, cash+int64(delta), delta, note,
		); err != nil {
			return err
		}
	}
	return nil
}

// deterministicChargeID arma un id de cargo del banco simulado con la forma que
// valida la API (43 caracteres base64url) y siempre el mismo para la misma
// semilla lógica.
func deterministicChargeID(kind string, n int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("salon-saas-demo-%s-%d-%d", kind, n, defaultSeed)))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:43]
}

// pickClient elige una clienta con la distribución de un salón real: la mitad
// de los turnos son de las diez clientas de siempre, la otra mitad del resto.
func (s *Seed) pickClient() clientRef {
	if len(s.clients) <= 10 || s.r.Chance(50) {
		return pick(s.r, s.clients[:min(10, len(s.clients))])
	}
	return pick(s.r, s.clients[10:])
}

// min es el mínimo de dos enteros.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
