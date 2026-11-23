package domain

import (
	"strings"
	"testing"
	"time"
)

// F1 (D1): el token del enlace son 32 bytes de crypto/rand en base64url SIN
// relleno (43 caracteres), como el de las invitaciones de F2g1. Nunca se repite.
func TestBookingLinkToken_forma(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		token, err := NewBookingLinkToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if len(token) != LinkTokenLength {
			t.Fatalf("el token mide %d, esperaba %d: %q", len(token), LinkTokenLength, token)
		}
		if !ValidBookingLinkToken(token) {
			t.Fatalf("el token generado no pasa la validación de forma: %q", token)
		}
		if seen[token] {
			t.Fatalf("token repetido: %q", token)
		}
		seen[token] = true
	}
}

// Un token mal formado no llega ni a la DB: largo distinto, caracteres fuera del
// alfabeto base64url, vacío o con espacios.
func TestValidBookingLinkToken_rechaza_formas_malas(t *testing.T) {
	good, _ := NewBookingLinkToken()
	if !ValidBookingLinkToken(good) {
		t.Fatalf("el token bueno tiene que pasar: %q", good)
	}
	for _, bad := range []string{
		"",
		"   ",
		good[:42],
		good + "A",
		strings.Repeat("a", 43) + "=",
		strings.Repeat("+", 43), // base64 clásico, no url-safe
		strings.Repeat("/", 43),
		strings.Repeat("a", 42) + " ",
		strings.Repeat("ñ", 43),
		strings.Repeat("a", 42) + "\n",
	} {
		if ValidBookingLinkToken(bad) {
			t.Fatalf("no puede aceptar %q", bad)
		}
	}
}

// D1: se guarda SOLO el SHA-256 (hex de 64) y la comparación es de tiempo
// constante sobre el hash.
func TestBookingLinkHash_solo_sha256(t *testing.T) {
	token := "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRS" // 43
	hash := HashBookingLinkToken(token)
	if len(hash) != 64 {
		t.Fatalf("el hash mide %d, esperaba 64: %q", len(hash), hash)
	}
	if hash == token {
		t.Fatal("el hash no puede ser el token")
	}
	if HashBookingLinkToken(token) != hash {
		t.Fatal("el hash tiene que ser determinista")
	}
	if HashBookingLinkToken(token+"x") == hash {
		t.Fatal("otro token no puede dar el mismo hash")
	}
	if !BookingLinkTokenMatches(hash, token) {
		t.Fatal("el token correcto tiene que coincidir con su hash")
	}
	if BookingLinkTokenMatches(hash, token+"x") {
		t.Fatal("un token distinto no puede coincidir")
	}
	if BookingLinkTokenMatches("", token) || BookingLinkTokenMatches(hash, "") {
		t.Fatal("hash o token vacío nunca coincide")
	}
}

// NewBookingLink valida lo que la DB no puede adivinar: el salón, el turno y que
// el hash sea un SHA-256 en hex.
func TestNewBookingLink(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	hash := HashBookingLinkToken(strings.Repeat("a", LinkTokenLength))
	link, err := NewBookingLink("s1", "b1", hash, now)
	if err != nil {
		t.Fatalf("enlace: %v", err)
	}
	if link.ID == "" || link.SalonID != "s1" || link.BookingID != "b1" || link.TokenHash != hash {
		t.Fatalf("enlace: %+v", link)
	}
	if !link.CreatedAt.Equal(now) || link.Used() {
		t.Fatalf("enlace recién emitido: %+v", link)
	}
	for _, bad := range [][3]string{
		{"", "b1", hash},
		{"s1", "", hash},
		{"s1", "b1", ""},
		{"s1", "b1", "no-es-un-hash"},
		{"s1", "b1", strings.Repeat("z", 63)},
	} {
		if _, err := NewBookingLink(bad[0], bad[1], bad[2], now); err == nil {
			t.Fatalf("tenía que rechazar %+v", bad)
		}
	}
	// El enlace usado deja de servir (D2) pero conserva su hash.
	usedAt := now.Add(time.Hour)
	link.UsedAt = &usedAt
	if !link.Used() {
		t.Fatal("con used_at el enlace está usado")
	}
}

// D3: vigencia EN VIVO del enlace: el turno tiene que seguir `booked` y empezar
// en el futuro. Se puede cambiar solo con 2 horas o más de anticipación.
func TestBookingLink_vigencia_en_vivo(t *testing.T) {
	now := time.Date(2026, 12, 1, 12, 0, 0, 0, time.UTC)
	base := Booking{ID: "b1", SalonID: "s1", Status: BookingBooked}

	in3h := base
	in3h.StartsAt = now.Add(3 * time.Hour)
	if !in3h.LinkLive(now) || !in3h.LinkCanChange(now) {
		t.Fatalf("a 3 h el enlace sirve para cambiar: %+v", in3h)
	}

	in1h := base
	in1h.StartsAt = now.Add(time.Hour)
	if !in1h.LinkLive(now) {
		t.Fatal("a 1 h el turno sigue vigente (se muestra)")
	}
	if in1h.LinkCanChange(now) {
		t.Fatal("a 1 h no se puede cambiar (D3)")
	}

	// Frontera exacta: 2 horas justas todavía se puede cambiar.
	edge := base
	edge.StartsAt = now.Add(LinkMinNoticeHours * time.Hour)
	if !edge.LinkCanChange(now) {
		t.Fatal("con 2 horas justas se puede cambiar")
	}

	// Un instante antes de las 2 horas ya no.
	almost := base
	almost.StartsAt = now.Add(LinkMinNoticeHours*time.Hour - time.Second)
	if almost.LinkCanChange(now) {
		t.Fatal("a 1 h 59 m 59 s no se puede cambiar")
	}

	for _, status := range []BookingStatus{BookingCancelled, BookingDone, BookingNoShow} {
		closed := base
		closed.Status = status
		closed.StartsAt = now.Add(3 * time.Hour)
		if closed.LinkLive(now) || closed.LinkCanChange(now) {
			t.Fatalf("un turno %q no está vigente para el enlace", status)
		}
	}

	past := base
	past.StartsAt = now.Add(-time.Hour)
	if past.LinkLive(now) {
		t.Fatal("un turno que ya pasó no está vigente")
	}

	// El personal lo mueve: el enlace vale contra el horario nuevo, sin tocar el
	// token (el turno es la verdad).
	moved := in3h
	moved.StartsAt = now.Add(30 * time.Hour)
	if !moved.LinkLive(now) || !moved.LinkCanChange(now) {
		t.Fatal("un turno movido sigue vigente contra el horario nuevo")
	}
}

// Los textos del contrato (D5/D6) son exactos: la pantalla los muestra verbatim.
func TestBookingLink_mensajes_del_contrato(t *testing.T) {
	cases := map[string]string{
		MsgBookingLinkInvalid:   "Enlace inválido o vencido",
		MsgBookingLinkMinNotice: "Faltan menos de 2 horas para el turno: comunicate con el salón para cambiarlo",
		MsgBookingLinkSlotTaken: "Ese horario ya no está disponible: elegí otro",
		MsgBookingLinkSameSlot:  "Elegí un horario distinto al actual",
		MsgBookingLinkDate:      "Fecha inválida: usá AAAA-MM-DD",
	}
	for got, want := range cases {
		if got != want {
			t.Fatalf("mensaje %q, esperaba %q", got, want)
		}
	}
	if LinkMinNoticeHours != 2 {
		t.Fatalf("LinkMinNoticeHours = %d, el contrato fija 2", LinkMinNoticeHours)
	}
}
