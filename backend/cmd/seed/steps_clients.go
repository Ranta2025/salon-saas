package main

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// timeRFC3339 es el formato que la API usa en todos los cuerpos.
const timeRFC3339 = time.RFC3339

// intField lee un número de un objeto JSON suelto (los enteros llegan como
// float64 después de json.Unmarshal).
func intField(obj map[string]any, key string) int {
	switch v := obj[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	}
	return 0
}

// hourOf y minuteOf parten un "HH:MM" de los horarios.
func hourOf(hhmm string) int {
	return minuteParts(hhmm)[0]
}

func minuteOf(hhmm string) int {
	return minuteParts(hhmm)[1]
}

func minuteParts(hhmm string) [2]int {
	var out [2]int
	parts := strings.SplitN(strings.TrimSpace(hhmm), ":", 2)
	if len(parts) > 0 {
		out[0], _ = strconv.Atoi(parts[0])
	}
	if len(parts) > 1 {
		out[1], _ = strconv.Atoi(parts[1])
	}
	return out
}

// stepClients crea las cuarenta fichas (por email, así repetir no duplica) y
// sube la foto de dos de ellas.
func (s *Seed) stepClients(ctx context.Context) error {
	path := "/salons/" + s.salonID + "/clients"
	list, err := s.api.getList(ctx, path, "clients")
	if err != nil {
		return err
	}
	byEmail := make(map[string]string, len(list))
	for _, obj := range list {
		byEmail[strings.ToLower(stringField(obj, "email"))] = jsonID(obj)
	}
	s.clients = s.clients[:0]
	for i, c := range demoClients {
		email := c.EmailName + demoEmailDomain
		id, ok := byEmail[email]
		if !ok {
			var created map[string]any
			body := map[string]any{
				"name": c.Name, "phone": c.Phone, "email": email,
				"notes": c.Notes, "allergies": c.Allergies,
			}
			if err := s.api.must(ctx, http.MethodPost, path, body, &created); err != nil {
				return err
			}
			id = jsonID(created)
		}
		s.clients = append(s.clients, clientRef{ID: id, Name: c.Name, Phone: c.Phone, Email: email})
		// Les primeras dos fichas llevan foto (las que ya tienen una se saltean).
		if i < len(clientPhotoAssets) && id != "" {
			if err := s.uploadClientPhoto(ctx, id, clientPhotoAssets[i]); err != nil {
				return err
			}
		}
	}
	return nil
}

// uploadClientPhoto sube la foto de una ficha si todavía no tiene ninguna.
func (s *Seed) uploadClientPhoto(ctx context.Context, clientID string, asset demoAsset) error {
	photos, err := s.api.getList(ctx, "/salons/"+s.salonID+"/clients/"+clientID+"/photos", "photos")
	if err != nil {
		return err
	}
	if len(photos) > 0 {
		return nil
	}
	data, err := loadAsset(s.assetsDir, asset)
	if err != nil {
		return err
	}
	return s.api.upload(ctx, "/salons/"+s.salonID+"/clients/"+clientID+"/photos", asset.File, data, nil)
}

// stepProducts crea el inventario y deja movimientos de stock (alta, reposición
// y una pérdida) además de dos alertas de stock bajo.
func (s *Seed) stepProducts(ctx context.Context) error {
	path := "/salons/" + s.salonID + "/products"
	list, err := s.api.getList(ctx, path, "products")
	if err != nil {
		return err
	}
	for _, obj := range list {
		s.products[stringField(obj, "name")] = jsonID(obj)
	}
	for _, p := range demoProducts {
		if _, ok := s.products[p.Name]; ok {
			continue
		}
		var created map[string]any
		body := map[string]any{
			"name": p.Name, "price_cents": p.PriceCents,
			"stock": p.Stock, "low_stock_threshold": p.Threshold,
		}
		if err := s.api.must(ctx, http.MethodPost, path, body, &created); err != nil {
			return err
		}
		s.products[p.Name] = jsonID(created)
	}
	// Movimientos con historia: una reposición y una pérdida en dos productos.
	movements := []struct {
		product string
		delta   int
		reason  string
		note    string
	}{
		{"Shampoo nutritivo 500 ml", 12, "restock", "Reposición del proveedor"},
		{"Esmalte semipermanente nude", -1, "loss", "Se rompió un frasco"},
		{"Sérum antifrizz 50 ml", 6, "restock", "Reposición del proveedor"},
	}
	for _, m := range movements {
		id := s.products[m.product]
		if id == "" || s.productHasAdjustment(ctx, id, m.note) {
			continue
		}
		if err := s.api.must(ctx, http.MethodPost, path+"/"+id+"/adjust", map[string]any{
			"delta": m.delta, "reason": m.reason, "note": m.note,
		}, nil); err != nil {
			return err
		}
	}
	return nil
}

// productHasAdjustment mira si el movimiento ya está cargado (idempotencia de la
// parte que va por API).
func (s *Seed) productHasAdjustment(ctx context.Context, productID, note string) bool {
	n, err := s.db.count(ctx, "stock_movements",
		"salon_id=$1 AND product_id=$2 AND note=$3", s.salonID, productID, note)
	return err == nil && n > 0
}
