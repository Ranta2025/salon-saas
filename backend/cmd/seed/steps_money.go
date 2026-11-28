package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"
)

// stepExpenses carga los gastos de los últimos 60 días por la API (categorías,
// importe, fecha y descripción son del contrato de F3b).
func (s *Seed) stepExpenses(ctx context.Context) error {
	existing, err := s.db.count(ctx, "expenses", "salon_id=$1", s.salonID)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	path := "/salons/" + s.salonID + "/expenses"
	for _, e := range demoExpenses {
		spentOn := s.clk.daysAgo(e.DaysAgo).Format("2006-01-02")
		body := map[string]any{
			"category": e.Category, "amount_cents": e.AmountCents,
			"spent_on": spentOn, "description": e.Description,
		}
		if err := s.api.must(ctx, http.MethodPost, path, body, nil); err != nil {
			return err
		}
	}
	return nil
}

// stepPayroll deja las dos pantallas con datos: una liquidación CERRADA del mes
// pasado (con un ajuste y ya pagada) y un período ABIERTO del mes en curso con
// sus líneas calculadas y un ajuste a mano.
func (s *Seed) stepPayroll(ctx context.Context) error {
	runs, err := s.api.getList(ctx, "/salons/"+s.salonID+"/payroll/runs", "runs")
	if err != nil {
		return err
	}
	if len(runs) > 0 {
		return nil
	}
	// 1) Liquidación cerrada y pagada del mes pasado.
	lastMonth := s.clk.now.AddDate(0, -1, -2)
	periodDate := time.Date(lastMonth.Year(), lastMonth.Month(), 15, 12, 0, 0, 0, s.clk.loc).Format("2006-01-02")
	closed, err := s.createPayrollRun(ctx, "month", periodDate)
	if err != nil {
		return err
	}
	if line := firstLineOf(closed); line != "" {
		_, _ = s.api.do(ctx, http.MethodPatch,
			"/salons/"+s.salonID+"/payroll/runs/"+closed.ID+"/lines/"+line,
			map[string]any{"adjustment_cents": 3500000, "adjustment_note": "Bono por las fiestas"})
	}
	if err := s.api.must(ctx, http.MethodPost,
		"/salons/"+s.salonID+"/payroll/runs/"+closed.ID+"/pay", map[string]any{}, nil); err != nil {
		return err
	}
	// 2) Período abierto del mes en curso: la liquidación existe (status open)
	// con las líneas del cálculo REAL de la API (la vista previa) y un ajuste.
	// Crear una liquidación por API para un período sin terminar es 400, así que
	// la fila la escribe el seed con los números que calculó la API.
	return s.createOpenPayrollRun(ctx)
}

// createOpenPayrollRun arma el período abierto del mes en curso.
func (s *Seed) createOpenPayrollRun(ctx context.Context) error {
	existing, err := s.db.count(ctx, "payroll_runs", "salon_id=$1 AND status='open'", s.salonID)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	type previewLine struct {
		UserID          string `json:"user_id"`
		Name            string `json:"name"`
		SalesCount      int    `json:"sales_count"`
		SalesTotalCents int    `json:"sales_total_cents"`
		CommissionCents int    `json:"commission_cents"`
		FixedCents      int    `json:"fixed_cents"`
		TotalCents      int    `json:"total_cents"`
	}
	var preview struct {
		Period struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"period"`
		Lines []previewLine `json:"lines"`
	}
	if err := s.api.must(ctx, http.MethodPost, "/salons/"+s.salonID+"/payroll/preview",
		map[string]any{"kind": "month", "date": s.clk.now.Format("2006-01-02")}, &preview); err != nil {
		return err
	}
	if preview.Period.From == "" || preview.Period.To == "" {
		return fmt.Errorf("seed: la vista previa de nómina no devolvió el período")
	}
	runID := seedID("pr", 1)
	createdAt := s.clk.now.AddDate(0, 0, -3)
	if _, err := s.db.exec(ctx,
		`INSERT INTO payroll_runs(id,salon_id,kind,from_date,to_date,status,created_by,created_at)
		 VALUES($1,$2,'month',$3,$4,'open',$5,$6) ON CONFLICT (id) DO NOTHING`,
		runID, s.salonID, preview.Period.From, preview.Period.To, s.ownerID, createdAt,
	); err != nil {
		return err
	}
	adjusted := false
	for i, line := range preview.Lines {
		adjustment, note := 0, ""
		if !adjusted && line.TotalCents > 0 {
			adjustment, note = 800000, "Adelanto pedido por la profesional"
			adjusted = true
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO payroll_lines(run_id,user_id,name_snapshot,sales_count,sales_total_cents,
			   commission_cents,fixed_cents,adjustment_cents,adjustment_note,total_cents,estimated)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT DO NOTHING`,
			runID, line.UserID, line.Name, line.SalesCount, line.SalesTotalCents,
			line.CommissionCents, line.FixedCents, adjustment, note,
			line.TotalCents+adjustment, i == 0,
		); err != nil {
			return err
		}
	}
	return nil
}

// payrollRun es lo mínimo que el seed necesita de una liquidación.
type payrollRun struct {
	ID    string
	Lines []map[string]any
}

// createPayrollRun crea una liquidación del período que contiene `date`.
func (s *Seed) createPayrollRun(ctx context.Context, kind, date string) (payrollRun, error) {
	var created struct {
		ID    string           `json:"id"`
		Lines []map[string]any `json:"lines"`
	}
	if err := s.api.must(ctx, http.MethodPost, "/salons/"+s.salonID+"/payroll/runs",
		map[string]any{"kind": kind, "date": date}, &created); err != nil {
		return payrollRun{}, err
	}
	return payrollRun{ID: created.ID, Lines: created.Lines}, nil
}

// firstLineOf elige la primera línea con comisión (la de una profesional que
// atiende) para colgarle el ajuste.
func firstLineOf(run payrollRun) string {
	for _, line := range run.Lines {
		if id := stringField(line, "user_id"); id != "" {
			return id
		}
	}
	return ""
}

// stepSellPackages vende tres bonos a tres clientas y descuenta una sesión, así
// la pantalla de paquetes muestra saldo y movimientos.
func (s *Seed) stepSellPackages(ctx context.Context) error {
	existing, err := s.db.count(ctx, "client_packages", "salon_id=$1", s.salonID)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	plans := []struct {
		pkg    string
		client int
		redeem bool
	}{
		{pkg: "Bono 5 cortes", client: 0, redeem: true},
		{pkg: "Pack 4 manicuras", client: 5},
		{pkg: "Bono 3 coloraciones", client: 9},
	}
	for i, plan := range plans {
		pkgID := s.packages[plan.pkg]
		if pkgID == "" || plan.client >= len(s.clients) {
			continue
		}
		client := s.clients[plan.client]
		pro := s.staff[i%len(s.staff)]
		var purchased struct {
			ClientPackage map[string]any `json:"client_package"`
		}
		body := map[string]any{
			"package_id": pkgID, "professional_id": pro.UserID,
			"method": s.saleMethod(), "client_op_id": seedID("pk-buy", i),
		}
		if err := s.api.must(ctx, http.MethodPost,
			"/salons/"+s.salonID+"/clients/"+client.ID+"/packages", body, &purchased); err != nil {
			return err
		}
		if !plan.redeem {
			continue
		}
		if id := jsonID(purchased.ClientPackage); id != "" {
			_ = s.api.must(ctx, http.MethodPost,
				"/salons/"+s.salonID+"/clients/"+client.ID+"/packages/"+id+"/redeem",
				map[string]any{
					"professional_id": pro.UserID, "note": "Primera sesión del bono",
					"client_op_id": seedID("pk-rd", i),
				}, nil)
		}
	}
	return nil
}

// stepLoyaltyRewards ajusta el inicio del conteo (el programa arrancó hace 60
// días, como el historial), entrega vales a las clientas que llegaron a las
// ocho visitas y usa uno.
func (s *Seed) stepLoyaltyRewards(ctx context.Context) error {
	// El programa se guardó recién, así que su counting_from es "ahora": para la
	// demo lo movemos al comienzo del historial. Es un UPDATE del propio salón de
	// la demo y no toca ninguna fila derivada.
	if _, err := s.db.exec(ctx,
		`UPDATE loyalty_programs SET counting_from=$2 WHERE salon_id=$1`,
		s.salonID, s.clk.daysAgo(60)); err != nil {
		return err
	}
	rows, err := s.db.pool.Query(ctx,
		`SELECT client_id, count(*) FROM bookings
		 WHERE salon_id=$1 AND status='done' AND client_id IS NOT NULL AND starts_at >= $2
		 GROUP BY client_id HAVING count(*) >= 6 ORDER BY count(*) DESC LIMIT 6`,
		s.salonID, s.clk.daysAgo(60))
	if err != nil {
		return err
	}
	type visitCount struct {
		clientID string
		visits   int
	}
	var due []visitCount
	for rows.Next() {
		var v visitCount
		if err := rows.Scan(&v.clientID, &v.visits); err != nil {
			rows.Close()
			return err
		}
		due = append(due, v)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	granted := 0
	var firstReward struct {
		clientID string
		rewardID string
	}
	for _, v := range due {
		body := map[string]any{"client_op_id": seedID("loyal-grant", granted)}
		var grantedReward struct {
			Reward map[string]any `json:"reward"`
		}
		res, err := s.api.do(ctx, http.MethodPost,
			"/salons/"+s.salonID+"/clients/"+v.clientID+"/loyalty/grant", body)
		if err != nil {
			return err
		}
		if !res.ok() {
			// Todavía no llegó a las visitas o ya tiene el vale: no es un error.
			continue
		}
		if err := res.decode(&grantedReward); err != nil {
			return err
		}
		granted++
		if firstReward.rewardID == "" {
			firstReward = struct {
				clientID string
				rewardID string
			}{clientID: v.clientID, rewardID: jsonID(grantedReward.Reward)}
		}
	}
	// El primer vale se usa: la pantalla muestra los dos estados.
	if firstReward.rewardID != "" {
		pro := s.staff[0]
		_ = s.api.must(ctx, http.MethodPost,
			"/salons/"+s.salonID+"/clients/"+firstReward.clientID+"/loyalty/rewards/"+firstReward.rewardID+"/redeem",
			map[string]any{
				"professional_id": pro.UserID, "note": "Vale usado en el salón",
				"client_op_id": seedID("loyal-redeem", 0),
			}, nil)
	}
	return nil
}

// stepWaitlist carga cinco pedidos por la vía pública (la misma que usa una
// clienta desde la landing).
func (s *Seed) stepWaitlist(ctx context.Context) error {
	path := "/public/salons/" + s.salonSlug + "/waitlist"
	for i, entry := range demoWaitlistEntries {
		svc, ok := s.services[entry.Service]
		if !ok {
			continue
		}
		day := s.clk.daysAhead(entry.DayOffset)
		body := map[string]any{
			"service_id": svc.ID,
			"date":       day.Format("2006-01-02"),
			"name":       entry.Name,
			"email":      entry.Email,
			"phone":      entry.Phone,
		}
		res, err := s.api.do(ctx, http.MethodPost, path, body)
		if err != nil {
			return err
		}
		if res.ok() {
			continue
		}
		// La API solo deja anotarse cuando el día NO tiene horarios libres ("Todavía
		// hay horarios libres ese día"). En la demo queremos la lista poblada
		// igual, así que esa entrada se escribe directo: es una fila del salón de
		// la demo con id determinista y sin token de baja.
		if err := s.insertWaitlistEntry(ctx, i, entry, svc.ID, day); err != nil {
			return err
		}
	}
	return nil
}

// insertWaitlistEntry escribe una entrada de la lista de espera sin pasar por la
// API (la API la rechaza si el día todavía tiene huecos).
func (s *Seed) insertWaitlistEntry(ctx context.Context, n int, entry demoWaitlist, serviceID string, day time.Time) error {
	_, err := s.db.exec(ctx,
		`INSERT INTO waitlist_entries(id,salon_id,service_id,professional_id,day,name,email,phone,token_hash,created_at)
		 VALUES($1,$2,$3,NULL,$4,$5,$6,$7,NULL,$8)
		 ON CONFLICT DO NOTHING`,
		seedID("wl", n+1), s.salonID, serviceID, day.Format("2006-01-02"),
		entry.Name, entry.Email, entry.Phone, s.clk.now.AddDate(0, 0, -n),
	)
	return err
}

// stepDeposits siembra las señas del historial con sus cuatro estados finales
// (pagada, vencida, devuelta y perdida): las que no se pueden generar esperando
// sobre la agenda futura.
func (s *Seed) stepDeposits(ctx context.Context) error {
	existing, err := s.db.count(ctx, "booking_deposits",
		"salon_id=$1 AND created_at < now() - interval '1 day'", s.salonID)
	if err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	// Se cuelgan de turnos del historial, de los más viejos a los más nuevos.
	var candidates []pastBooking
	for _, b := range s.past {
		if b.StartsAt.Before(s.clk.now.AddDate(0, 0, -1)) {
			candidates = append(candidates, b)
		}
	}
	if len(candidates) < 8 {
		return nil
	}
	type depositState struct {
		status   string
		paid     bool
		settled  bool
		bankCode string
	}
	states := []depositState{
		{status: "paid", paid: true, bankCode: "approved"},
		{status: "paid", paid: true, bankCode: "approved"},
		{status: "paid", paid: true, bankCode: "approved"},
		{status: "paid", paid: true, bankCode: "approved"},
		{status: "expired"},
		{status: "expired"},
		{status: "refunded", settled: true, bankCode: "refunded"},
		{status: "forfeited", settled: true, bankCode: "approved"},
	}
	for i, st := range states {
		b := candidates[(i*5)%len(candidates)]
		amount := b.PriceCents * 30 / 100
		created := b.StartsAt.AddDate(0, 0, -4)
		expires := created.Add(30 * time.Minute)
		var paidAt, settledAt any
		if st.paid {
			paidAt = created.Add(6 * time.Minute)
		}
		if st.settled {
			settledAt = b.StartsAt.Add(2 * time.Hour)
		}
		chargeID := deterministicChargeID("deposit", i)
		if _, err := s.db.exec(ctx,
			`INSERT INTO booking_deposits(id,salon_id,booking_id,amount_cents,percent,status,charge_id,
			   created_at,expires_at,paid_at,settled_at,client_op_id)
			 VALUES($1,$2,$3,$4,30,$5,$6,$7,$8,$9,$10,$11) ON CONFLICT (id) DO NOTHING`,
			seedID("dp", i), s.salonID, b.ID, amount, st.status, chargeID,
			created, expires, paidAt, settledAt, seedID("dp-op", i),
		); err != nil {
			return err
		}
		var decidedAt any
		if st.bankCode != "" && st.bankCode != "pending" {
			decidedAt = created.Add(6 * time.Minute)
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO simbank_charges(id,amount_cents,reference,description,status,attempts,expires_at,created_at,decided_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
			chargeID, amount, seedID("dp", i), "Seña "+b.ServiceName,
			orDefault(st.bankCode, "pending"), attemptsOf(st.status), expires, created, decidedAt,
		); err != nil {
			return err
		}
		// Los eventos de la seña: creada siempre, y después el final que tuvo.
		if _, err := s.db.exec(ctx,
			`INSERT INTO deposit_events(id,deposit_id,kind,amount_cents,detail,bank_event_id,created_at)
			 VALUES($1,$2,'created',$3,'Seña creada para el turno',$4,$5) ON CONFLICT (id) DO NOTHING`,
			seedID("de", i*10), seedID("dp", i), amount, seedID("ev", i*10), created,
		); err != nil {
			return err
		}
		kind := map[string]string{
			"paid": "paid", "expired": "expired", "refunded": "refunded", "forfeited": "forfeited",
		}[st.status]
		at := created.Add(10 * time.Minute)
		if st.settled {
			at = b.StartsAt
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO deposit_events(id,deposit_id,kind,amount_cents,detail,bank_event_id,created_at)
			 VALUES($1,$2,$3,$4,'',$5,$6) ON CONFLICT (id) DO NOTHING`,
			seedID("dee", i*10), seedID("dp", i), kind, amount, seedID("ev", i*10+1), at,
		); err != nil {
			return err
		}
	}
	return nil
}

// orDefault devuelve el valor o el default si viene vacío.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// attemptsOf cuenta los intentos que tuvo un cargo del banco.
func attemptsOf(status string) int {
	if status == "pending" {
		return 0
	}
	return 1
}

// stepReviews configura las reseñas posvisita, siembra catorce reseñas reales
// (una oculta) y deja los pedidos con su token ya usado.
func (s *Seed) stepReviews(ctx context.Context) error {
	if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/review-settings",
		map[string]any{"enabled": true, "delay_hours": 24}, nil); err != nil {
		return err
	}
	// La reseña se cuelga de turnos atendidos y distintos: un turno, una reseña.
	var booked []pastBooking
	for i := len(s.past) - 1; i >= 0 && len(booked) < len(demoReviews); i-- {
		if s.past[i].Status == "done" {
			booked = append(booked, s.past[i])
		}
	}
	for i, review := range demoReviews {
		if i >= len(booked) {
			break
		}
		b := booked[i]
		at := b.StartsAt.AddDate(0, 0, 1)
		if _, err := s.db.exec(ctx,
			`INSERT INTO reviews(id,salon_id,booking_id,rating,comment,author_name,service_name,hidden,created_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (id) DO NOTHING`,
			seedID("rv", i+1), s.salonID, b.ID, review.Rating, review.Comment, review.Author,
			b.ServiceName, review.Hidden, at,
		); err != nil {
			return err
		}
		// El pedido que generó el enlace: ya enviado y ya usado.
		if _, err := s.db.exec(ctx,
			`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,created_at,sent_at,delivered_at,expires_at,used_at)
			 VALUES($1,$2,$3,$4,$5,$6,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
			seedID("rq", i+1), s.salonID, b.ID, hashToken("seed-review-token-"+(seedID("rv", i+1))),
			at.Add(-24*time.Hour), at.Add(-23*time.Hour), at.AddDate(0, 0, 7), at,
		); err != nil {
			return err
		}
	}
	// Un pedido PENDIENTE (sin usar) para que la pantalla muestre lo que todavía
	// se puede reclamar, colgado de un turno atendido sin reseña.
	if len(booked) > len(demoReviews) {
		b := booked[len(demoReviews)]
		if _, err := s.db.exec(ctx,
			`INSERT INTO review_requests(id,salon_id,booking_id,token_hash,created_at,expires_at)
			 VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT (id) DO NOTHING`,
			seedID("rq", 99), s.salonID, b.ID,
			hashToken("seed-review-token-pending"), b.StartsAt.AddDate(0, 0, 1),
			b.StartsAt.AddDate(0, 0, 8),
		); err != nil {
			return err
		}
	}
	return nil
}

// hashToken es el mismo hash que usa la API (SHA-256 en hex): el seed nunca
// guarda un token en claro.
func hashToken(token string) string {
	sum := sha256Sum(token)
	return sum
}

// stepNotifications deja la bandeja de avisos con novedades (algunas sin leer).
func (s *Seed) stepNotifications(ctx context.Context) error {
	for i, n := range demoNotifications {
		at := s.clk.now.AddDate(0, 0, -n.DaysAgo).Add(time.Duration(8+i) * time.Hour)
		var readAt any
		if !n.Unread {
			readAt = at.Add(30 * time.Minute)
		}
		if _, err := s.db.exec(ctx,
			`INSERT INTO notifications(id,salon_id,user_id,title,body,kind,read_at,created_at)
			 VALUES($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (id) DO NOTHING`,
			seedID("nt", i+1), s.salonID, s.ownerID, n.Title, n.Body, n.Kind, readAt, at,
		); err != nil {
			return err
		}
	}
	return nil
}

// stepCheckLogins prueba que cada cuenta de la demo entra y que ninguna tiene
// 2FA (el seed no lo activa nunca).
func (s *Seed) stepCheckLogins(ctx context.Context) error {
	for _, u := range demoUsers {
		anon := newAPI(s.cfg.APIURL)
		res, err := anon.do(ctx, http.MethodPost, "/auth/login",
			map[string]string{"email": u.Email, "password": demoPassword})
		if err != nil {
			return err
		}
		if !res.ok() {
			return fmt.Errorf("login de %s: HTTP %d: %s", u.Email, res.status, res.errorText())
		}
		var body struct {
			Token             string `json:"token"`
			TwoFactorRequired bool   `json:"two_factor_required"`
		}
		if err := res.decode(&body); err != nil {
			return err
		}
		if body.TwoFactorRequired || body.Token == "" {
			return fmt.Errorf("login de %s: la cuenta no devolvió token (¿2FA activo?)", u.Email)
		}
	}
	n, err := s.db.count(ctx, "user_totp", "enabled AND user_id IN (SELECT id FROM users WHERE email LIKE '%@salon.local')")
	if err != nil {
		return err
	}
	if n > 0 {
		return fmt.Errorf("seed: %d cuentas de la demo tienen 2FA activo y no deberían", n)
	}
	return nil
}

// sha256Sum es el hash de los tokens que guarda la API (SHA-256 en hex).
func sha256Sum(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
