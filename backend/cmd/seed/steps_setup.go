package main

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// stepSalon carga el perfil público y la configuración operativa del salón.
func (s *Seed) stepSalon(ctx context.Context) error {
	profile := map[string]string{
		"name":        "Salón Aurelia",
		"description": "Peluquería, barbería y estética en el centro. Cortes, color, uñas y faciales con turnos online y señas por la web.",
		"address":     "Av. Rivadavia 3482, Almagro, CABA",
		"phone":       "+54 11 4862-7710",
		"email":       "hola@aurelia.test",
		"instagram":   "salon.aurelia",
	}
	if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/profile", profile, nil); err != nil {
		return err
	}
	settings := map[string]any{
		"timezone":      demoTZ,
		"open_time":     "09:00",
		"close_time":    "21:00",
		"slot_step_min": 30,
	}
	if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/settings", settings, nil); err != nil {
		return err
	}
	// El WhatsApp público es el mismo teléfono: la landing arma el enlace.
	return nil
}

// ensureUser crea la cuenta si todavía no existe. No inicia sesión: cada login
// es un pedido más al límite por IP de /auth/ y acá alcanza con que la cuenta
// esté (los logins se prueban al final, uno por persona).
func (s *Seed) ensureUser(ctx context.Context, u demoUser) error {
	n, err := s.db.count(ctx, "users", "lower(email)=lower($1)", u.Email)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	res, err := newAPI(s.cfg.APIURL).do(ctx, http.MethodPost, "/auth/register", map[string]string{
		"name": u.Name, "email": u.Email, "password": demoPassword,
	})
	if err != nil {
		return err
	}
	if !res.ok() && res.status != http.StatusConflict {
		return fmt.Errorf("registro %s: HTTP %d: %s", u.Email, res.status, res.errorText())
	}
	return nil
}

// stepServices crea los puestos/equipamiento y el catálogo de doce servicios.
func (s *Seed) stepServices(ctx context.Context) error {
	path := "/salons/" + s.salonID + "/resources"
	existing, err := s.api.getList(ctx, path, "types")
	if err != nil {
		return err
	}
	resourceTypes := make(map[string]string, len(demoResourceTypes))
	for _, obj := range existing {
		resourceTypes[stringField(obj, "name")] = jsonID(obj)
	}
	for _, rt := range demoResourceTypes {
		if _, ok := resourceTypes[rt.Name]; ok {
			continue
		}
		var created map[string]any
		if err := s.api.must(ctx, http.MethodPost, path, map[string]any{
			"name": rt.Name, "count": len(rt.Units),
		}, &created); err != nil {
			return err
		}
		resourceTypes[rt.Name] = jsonID(created)
	}

	servicesPath := "/salons/" + s.salonID + "/services"
	list, err := s.api.getList(ctx, servicesPath, "services")
	if err != nil {
		return err
	}
	for _, obj := range list {
		name := stringField(obj, "name")
		s.services[name] = serviceRef{
			ID:            jsonID(obj),
			Name:          name,
			Minutes:       intField(obj, "duration_min"),
			PriceCents:    intField(obj, "price_cents"),
			CommissionPct: intField(obj, "commission_pct"),
			ResourceType:  resourceTypeOf(name),
		}
	}
	for _, svc := range demoServices {
		if _, ok := s.services[svc.Name]; ok {
			continue
		}
		body := map[string]any{
			"name":             svc.Name,
			"duration_min":     svc.Minutes,
			"price_cents":      svc.PriceCents,
			"commission_pct":   svc.CommissionPct,
			"professional_ids": []string{},
			"description":      svc.Description,
		}
		if id, ok := resourceTypes[svc.ResourceType]; ok && id != "" {
			body["resource_type_id"] = id
		}
		var created map[string]any
		if err := s.api.must(ctx, http.MethodPost, servicesPath, body, &created); err != nil {
			return err
		}
		s.services[svc.Name] = serviceRef{
			ID:            jsonID(created),
			Name:          svc.Name,
			Minutes:       svc.Minutes,
			PriceCents:    svc.PriceCents,
			CommissionPct: svc.CommissionPct,
			ResourceType:  svc.ResourceType,
		}
	}
	return nil
}

// resourceTypeOf dice qué puesto necesita un servicio (los que llevan camilla o
// gabinete no se pueden agendar en una silla).
func resourceTypeOf(service string) string {
	switch service {
	case "Manicura semipermanente", "Pedicura", "Esculpidas":
		return "Gabinete de uñas"
	case "Facial hidratante", "Depilación con cera":
		return "Camilla"
	}
	return "Puesto de peluquería"
}

// stepTeam da de alta a las cuentas del equipo, su oficio, sus servicios y su
// modelo de pago. Cada cuenta se registra primero: el alta del salón exige un
// email que YA existe (así lo pide el caso de uso).
func (s *Seed) stepTeam(ctx context.Context) error {
	membersPath := "/salons/" + s.salonID + "/members"
	list, err := s.api.getList(ctx, membersPath, "members")
	if err != nil {
		return err
	}
	byEmail := make(map[string]map[string]any, len(list))
	for _, m := range list {
		byEmail[strings.ToLower(stringField(m, "email"))] = m
	}

	for _, u := range demoUsers[1:] {
		if _, ok := byEmail[strings.ToLower(u.Email)]; ok {
			continue
		}
		if err := s.ensureUser(ctx, u); err != nil {
			return err
		}
		body := map[string]any{"email": u.Email, "role": u.Role}
		// `attends` solo existe para los roles que pueden atender: mandarlo en
		// recepción es 400 "Ese rol no atiende clientes".
		if u.Role == "owner" || u.Role == "admin" || u.Role == "professional" {
			body["attends"] = u.Attends
		}
		if err := s.api.must(ctx, http.MethodPost, membersPath, body, nil); err != nil {
			return err
		}
	}
	// Releer: los ids de los miembros recién creados.
	list, err = s.api.getList(ctx, membersPath, "members")
	if err != nil {
		return err
	}
	s.staff = s.staff[:0]
	for _, m := range list {
		email := strings.ToLower(stringField(m, "email"))
		userID := jsonID(m)
		for _, u := range demoUsers {
			if strings.ToLower(u.Email) != email {
				continue
			}
			if u.Attends {
				_ = s.api.must(ctx, http.MethodPatch, membersPath+"/"+userID, map[string]any{
					"attends": true, "trade": u.Trade,
				}, nil)
				if len(u.Services) > 0 {
					ids := make([]string, 0, len(u.Services))
					for _, name := range u.Services {
						if svc, ok := s.services[name]; ok {
							ids = append(ids, svc.ID)
						}
					}
					if err := s.api.must(ctx, http.MethodPut, membersPath+"/"+userID+"/services",
						map[string]any{"service_ids": ids}, nil); err != nil {
						return err
					}
				}
				s.staff = append(s.staff, staffRef{
					UserID: userID, Name: u.Name, Email: u.Email, Trade: u.Trade,
					Services: u.Services, segments: staffSegments(u.Email),
				})
			}
			// Modelo de pago de TODO el equipo (los que no atienden cobran fijo).
			if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/members/"+userID+"/pay-model",
				map[string]any{
					"fixed_cents":        u.PayModel.FixedCents,
					"fixed_period":       u.PayModel.FixedPeriod,
					"commission_enabled": u.PayModel.CommissionOn,
				}, nil); err != nil {
				return err
			}
			break
		}
	}
	if len(s.staff) < 4 {
		return fmt.Errorf("seed: esperaba al menos 4 profesionales que atienden y encontré %d", len(s.staff))
	}
	return nil
}

// stepHours carga el horario semanal del salón, el de cada profesional (con la
// pausa del mediodía) y dos ausencias.
func (s *Seed) stepHours(ctx context.Context) error {
	days := make([]map[string]any, 0, len(salonHours))
	for wd := 1; wd <= 7; wd++ {
		seg, ok := salonHours[wd]
		if !ok {
			continue
		}
		days = append(days, map[string]any{"weekday": wd, "open": seg.Start, "close": seg.End})
	}
	if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/hours",
		map[string]any{"days": days}, nil); err != nil {
		return err
	}
	for _, pro := range s.staff {
		segments := make([]map[string]any, 0, 8)
		for wd := 1; wd <= 7; wd++ {
			for _, seg := range pro.segments[wd] {
				segments = append(segments, map[string]any{"weekday": wd, "start": seg.Start, "end": seg.End})
			}
		}
		if err := s.api.must(ctx, http.MethodPut,
			"/salons/"+s.salonID+"/members/"+pro.UserID+"/hours",
			map[string]any{"segments": segments}, nil); err != nil {
			return err
		}
	}
	// Dos ausencias: un día libre la semana que viene y una tarde dentro de tres
	// semanas. No pisan turnos futuros de la demo.
	timeOffs := []struct {
		pro     staffRef
		daysAgo int
		start   string
		end     string
		reason  string
	}{
		{s.staff[1%len(s.staff)], 7, "09:00", "18:00", "Día libre"},
		{s.staff[2%len(s.staff)], 21, "14:00", "21:00", "Trámite personal"},
	}
	from := s.clk.daysAgo(1).Format(timeRFC3339)
	to := s.clk.daysAhead(60).Format(timeRFC3339)
	existing, err := s.api.getList(ctx, "/salons/"+s.salonID+"/time-off?from="+url.QueryEscape(from)+"&to="+url.QueryEscape(to), "time_off")
	if err != nil {
		return err
	}
	if len(existing) > 0 {
		return nil // ya cargadas: la demo no duplica ausencias
	}
	for _, t := range timeOffs {
		day := s.clk.daysAhead(t.daysAgo)
		body := map[string]any{
			"starts_at": s.clk.at(day, hourOf(t.start), minuteOf(t.start)).Format(timeRFC3339),
			"ends_at":   s.clk.at(day, hourOf(t.end), minuteOf(t.end)).Format(timeRFC3339),
			"reason":    t.reason,
		}
		if err := s.api.must(ctx, http.MethodPost,
			"/salons/"+s.salonID+"/members/"+t.pro.UserID+"/time-off", body, nil); err != nil {
			// Una ausencia que se solapa con otra da 409: no es un error de la demo.
			s.log.Printf("aviso: ausencia de %s no cargada: %v", t.pro.Name, err)
		}
	}
	return nil
}

// stepPrograms carga el catálogo de paquetes, el programa de fidelidad y deja
// la política de señas APAGADA (se enciende más adelante, cuando ya hay turnos
// sin seña).
func (s *Seed) stepPrograms(ctx context.Context) error {
	pkgPath := "/salons/" + s.salonID + "/packages"
	list, err := s.api.getList(ctx, pkgPath, "packages")
	if err != nil {
		return err
	}
	for _, obj := range list {
		s.packages[stringField(obj, "name")] = jsonID(obj)
	}
	for _, pkg := range demoPackages {
		if _, ok := s.packages[pkg.Name]; ok {
			continue
		}
		svc, ok := s.services[pkg.Service]
		if !ok {
			return fmt.Errorf("seed: el paquete %q pide el servicio %q, que no existe", pkg.Name, pkg.Service)
		}
		var created map[string]any
		body := map[string]any{
			"name": pkg.Name, "service_id": svc.ID, "sessions": pkg.Sessions,
			"price_cents": pkg.PriceCents, "validity_days": pkg.Validity,
		}
		if err := s.api.must(ctx, http.MethodPost, pkgPath, body, &created); err != nil {
			return err
		}
		s.packages[pkg.Name] = jsonID(created)
	}

	reward := s.services["Brushing"]
	if reward.ID != "" {
		body := map[string]any{
			"enabled": true, "visits_required": 8,
			"reward_service_id": reward.ID, "reward_validity_days": 90,
		}
		if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/loyalty-program", body, nil); err != nil {
			return err
		}
	}
	// Política de señas apagada por ahora: así los primeros turnos públicos no
	// piden seña y la pantalla muestra los dos casos.
	return s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/deposit-policy", map[string]any{
		"enabled": false, "percent": 30, "hold_minutes": 30, "refund_hours": 24,
	}, nil)
}

// stepGallery sube las fotos del salón con su leyenda.
func (s *Seed) stepGallery(ctx context.Context) error {
	existing, err := s.api.getList(ctx, "/salons/"+s.salonID+"/gallery", "photos")
	if err != nil {
		return err
	}
	if len(existing) >= len(galleryAssets) {
		return nil
	}
	for _, asset := range galleryAssets {
		data, err := loadAsset(s.assetsDir, asset)
		if err != nil {
			return err
		}
		var created struct {
			Photo map[string]any `json:"photo"`
		}
		if err := s.api.upload(ctx, "/salons/"+s.salonID+"/gallery", asset.File, data, &created); err != nil {
			return err
		}
		if id := jsonID(created.Photo); id != "" && asset.Caption != "" {
			if err := s.api.must(ctx, http.MethodPut, "/salons/"+s.salonID+"/gallery/"+id,
				map[string]string{"caption": asset.Caption}, nil); err != nil {
				return err
			}
		}
	}
	return nil
}
