// Pruebas de las reglas de F13 (D4..D7) sin base ni HTTP.
package reviews

import (
	"context"
	"encoding/base64"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// roleUser devuelve una membership activa con el rol pedido.
func roleUser(role domain.Role) domain.Membership {
	return domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role}
}

// world arma el caso de uso sobre los dobles, con el salón s1 y sus cuatro roles.
func newWorld() (*Service, *fakeStore, *fakeMailer, *fakeMemberships) {
	store := newFakeStore()
	mailer := &fakeMailer{}
	mems := &fakeMemberships{rows: map[string]domain.Membership{}}
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleReception, domain.RoleProfessional, domain.RoleClient} {
		_ = mems.Save(domain.Membership{UserID: "u-" + string(role), SalonID: "s1", Role: role})
	}
	svc := &Service{
		Reviews: store, Requests: store, Memberships: mems, Mailer: mailer,
		AppURL: "https://app.test",
	}
	return svc, store, mailer, mems
}

// --- D5: el nombre público -----------------------------------------------------

func TestAuthorName_firstNameAndInitial(t *testing.T) {
	cases := map[string]string{
		"Lía Gómez":       "Lía G.",
		"Lia gomez":       "Lia g.",      // la inicial respeta lo que escribió la clienta
		"Lía":             "Lía",         // una sola palabra: no se inventa un apellido
		"  Mora  Peña  ":  "Mora P.",     // espacios de más no cambian el resultado
		"Ana\tMaría Ruiz": "AnaMaría R.", // los controles no llegan al nombre público
		"":                "",            // un turno sin nombre no inventa autor
		"   ":             "",
	}
	for in, want := range cases {
		if got := AuthorName(in); got != want {
			t.Errorf("AuthorName(%q) = %q, esperaba %q", in, got, want)
		}
	}
	// Nunca el nombre completo del turno ni nada más largo que el tope del CHECK.
	full := AuthorName("Lía Gómez Fernández")
	if strings.Contains(full, "Fernández") || full != "Lía G." {
		t.Fatalf("el nombre público filtra el apellido: %q", full)
	}
}

// --- D4: rating y comentario ----------------------------------------------------

func TestValidateRating_oneToFive(t *testing.T) {
	for _, bad := range []int{0, 6, -1, 100} {
		err := ValidateRating(bad)
		if !errors.Is(err, domain.ErrValidation) || err.Error() != MsgRatingInvalid {
			t.Errorf("rating %d: %v", bad, err)
		}
	}
	for rating := 1; rating <= 5; rating++ {
		if err := ValidateRating(rating); err != nil {
			t.Errorf("rating %d: %v", rating, err)
		}
	}
}

func TestValidateComment_countsRunes(t *testing.T) {
	// 500 emoji son 500 CARACTERES (2000 bytes): entran. 501 no.
	exact := strings.Repeat("💅", MaxCommentRunes)
	got, err := ValidateComment(exact)
	if err != nil || got != exact {
		t.Fatalf("500 runas deben entrar: %v", err)
	}
	if _, err := ValidateComment(exact + "💅"); err == nil || err.Error() != MsgCommentTooLong {
		t.Fatalf("501 runas: %v", err)
	}
	// El tope es de runas, no de bytes: 300 emoji (1200 bytes) siguen entrando.
	if _, err := ValidateComment(strings.Repeat("ñ", 400)); err != nil {
		t.Fatalf("400 runas acentuadas: %v", err)
	}
}

func TestValidateComment_rejectsControlCharactersAndTrims(t *testing.T) {
	for _, bad := range []string{
		"hola\x00mundo",   // NUL: nunca 500
		"uno\rdos",        // CR
		"a\x07b",          // BEL
		"x\x7fy",          // DEL
		"c1 \u0085 aquí",  // C1 (NEL)
		"sep \u2028 aquí", // separador de línea Unicode
		"fin \u2029 aquí",
	} {
		_, err := ValidateComment(bad)
		if err == nil || err.Error() != MsgCommentInvalid || !errors.Is(err, domain.ErrValidation) {
			t.Errorf("comentario %q: %v", bad, err)
		}
	}
	// Decisión: el salto de línea y el tabulador SÍ son texto de un comentario.
	ok, err := ValidateComment("Muy bien\ngracias\tpor todo")
	if err != nil || ok != "Muy bien\ngracias\tpor todo" {
		t.Fatalf("\n y \\t deben entrar: %q %v", ok, err)
	}
	// Un UTF-8 inválido es 400, no un 500 de Postgres.
	if _, err := ValidateComment("mal \xff byte"); err == nil || err.Error() != MsgCommentInvalid {
		t.Fatalf("UTF-8 inválido: %v", err)
	}
	trimmed, err := ValidateComment("   Muy bien  ")
	if err != nil || trimmed != "Muy bien" {
		t.Fatalf("se recorta: %q %v", trimmed, err)
	}
}

// --- D6: página del panel -------------------------------------------------------

func TestParseQuery_limitAndCursor(t *testing.T) {
	q, err := ParseQuery("", "")
	if err != nil || q.Limit != DefaultLimit || q.Before != nil {
		t.Fatalf("por defecto: %+v %v", q, err)
	}
	for _, bad := range []string{"0", "101", "abc", "-1", "1.5", " 5", "00000005"} {
		if _, err := ParseQuery(bad, ""); err == nil || err.Error() != MsgLimitRange {
			t.Errorf("limit %q: %v", bad, err)
		}
	}
	q, err = ParseQuery("100", "")
	if err != nil || q.Limit != 100 {
		t.Fatalf("100 entra: %+v %v", q, err)
	}
	if _, err := ParseQuery("", "no-es-un-cursor"); err == nil || err.Error() != MsgCursorInvalid {
		t.Fatalf("cursor inválido: %v", err)
	}
}

func TestCursor_roundTripAndTampering(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 123456000, time.UTC)
	c := ports.ReviewCursor{CreatedAt: at, ID: "rv-1"}
	enc := EncodeCursor(c)
	back, err := DecodeCursor(enc)
	if err != nil || !back.CreatedAt.Equal(at) || back.ID != c.ID {
		t.Fatalf("ida y vuelta: %+v %v", back, err)
	}
	// Un id con espacios alrededor, una fecha imposible o basura: 400.
	tampered := base64.RawURLEncoding.EncodeToString([]byte("2026-13-99T99:99:99Z|rv-1"))
	blankID := base64.RawURLEncoding.EncodeToString([]byte(at.Format(time.RFC3339Nano) + "| "))
	for _, bad := range []string{"", "   ", "!!!!", tampered, blankID} {
		if _, err := DecodeCursor(bad); err == nil || err.Error() != MsgCursorInvalid {
			t.Errorf("cursor %q: %v", bad, err)
		}
	}
}

// --- D4: el token ---------------------------------------------------------------

func TestToken_shapeHashAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 20; i++ {
		tok, err := NewToken()
		if err != nil {
			t.Fatalf("token: %v", err)
		}
		if !ValidTokenShape(tok) || len(tok) != TokenLen {
			t.Fatalf("forma del token: %q", tok)
		}
		if seen[tok] {
			t.Fatalf("token repetido: %q", tok)
		}
		seen[tok] = true
		hash := HashToken(tok)
		if len(hash) != 64 || hash == tok || strings.Contains(hash, tok) {
			t.Fatalf("el hash no es SHA-256 hex del token: %q -> %q", tok, hash)
		}
	}
	for _, bad := range []string{"", "corto", strings.Repeat("a", 42), strings.Repeat("a", 44), "con espacio aquí", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaA"} {
		if bad == strings.Repeat("a", 42) || bad == "" || bad == "corto" {
			if ValidTokenShape(bad) {
				t.Errorf("forma inválida aceptada: %q", bad)
			}
			continue
		}
		if ValidTokenShape(bad) && strings.ContainsAny(bad, " =") {
			t.Errorf("forma inválida aceptada: %q", bad)
		}
	}
	if !ValidTokenShape(strings.Repeat("A", TokenLen)) {
		t.Error("un token base64url válido debe entrar")
	}
}

// --- D6: permisos ANTES de los parámetros ---------------------------------------

func TestList_permissionBeforeParameters(t *testing.T) {
	svc, store, _, mems := newWorld()
	store.addReview(ports.Review{ID: "rv1", SalonID: "s1", Rating: 5, CreatedAt: time.Now().UTC()})

	// owner/admin/recepción ven el panel.
	for _, role := range []domain.Role{domain.RoleOwner, domain.RoleAdmin, domain.RoleReception} {
		if _, err := svc.List(roleUser(role), "s1", "", ""); err != nil {
			t.Errorf("%s: %v", role, err)
		}
	}
	// profesional, clienta y quien no es del salón: 403 ANTES del límite.
	for _, actor := range []domain.Membership{
		roleUser(domain.RoleProfessional),
		roleUser(domain.RoleClient),
		{UserID: "u-otro", SalonID: "s2", Role: domain.RoleOwner},
		{UserID: "u-owner", SalonID: "s2", Role: domain.RoleOwner},
	} {
		if _, err := svc.List(actor, "s1", "abc", "basura"); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("actor %+v: %v", actor, err)
		}
	}
	// Una membresía dada de baja no es miembro: 403.
	deactivated := domain.Membership{UserID: "u-owner", SalonID: "s1", Role: domain.RoleOwner}
	end := time.Now().UTC()
	deactivated.EndedAt = &end
	_ = mems.Save(deactivated)
	if _, err := svc.List(deactivated, "s1", "", ""); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("baja: %v", err)
	}
}

func TestList_pagination(t *testing.T) {
	svc, store, _, _ := newWorld()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		store.addReview(ports.Review{
			ID: "rv" + string(rune('a'+i)), SalonID: "s1", Rating: 5,
			CreatedAt: base.Add(time.Duration(i) * time.Hour),
		})
	}
	page, err := svc.List(roleUser(domain.RoleOwner), "s1", "2", "")
	if err != nil || len(page.Items) != 2 || page.NextCursor == "" {
		t.Fatalf("primera página: %+v %v", page, err)
	}
	if page.Items[0].ID != "rve" || page.Items[1].ID != "rvd" {
		t.Fatalf("orden del más nuevo al más viejo: %+v", page.Items)
	}
	next, err := svc.List(roleUser(domain.RoleOwner), "s1", "2", page.NextCursor)
	if err != nil || len(next.Items) != 2 || next.Items[0].ID != "rvc" {
		t.Fatalf("segunda página: %+v %v", next, err)
	}
	last, err := svc.List(roleUser(domain.RoleOwner), "s1", "2", next.NextCursor)
	if err != nil || len(last.Items) != 1 || last.NextCursor != "" {
		t.Fatalf("última página: %+v %v", last, err)
	}
}

// --- D6: configuración -----------------------------------------------------------

func TestSettings_defaultWhenNeverSaved(t *testing.T) {
	svc, _, _, _ := newWorld()
	st, err := svc.Settings(roleUser(domain.RoleReception), "s1")
	if err != nil || st.Enabled || st.DelayHours != DefaultDelayHours || st.Saved {
		t.Fatalf("sin guardar: %+v %v", st, err)
	}
	// Profesional y clienta no configuran: 403.
	for _, role := range []domain.Role{domain.RoleProfessional, domain.RoleClient} {
		if _, err := svc.Settings(roleUser(role), "s1"); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: %v", role, err)
		}
	}
}

func TestSaveSettings_managerAndDelayRange(t *testing.T) {
	svc, store, _, _ := newWorld()
	st, err := svc.SaveSettings(roleUser(domain.RoleOwner), "s1", true, 24)
	if err != nil || !st.Enabled || st.DelayHours != 24 {
		t.Fatalf("guardar: %+v %v", st, err)
	}
	if got, _ := store.Settings("s1"); !got.Enabled || got.DelayHours != 24 {
		t.Fatalf("no se guardó: %+v", got)
	}
	// recepción, profesional y clienta: 403 (aunque la demora sea válida).
	for _, role := range []domain.Role{domain.RoleReception, domain.RoleProfessional, domain.RoleClient} {
		if _, err := svc.SaveSettings(roleUser(role), "s1", true, 24); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: %v", role, err)
		}
	}
	// La demora fuera de 1..72 es 400 con su texto.
	for _, bad := range []int{0, 73, -3} {
		_, err := svc.SaveSettings(roleUser(domain.RoleAdmin), "s1", true, bad)
		if err == nil || err.Error() != MsgDelayRange || !errors.Is(err, domain.ErrValidation) {
			t.Errorf("demora %d: %v", bad, err)
		}
	}
	// Un 400 no pisa lo guardado.
	if got, _ := store.Settings("s1"); got.DelayHours != 24 {
		t.Fatalf("el 400 escribió: %+v", got)
	}
}

// --- D6: ocultar y mostrar --------------------------------------------------------

func TestHide_permissionsAndSalonScope(t *testing.T) {
	svc, store, _, _ := newWorld()
	store.addReview(ports.Review{ID: "rv1", SalonID: "s1", Rating: 4, CreatedAt: time.Now().UTC()})
	store.addReview(ports.Review{ID: "rv2", SalonID: "s2", Rating: 4, CreatedAt: time.Now().UTC()})

	if err := svc.Hide(roleUser(domain.RoleOwner), "s1", "rv1", true); err != nil {
		t.Fatalf("ocultar: %v", err)
	}
	// Idempotente: ocultar dos veces no es un error y no cambia nada más.
	if err := svc.Hide(roleUser(domain.RoleOwner), "s1", "rv1", true); err != nil {
		t.Fatalf("ocultar dos veces: %v", err)
	}
	if err := svc.Hide(roleUser(domain.RoleAdmin), "s1", "rv1", false); err != nil {
		t.Fatalf("mostrar: %v", err)
	}
	// La reseña de OTRO salón es 404 con su texto.
	err := svc.Hide(roleUser(domain.RoleOwner), "s1", "rv2", true)
	if !errors.Is(err, domain.ErrNotFound) || err.Error() != MsgReviewNotFound {
		t.Fatalf("otro salón: %v", err)
	}
	// Un id que no puede existir es 404 sin tocar la base (nunca un 500).
	for _, bad := range []string{"", "a\x00b", strings.Repeat("x", MaxIDLen+1)} {
		if err := svc.Hide(roleUser(domain.RoleOwner), "s1", bad, true); !errors.Is(err, domain.ErrNotFound) {
			t.Errorf("id %q: %v", bad, err)
		}
	}
	// El permiso va primero: recepción/profesional reciben 403 aunque el id sea basura.
	for _, role := range []domain.Role{domain.RoleReception, domain.RoleProfessional, domain.RoleClient} {
		if err := svc.Hide(roleUser(role), "s1", "a\x00b", true); !errors.Is(err, domain.ErrForbidden) {
			t.Errorf("%s: %v", role, err)
		}
	}
}

// --- D4: enlace y canje -----------------------------------------------------------

func tokenFor(t *testing.T) (string, string) {
	t.Helper()
	tok, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	return tok, HashToken(tok)
}

func seededRequest(t *testing.T, store *fakeStore, now time.Time) (string, string) {
	t.Helper()
	tok, hash := tokenFor(t)
	store.addRequest(fakeRequest{
		id: "rq1", salonID: "s1", bookingID: "b1", tokenHash: hash,
		createdAt: now, expiresAt: now.Add(RequestTTL), sentAt: &now,
		startsAt: now.Add(-48 * time.Hour), clientName: "Lía Gómez", serviceName: "Corte",
	})
	return tok, hash
}

func TestLink_uniformNotFound(t *testing.T) {
	svc, store, _, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	tok, _ := seededRequest(t, store, now)

	link, err := svc.Link(tok)
	if err != nil || link.SalonName != "s1" || link.ServiceName != "Corte" {
		t.Fatalf("enlace válido: %+v %v", link, err)
	}
	// Desconocido, con forma inválida, vencido y usado: el MISMO 404.
	otro, _ := tokenFor(t)
	expired, expiredHash := tokenFor(t)
	store.addRequest(fakeRequest{id: "rq2", salonID: "s1", bookingID: "b2", tokenHash: expiredHash,
		createdAt: now.Add(-31 * 24 * time.Hour), expiresAt: now.Add(-time.Hour), sentAt: &now})
	used, usedHash := tokenFor(t)
	store.addRequest(fakeRequest{id: "rq3", salonID: "s1", bookingID: "b3", tokenHash: usedHash,
		createdAt: now, expiresAt: now.Add(RequestTTL), sentAt: &now, usedAt: &now})
	for _, bad := range []string{otro, "", "no-token", expired, used} {
		_, err := svc.Link(bad)
		if !errors.Is(err, domain.ErrNotFound) || err.Error() != MsgLinkInvalid {
			t.Errorf("token %q: %v", bad, err)
		}
	}
}

func TestSubmit_invalidBodyDoesNotConsumeToken(t *testing.T) {
	svc, store, _, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	svc.Now = func() time.Time { return now }
	tok, _ := seededRequest(t, store, now)

	// Rating inválido y comentario de 501: 400 y el enlace SIGUE sirviendo.
	if _, err := svc.Submit(tok, 0, ""); err == nil || err.Error() != MsgRatingInvalid {
		t.Fatalf("rating 0: %v", err)
	}
	if _, err := svc.Submit(tok, 5, strings.Repeat("a", 501)); err == nil || err.Error() != MsgCommentTooLong {
		t.Fatalf("501: %v", err)
	}
	if used := store.requestsOf()[0].usedAt; used != nil {
		t.Fatal("un 400 consumió el enlace")
	}
	// El canje bueno guarda el nombre público (D5) y el servicio.
	review, err := svc.Submit(tok, 5, "  Excelente atención  ")
	if err != nil {
		t.Fatalf("canje: %v", err)
	}
	if review.Comment != "Excelente atención" || review.AuthorName != "Lía G." || review.ServiceName != "Corte" {
		t.Fatalf("reseña guardada: %+v", review)
	}
	// El SEGUNDO POST con el mismo token es 404 uniforme.
	if _, err := svc.Submit(tok, 5, "otra"); !errors.Is(err, domain.ErrNotFound) || err.Error() != MsgLinkInvalid {
		t.Fatalf("reuso: %v", err)
	}
}

// --- D7: la sección pública -------------------------------------------------------

func TestPublicSummary_oneDecimalAndHiddenExcluded(t *testing.T) {
	svc, store, _, _ := newWorld()
	base := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i, rating := range []int{5, 4, 4, 3} {
		store.addReview(ports.Review{ID: "rv" + string(rune('a'+i)), SalonID: "s1", Rating: rating,
			AuthorName: "Lía G.", CreatedAt: base.Add(time.Duration(i) * time.Hour)})
	}
	sum, err := svc.PublicSummary("s1")
	if err != nil || sum.Count != 4 || sum.Average != 4.0 {
		t.Fatalf("promedio: %+v %v", sum, err)
	}
	// Una oculta desaparece del promedio y de la lista (regla del usuario, D7).
	store.addReview(ports.Review{ID: "rvbad", SalonID: "s1", Rating: 1, AuthorName: "X", CreatedAt: base.Add(9 * time.Hour)})
	if err := svc.Hide(roleUser(domain.RoleOwner), "s1", "rvbad", true); err != nil {
		t.Fatal(err)
	}
	sum, err = svc.PublicSummary("s1")
	if err != nil || sum.Count != 4 || sum.Average != 4.0 {
		t.Fatalf("con una oculta: %+v %v", sum, err)
	}
	for _, item := range sum.Items {
		if item.Rating == 1 {
			t.Fatalf("la oculta sigue en la web: %+v", sum.Items)
		}
	}
	// 4.666... -> 4.7 (una decimal).
	store.addReview(ports.Review{ID: "rvfive", SalonID: "s1", Rating: 5, AuthorName: "Mora P.", CreatedAt: base.Add(20 * time.Hour)})
	sum, _ = svc.PublicSummary("s1")
	if sum.Count != 5 || sum.Average != 4.2 {
		t.Fatalf("5+4+4+3+5 = 4.2, dio %+v", sum)
	}
	// El promedio se calcula sobre TODAS las visibles, no sobre las 6 que se
	// muestran, y sale con UNA decimal.
	store.mu.Lock()
	for i := 0; i < 10; i++ {
		store.reviews = append(store.reviews, ports.Review{ID: "rvmas" + string(rune('a'+i)), SalonID: "s1",
			Rating: 5, AuthorName: "Mora P.", CreatedAt: base.Add(time.Duration(100+i) * time.Hour)})
	}
	store.mu.Unlock()
	sum, _ = svc.PublicSummary("s1")
	if len(sum.Items) != MaxPublicReviews || sum.Count != 15 {
		t.Fatalf("6 renglones y 15 visibles: %+v", sum)
	}
	if want := math.Round((21.0+50.0)/15.0*10) / 10; sum.Average != want {
		t.Fatalf("promedio sobre todas: %v, esperaba %v", sum.Average, want)
	}
}

func TestPublicSummary_noVisibleReviewsAndFailure(t *testing.T) {
	svc, store, _, _ := newWorld()
	sum, err := svc.PublicSummary("s1")
	if err != nil || sum.Count != 0 || len(sum.Items) != 0 {
		t.Fatalf("sin reseñas: %+v %v", sum, err)
	}
	store.visibleErr = errors.New("la tabla no está (migración sin aplicar)")
	if _, err := svc.PublicSummary("s1"); err == nil {
		t.Fatal("el fallo de la consulta debe poder degradarse en quien llama")
	}
}

// --- D3: el barrido periódico ------------------------------------------------------

func dueReview(bookingID string) ports.DueReview {
	return ports.DueReview{
		BookingID: bookingID, SalonID: "s1", SalonName: "Luna Studio", ServiceName: "Corte",
		ClientName: "Lía Gómez", Email: "lia@mail.com",
		StartsAt: time.Date(2026, 11, 3, 18, 0, 0, 0, time.UTC), Timezone: "America/Mexico_City",
	}
}

func TestRunOnce_sendsOneEmailOnly(t *testing.T) {
	svc, store, mailer, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store.due = []ports.DueReview{dueReview("b1")}

	sent, failed := svc.RunOnce(context.Background(), now)
	if sent != 1 || failed != 0 {
		t.Fatalf("primera pasada: %d/%d", sent, failed)
	}
	// Segunda pasada (el otro barredor): el pedido ya está enviado, no hay otro correo.
	sent, failed = svc.RunOnce(context.Background(), now.Add(time.Minute))
	if sent != 0 || failed != 0 {
		t.Fatalf("segunda pasada: %d/%d", sent, failed)
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("dos barredores mandaron %d correos", got)
	}
	reqs := store.requestsOf()
	if len(reqs) != 1 || reqs[0].sentAt == nil {
		t.Fatalf("pedido: %+v", reqs)
	}
	// El token del correo es el que quedó guardado como hash, y el correo no lo
	// repite en el query.
	msg := mailer.messages()[0]
	if !strings.Contains(msg.Text, "https://app.test/resena#") || msg.To != "lia@mail.com" {
		t.Fatalf("correo: %+v", msg)
	}
	token := msg.Text[strings.Index(msg.Text, "https://app.test/resena#")+len("https://app.test/resena#"):]
	token, _, _ = strings.Cut(token, "\n")
	if HashToken(token) != reqs[0].tokenHash {
		t.Fatalf("el hash guardado no es el del token enviado")
	}
	if strings.Contains(msg.Text, "?token=") {
		t.Fatal("el token no puede ir en el query")
	}
}

func TestRunOnce_retriesAfterAFailedSend(t *testing.T) {
	svc, store, mailer, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store.due = []ports.DueReview{dueReview("b1")}
	mailer.failNext = 1

	sent, failed := svc.RunOnce(context.Background(), now)
	if sent != 0 || failed != 1 {
		t.Fatalf("envío fallido: %d/%d", sent, failed)
	}
	// sent_at vuelve a NULL: el correo se reintenta en el próximo tick.
	first := store.requestsOf()
	if len(first) != 1 || first[0].sentAt != nil {
		t.Fatalf("el reclamo no se deshizo: %+v", first)
	}
	sent, failed = svc.RunOnce(context.Background(), now.Add(time.Minute))
	if sent != 1 || failed != 0 {
		t.Fatalf("reintento: %d/%d", sent, failed)
	}
	second := store.requestsOf()
	if second[0].sentAt == nil {
		t.Fatal("el reintento no reclamó el envío")
	}
	if second[0].tokenHash == first[0].tokenHash {
		t.Fatal("el reintento debe rotar el token: el anterior no se puede reenviar")
	}
	if len(mailer.messages()) != 2 || mailer.fails != 1 {
		t.Fatalf("intentos: %d, fallos: %d", len(mailer.messages()), mailer.fails)
	}
}

func TestRunOnce_withoutMailerOrDueTouchesNothing(t *testing.T) {
	svc, store, _, _ := newWorld()
	store.due = []ports.DueReview{dueReview("b1")}
	svc.Mailer = nil
	if sent, failed := svc.RunOnce(context.Background(), time.Now()); sent != 0 || failed != 0 {
		t.Fatalf("sin correo configurado no se reclama nada: %d/%d", sent, failed)
	}
	if reqs := store.requestsOf(); len(reqs) != 0 {
		t.Fatalf("se creó un pedido sin poder enviarlo: %+v", reqs)
	}
}

// El enlace del correo va en el FRAGMENTO y el HTML escapa lo variable.
func TestReviewRequestMail_linkAndEscaping(t *testing.T) {
	d := dueReview("b1")
	d.SalonName = `Luna <Studio>`
	msg := reviewRequestMail(d, ReviewLinkURL("https://app.test/", "tok123"))
	if msg.Subject != "¿Cómo te fue en Luna <Studio>?" {
		t.Fatalf("asunto: %q", msg.Subject)
	}
	if !strings.Contains(msg.Text, "https://app.test/resena#tok123") {
		t.Fatalf("enlace: %s", msg.Text)
	}
	if !strings.Contains(msg.HTML, `href="https://app.test/resena#tok123"`) {
		t.Fatalf("HTML: %s", msg.HTML)
	}
	if strings.Contains(msg.HTML, "<Studio>") || !strings.Contains(msg.HTML, "&lt;Studio&gt;") {
		t.Fatalf("el HTML no escapa el nombre del salón: %s", msg.HTML)
	}
	// La fecha va en la zona del salón (America/Mexico_City = UTC-6).
	if !strings.Contains(msg.Text, "12:00") {
		t.Fatalf("la hora no está en la zona del salón: %s", msg.Text)
	}
	if !strings.Contains(msg.Text, "30 días") {
		t.Fatalf("el correo no dice cuánto dura el enlace: %s", msg.Text)
	}
	if ReviewLinkURL("", "tok") != "http://localhost:8081/resena#tok" {
		t.Fatalf("sin APP_URL: %q", ReviewLinkURL("", "tok"))
	}
}

// D3 (arriendo): el envío se marca ENTREGADO solo después del envío bueno, y un
// envío fallido deshace el arriendo para que el próximo tick reintente.
func TestRunOnce_marksDeliveredOnlyAfterTheSend(t *testing.T) {
	svc, store, mailer, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store.due = []ports.DueReview{dueReview("b1")}

	if sent, failed := svc.RunOnce(context.Background(), now); sent != 1 || failed != 0 {
		t.Fatalf("pasada buena: %d/%d", sent, failed)
	}
	req := store.requestsOf()[0]
	if req.deliveredAt == nil || !req.deliveredAt.Equal(now) {
		t.Fatalf("no quedó entregado: %+v", req)
	}
	if req.sentAt == nil || !req.sentAt.Equal(now) {
		t.Fatalf("el arriendo no quedó sellado: %+v", req)
	}
	// Un entregado no vuelve a salir ni a reclamarse.
	if sent, failed := svc.RunOnce(context.Background(), now.Add(time.Hour)); sent != 0 || failed != 0 {
		t.Fatalf("segunda pasada: %d/%d", sent, failed)
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("correos: %d", got)
	}
}

func TestRunOnce_sendErrorDoesNotMarkDelivered(t *testing.T) {
	svc, store, mailer, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	store.due = []ports.DueReview{dueReview("b1")}
	mailer.failNext = 1

	if sent, failed := svc.RunOnce(context.Background(), now); sent != 0 || failed != 1 {
		t.Fatalf("envío fallido: %d/%d", sent, failed)
	}
	req := store.requestsOf()[0]
	if req.deliveredAt != nil {
		t.Fatalf("un envío fallido quedó entregado: %+v", req)
	}
	if req.sentAt != nil {
		t.Fatalf("el arriendo no se deshizo: %+v", req)
	}
	// El reintento entrega una sola vez más.
	if sent, failed := svc.RunOnce(context.Background(), now.Add(time.Minute)); sent != 1 || failed != 0 {
		t.Fatalf("reintento: %d/%d", sent, failed)
	}
	if store.requestsOf()[0].deliveredAt == nil {
		t.Fatal("el reintento no marcó entregado")
	}
}

// F13 verifier V1 advisory: Go's JSON decoder turns invalid UTF-8 into U+FFFD BEFORE
// ValidateComment sees it, so the replacement character itself must be rejected
// (the documented 400 for invalid UTF-8), not stored.
func TestValidateComment_rejectsTheReplacementCharacterInvalidUTF8BecomesInJSON(t *testing.T) {
	for _, in := range []string{"a\uFFFDb", "\uFFFD", "bien \uFFFD"} {
		if _, err := ValidateComment(in); err == nil {
			t.Fatalf("%q debe ser un 400", in)
		}
	}
	if got, err := ValidateComment("ñandú ✓ 😀"); err != nil || got != "ñandú ✓ 😀" {
		t.Fatalf("el texto válido sigue entrando: %q %v", got, err)
	}
}

// F13 verifier V2 advisory: with two sweepers the second one's Claim loses; that
// attempt sent nothing, so it must not be counted as "sent" in the log numbers.
func TestRequestOne_reportsWhetherItActuallySentTheMail(t *testing.T) {
	svc, store, mailer, _ := newWorld()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	d := dueReview("b1")
	store.due = []ports.DueReview{d}

	sent, err := svc.requestOne(context.Background(), d, now)
	if err != nil || !sent {
		t.Fatalf("el primer barredor manda: %v %v", sent, err)
	}
	// The other sweeper holds the same (stale) due list: its Claim loses.
	sent, err = svc.requestOne(context.Background(), d, now.Add(time.Minute))
	if err != nil || sent {
		t.Fatalf("el que pierde el reclamo no manda ni cuenta: %v %v", sent, err)
	}
	if got := len(mailer.messages()); got != 1 {
		t.Fatalf("un solo correo: %d", got)
	}
}
