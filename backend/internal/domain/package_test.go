package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func ip(v int) *int { return &v }

func pkgLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatalf("zona %s: %v", name, err)
	}
	return loc
}

// msgOf devuelve el texto de un error de dominio y comprueba su familia.
func msgOf(t *testing.T, err error, kind error) string {
	t.Helper()
	if err == nil {
		t.Fatalf("se esperaba un error de %v", kind)
	}
	if !errors.Is(err, kind) {
		t.Fatalf("el error %q no es de la familia esperada %v", err, kind)
	}
	return err.Error()
}

// D3/D10: validaciones del catálogo con los textos exactos del contrato.
func TestNewServicePackage_validations(t *testing.T) {
	ok, err := NewServicePackage("s1", "  Pack 5 cortes ", "svc1", 5, 90000, ip(90))
	if err != nil {
		t.Fatalf("válido: %v", err)
	}
	if ok.Name != "Pack 5 cortes" || ok.Sessions != 5 || ok.PriceCents != 90000 || ok.ValidityDays == nil || *ok.ValidityDays != 90 || !ok.Active || ok.ID == "" {
		t.Fatalf("paquete armado mal: %+v", ok)
	}
	if free, err := NewServicePackage("s1", "Gratis", "svc1", 1, 0, nil); err != nil || free.ValidityDays != nil || free.PriceCents != 0 {
		t.Fatalf("precio 0 y sin vigencia son válidos: %+v %v", free, err)
	}
	// Bordes que SÍ valen.
	for _, c := range []struct {
		sessions, price int
		validity        *int
	}{{1, 10_000_000, ip(1)}, {100, 0, ip(3650)}} {
		if _, err := NewServicePackage("s1", "Borde", "svc1", c.sessions, c.price, c.validity); err != nil {
			t.Fatalf("borde válido %+v: %v", c, err)
		}
	}

	longName := strings.Repeat("ñ", 121)
	for _, c := range []struct {
		name     string
		sessions int
		price    int
		validity *int
		want     string
	}{
		{"", 5, 100, nil, "El nombre del paquete es obligatorio"},
		{"   ", 5, 100, nil, "El nombre del paquete es obligatorio"},
		{longName, 5, 100, nil, "El nombre del paquete no puede superar los 120 caracteres"},
		{"Pack\x00", 5, 100, nil, MsgPackageInvalidChars},
		{"Pack\n5", 5, 100, nil, MsgPackageInvalidChars},
		{"Pack\r5", 5, 100, nil, MsgPackageInvalidChars},
		{"Pack 5\n", 5, 100, nil, MsgPackageInvalidChars},
		{"Pack\u20285", 5, 100, nil, MsgPackageInvalidChars},
		{"Pack", 0, 100, nil, "Las sesiones deben estar entre 1 y 100"},
		{"Pack", 101, 100, nil, "Las sesiones deben estar entre 1 y 100"},
		{"Pack", 5, -1, nil, "El precio debe estar entre 0 y 10000000 centavos"},
		{"Pack", 5, 10_000_001, nil, "El precio debe estar entre 0 y 10000000 centavos"},
		{"Pack", 5, 100, ip(0), "La vigencia debe estar entre 1 y 3650 días"},
		{"Pack", 5, 100, ip(3651), "La vigencia debe estar entre 1 y 3650 días"},
		{"Pack", 5, 100, ip(-3), "La vigencia debe estar entre 1 y 3650 días"},
	} {
		_, err := NewServicePackage("s1", c.name, "svc1", c.sessions, c.price, c.validity)
		if got := msgOf(t, err, ErrValidation); got != c.want {
			t.Errorf("nombre %q ses %d precio %d: mensaje %q, quería %q", c.name, c.sessions, c.price, got, c.want)
		}
	}
	// El nombre de 120 caracteres (no bytes) es válido.
	if _, err := NewServicePackage("s1", strings.Repeat("ñ", 120), "svc1", 5, 100, nil); err != nil {
		t.Fatalf("120 caracteres válidos: %v", err)
	}
	if _, err := NewServicePackage("", "Pack", "svc1", 5, 100, nil); err == nil {
		t.Fatalf("sin salón debe fallar")
	}
	if _, err := NewServicePackage("s1", "Pack", " ", 5, 100, nil); err == nil {
		t.Fatalf("sin servicio debe fallar")
	}
}

// D10: el PUT REEMPLAZA las cuatro cosas y NO toca servicio ni sesiones;
// validity nil = sin vencimiento (no conserva el valor anterior).
func TestServicePackage_WithDetails_replaces(t *testing.T) {
	base, _ := NewServicePackage("s1", "Pack", "svc1", 5, 100, ip(30))
	got, err := base.WithDetails("Pack nuevo", 200, nil, false)
	if err != nil {
		t.Fatalf("WithDetails: %v", err)
	}
	if got.Name != "Pack nuevo" || got.PriceCents != 200 || got.ValidityDays != nil || got.Active {
		t.Fatalf("no reemplazó: %+v", got)
	}
	if got.ServiceID != "svc1" || got.Sessions != 5 || got.ID != base.ID {
		t.Fatalf("tocó lo que no se edita: %+v", got)
	}
	if base.ValidityDays == nil || *base.ValidityDays != 30 || base.Name != "Pack" {
		t.Fatalf("mutó el original: %+v", base)
	}
	if _, err := base.WithDetails("X\n", 1, nil, true); err == nil {
		t.Fatalf("salto de línea debe fallar")
	}
	if _, err := base.WithDetails("X", 1, ip(0), true); err == nil {
		t.Fatalf("vigencia 0 debe fallar")
	}
	// Mutar el puntero recibido no cambia el paquete.
	v := 10
	got2, _ := base.WithDetails("Y", 1, &v, true)
	v = 99
	if *got2.ValidityDays != 10 {
		t.Fatalf("comparte el puntero de la vigencia")
	}
}

// D5: el vencimiento es el FIN del día LOCAL del salón de (compra + vigencia).
// Se prueba en zonas lejanas de UTC en los dos sentidos y alrededor de la
// medianoche local, donde el día UTC y el día local NO coinciden.
func TestExpiryFor_endOfLocalDay(t *testing.T) {
	last := func(y int, m time.Month, d int, loc *time.Location) time.Time {
		return time.Date(y, m, d, 23, 59, 59, 999_999_000, loc).UTC()
	}
	auckland := pkgLoc(t, "Pacific/Auckland")
	la := pkgLoc(t, "America/Los_Angeles")
	utc := time.UTC

	if got := ExpiryFor(time.Now(), nil, auckland); got != nil {
		t.Fatalf("sin vigencia no vence: %v", got)
	}
	cases := []struct {
		name     string
		at       time.Time
		days     int
		loc      *time.Location
		wantY    int
		wantM    time.Month
		wantD    int
		wantUTCt string
	}{
		// 11 de marzo 00:30 en Auckland (NZDT +13) = 10 de marzo 11:30 UTC: el
		// día local es el 11, no el 10 de UTC.
		{"Auckland pasada la medianoche local", time.Date(2026, 3, 10, 11, 30, 0, 0, time.UTC), 1, auckland, 2026, 3, 12, "2026-03-12T10:59:59.999999Z"},
		// 9 de marzo 20:00 en Los Ángeles (PDT -7) = 10 de marzo 03:00 UTC: el
		// día local es el 9.
		{"Los Ángeles de noche", time.Date(2026, 3, 10, 3, 0, 0, 0, time.UTC), 1, la, 2026, 3, 10, "2026-03-11T06:59:59.999999Z"},
		// Cruza el inicio del horario de verano de Los Ángeles (8 de marzo).
		{"Los Ángeles cruzando el cambio de hora", time.Date(2026, 3, 7, 20, 0, 0, 0, time.UTC), 2, la, 2026, 3, 9, "2026-03-10T06:59:59.999999Z"},
		// Cruza el fin del horario de verano de Auckland (5 de abril).
		{"Auckland cruzando el cambio de hora", time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC), 30, auckland, 2026, 4, 10, "2026-04-10T11:59:59.999999Z"},
		// Cruce de año y de mes.
		{"UTC fin de año", time.Date(2026, 12, 31, 23, 59, 59, 0, time.UTC), 1, utc, 2027, 1, 1, "2027-01-01T23:59:59.999999Z"},
		{"UTC 1 día", time.Date(2026, 1, 31, 8, 0, 0, 0, time.UTC), 1, utc, 2026, 2, 1, "2026-02-01T23:59:59.999999Z"},
	}
	for _, c := range cases {
		got := ExpiryFor(c.at, ip(c.days), c.loc)
		if got == nil {
			t.Fatalf("%s: nil", c.name)
		}
		want := last(c.wantY, c.wantM, c.wantD, c.loc)
		if !got.Equal(want) || got.Location() != time.UTC {
			t.Errorf("%s: %v (%v), quería %v", c.name, got, got.Location(), want)
		}
		if s := got.Format("2006-01-02T15:04:05.999999Z07:00"); s != c.wantUTCt {
			t.Errorf("%s: instante %s, quería %s", c.name, s, c.wantUTCt)
		}
	}
}

// La regla de vencer es una sola: vigente mientras expires_at > now.
func TestClientPackage_ExpiredAt_boundary(t *testing.T) {
	at := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	never := ClientPackage{}
	if never.ExpiredAt(at) {
		t.Fatalf("sin vencimiento nunca vence")
	}
	cp := ClientPackage{ExpiresAt: &at}
	if !cp.ExpiredAt(at) {
		t.Fatalf("en el instante exacto ya venció (expires_at > now es falso)")
	}
	if cp.ExpiredAt(at.Add(-time.Nanosecond)) {
		t.Fatalf("un instante antes sigue vigente")
	}
	if !cp.ExpiredAt(at.Add(time.Second)) {
		t.Fatalf("después vence")
	}
}

// D5: la compra arma venta (sin líneas), paquete con FOTO y movimiento.
func TestNewPackagePurchase_shape(t *testing.T) {
	pkg, _ := NewServicePackage("s1", "Pack 5 cortes", "svc1", 5, 90000, ip(90))
	pkg.ServiceName = "Corte"
	client := Client{ID: "c1", SalonID: "s1", Name: "Lía Pérez"}
	now := time.Date(2026, 3, 10, 22, 0, 0, 0, time.UTC)
	loc := pkgLoc(t, "Pacific/Auckland")

	o, err := NewPackagePurchase(pkg, client, "prof1", MethodCard, " op-1 ", "actor1", now, loc)
	if err != nil {
		t.Fatalf("compra: %v", err)
	}
	s := o.Sale
	if s.ID == "" || s.SalonID != "s1" || s.ProfessionalID != "prof1" || s.ClientName != "Lía Pérez" ||
		s.TotalCents != 90000 || s.Method != MethodCard || s.ClientOpID != "op-1" || len(s.Items) != 0 || !s.CreatedAt.Equal(now) {
		t.Fatalf("venta armada mal: %+v", s)
	}
	cp := o.ClientPackage
	if cp.SaleID != s.ID || cp.ClientID != "c1" || cp.PackageID != pkg.ID || cp.ServiceID != "svc1" || cp.Name != "Pack 5 cortes" ||
		cp.SessionsTotal != 5 || cp.SessionsLeft != 5 || cp.PriceCents != 90000 || !cp.PurchasedAt.Equal(now) || cp.ServiceName != "Corte" {
		t.Fatalf("paquete de la clienta armado mal: %+v", cp)
	}
	want := ExpiryFor(now, ip(90), loc)
	if cp.ExpiresAt == nil || !cp.ExpiresAt.Equal(*want) {
		t.Fatalf("vencimiento %v, quería %v", cp.ExpiresAt, want)
	}
	m := o.Movement
	if m.ClientPackageID != cp.ID || m.SalonID != "s1" || m.Delta != 5 || m.Reason != PackagePurchase || m.ProfessionalID != "prof1" || m.Actor != "actor1" || m.ClientOpID != "" {
		t.Fatalf("movimiento de compra armado mal: %+v", m)
	}
	// Sin vigencia: no vence. Precio 0: venta de 0.
	free, _ := NewServicePackage("s1", "Regalo", "svc1", 1, 0, nil)
	o2, err := NewPackagePurchase(free, client, "prof1", MethodCash, "", "a", now, loc)
	if err != nil || o2.ClientPackage.ExpiresAt != nil || o2.Sale.TotalCents != 0 || o2.Sale.ClientOpID != "" {
		t.Fatalf("paquete sin vigencia: %+v %v", o2, err)
	}
	// Rechazos: método, profesional y nombre de la ficha.
	if _, err := NewPackagePurchase(pkg, client, "prof1", PaymentMethod("bitcoin"), "", "a", now, loc); !errors.Is(err, ErrValidation) {
		t.Fatalf("método inválido: %v", err)
	}
	if _, err := NewPackagePurchase(pkg, client, "  ", MethodCash, "", "a", now, loc); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin profesional: %v", err)
	}
	if _, err := NewPackagePurchase(pkg, Client{ID: "c2", Name: " "}, "prof1", MethodCash, "", "a", now, loc); !errors.Is(err, ErrValidation) {
		t.Fatalf("ficha sin nombre: %v", err)
	}
}

// D9: límites del ajuste.
func TestValidatePackageAdjust(t *testing.T) {
	if note, err := ValidatePackageAdjust(-1, "  se descontó de más "); err != nil || note != "se descontó de más" {
		t.Fatalf("válido: %q %v", note, err)
	}
	if _, err := ValidatePackageAdjust(100, "x"); err != nil {
		t.Fatalf("+100 válido: %v", err)
	}
	for _, c := range []struct {
		delta int
		note  string
		kind  error
		want  string
	}{
		{0, "motivo", ErrValidation, "El ajuste no puede ser cero"},
		{1, "", ErrValidation, "Escribí el motivo del ajuste"},
		{1, "   ", ErrValidation, "Escribí el motivo del ajuste"},
		{1, strings.Repeat("a", 201), ErrValidation, MsgPackageNoteTooLong},
		{1, "a\x00b", ErrValidation, MsgPackageInvalidChars},
		{1, "a\nb", ErrValidation, MsgPackageInvalidChars},
		{101, "x", ErrAlreadyExists, "El ajuste deja el saldo fuera de rango"},
		{-101, "x", ErrAlreadyExists, "El ajuste deja el saldo fuera de rango"},
		{1 << 40, "x", ErrAlreadyExists, "El ajuste deja el saldo fuera de rango"},
	} {
		_, err := ValidatePackageAdjust(c.delta, c.note)
		if got := msgOf(t, err, c.kind); got != c.want {
			t.Errorf("delta %d nota %q: %q, quería %q", c.delta, c.note, got, c.want)
		}
	}
	if _, err := ValidatePackageAdjust(1, strings.Repeat("ñ", 200)); err != nil {
		t.Fatalf("200 caracteres válidos: %v", err)
	}
}

func TestValidatePackageNote(t *testing.T) {
	if n, err := ValidatePackageNote("  "); err != nil || n != "" {
		t.Fatalf("la nota es opcional: %q %v", n, err)
	}
	if n, err := ValidatePackageNote(" corte "); err != nil || n != "corte" {
		t.Fatalf("recorta: %q %v", n, err)
	}
	for _, bad := range []string{"a\x00", "a\nb", "a\rb", strings.Repeat("x", 201)} {
		if _, err := ValidatePackageNote(bad); !errors.Is(err, ErrValidation) {
			t.Fatalf("nota %q debe ser 400: %v", bad, err)
		}
	}
}

// El client_op_id es opcional, sin NUL y de largo acotado: nunca llega un NUL a
// Postgres (sería un 500).
func TestValidatePackageOpID(t *testing.T) {
	if id, err := ValidatePackageOpID("  op-1 "); err != nil || id != "op-1" {
		t.Fatalf("válido: %q %v", id, err)
	}
	if id, err := ValidatePackageOpID(""); err != nil || id != "" {
		t.Fatalf("vacío es válido: %q %v", id, err)
	}
	if _, err := ValidatePackageOpID(strings.Repeat("a", 200)); err != nil {
		t.Fatalf("200 válido: %v", err)
	}
	for _, bad := range []string{"a\x00b", strings.Repeat("a", 201)} {
		if got := msgOf(t, func() error { _, e := ValidatePackageOpID(bad); return e }(), ErrValidation); got != "El identificador de la operación no es válido" {
			t.Fatalf("mensaje %q", got)
		}
	}
}
