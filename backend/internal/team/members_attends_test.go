package team

import (
	"errors"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/testutil"
)

// S-F2a-4/S-F2a-5: al dar de alta, la marca sale del rol salvo que el body diga
// otra cosa: el profesional atiende siempre, la administración solo si se pide.
func TestMemberService_add_attends(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")

	// Profesional sin attends en el body: atiende (es su rol).
	if _, err := svc.Add(owner, "s1", "caro@mail.com", domain.RoleProfessional, nil); err != nil {
		t.Fatalf("owner agrega profesional: %v", err)
	}
	if got, _ := mems.Find("u-prof", "s1"); !got.Attends || !got.Bookable() {
		t.Fatalf("el profesional agregado tiene que atender: %+v", got)
	}
	// Administración sin attends en el body: NO atiende.
	if _, err := svc.Add(owner, "s1", "eva@mail.com", domain.RoleAdmin, nil); err != nil {
		t.Fatalf("owner agrega admin: %v", err)
	}
	if got, _ := mems.Find("u-owner2", "s1"); got.Attends || got.Bookable() {
		t.Fatalf("la administración no nace atendiendo: %+v", got)
	}
	// Recepción: tampoco.
	if _, err := svc.Add(owner, "s1", "dani@mail.com", domain.RoleReception, nil); err != nil {
		t.Fatalf("owner agrega recepción: %v", err)
	}
	if got, _ := mems.Find("u-recep", "s1"); got.Attends {
		t.Fatalf("recepción no atiende: %+v", got)
	}
}

// El body puede encender la marca, pero no para un rol que nunca atiende ni
// apagarla para un profesional.
func TestMemberService_add_attends_explicito(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	si, no := true, false

	// Administración que también atiende (el dueño-barbero suele ser admin).
	m, err := svc.Add(owner, "s1", "eva@mail.com", domain.RoleAdmin, &si)
	if err != nil {
		t.Fatalf("admin que atiende: %v", err)
	}
	if !m.Attends || m.Role != domain.RoleAdmin {
		t.Fatalf("el miembro devuelto lleva la marca: %+v", m)
	}
	if got, _ := mems.Find("u-owner2", "s1"); !got.Attends {
		t.Fatalf("la marca no se persistió: %+v", got)
	}
	// Profesional en false: 400 con el mensaje del contrato.
	_, err = svc.Add(owner, "s1", "caro@mail.com", domain.RoleProfessional, &no)
	if !errors.Is(err, domain.ErrValidation) || err.Error() != "Las profesionales siempre atienden" {
		t.Fatalf("profesional en false: %v", err)
	}
	// Recepción en true: 400. Recepción nunca atiende.
	_, err = svc.Add(owner, "s1", "dani@mail.com", domain.RoleReception, &si)
	if !errors.Is(err, domain.ErrValidation) || err.Error() != "Ese rol no atiende clientes" {
		t.Fatalf("recepción que atiende: %v", err)
	}
	// Nada de eso dejó membresías a medias.
	if _, err := mems.Find("u-prof", "s1"); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("el alta rechazada no debe guardar nada: %v", err)
	}
}

// S-F2a-5: PATCH /salons/:id/members/:uid. Solo owner/admin del salón, el
// miembro tiene que ser DE ESE salón y el rol decide si la marca se puede
// cambiar.
func TestMemberService_setAttends(t *testing.T) {
	svc, mems, _ := memberFixture(t)
	owner, _ := mems.Find("u-owner", "s1")
	admin, _ := mems.Find("u-admin", "s1")
	_ = mems.Save(domain.Membership{UserID: "u-prof", SalonID: "s1", Role: domain.RoleProfessional, Attends: true})
	prof, _ := mems.Find("u-prof", "s1")
	recep := testutil.Miembro(mems, "u-recep", "s1", domain.RoleReception)

	// El dueño puede apagarse a sí mismo: deja de atender sin dejar de mandar.
	m, err := svc.Update(owner, "s1", "u-owner", attendsPatch(false))
	if err != nil {
		t.Fatalf("dueño se apaga: %v", err)
	}
	if m.ID != "u-owner" || m.Name != "Ana" || m.Email != "ana@mail.com" || m.Role != domain.RoleOwner || m.Attends {
		t.Fatalf("miembro devuelto: %+v", m)
	}
	if got, _ := mems.Find("u-owner", "s1"); got.Attends {
		t.Fatalf("la marca no se persistió: %+v", got)
	}
	// La administración la enciende y la apaga (el admin también administra).
	if _, err := svc.Update(admin, "s1", "u-admin", attendsPatch(true)); err != nil {
		t.Fatalf("admin se enciende: %v", err)
	}
	if got, _ := mems.Find("u-admin", "s1"); !got.Attends || !got.Bookable() {
		t.Fatalf("admin con attends: %+v", got)
	}
	if _, err := svc.Update(admin, "s1", "u-admin", attendsPatch(false)); err != nil {
		t.Fatalf("admin se apaga: %v", err)
	}
	// Profesional en false: 400 con el mensaje exacto y sin tocar la marca.
	_, err = svc.Update(owner, "s1", "u-prof", attendsPatch(false))
	if !errors.Is(err, domain.ErrValidation) || err.Error() != "Las profesionales siempre atienden" {
		t.Fatalf("profesional en false: %v", err)
	}
	if got, _ := mems.Find("u-prof", "s1"); !got.Attends {
		t.Fatalf("la marca del profesional no se toca: %+v", got)
	}
	// Recepción/cliente: 400 con el mensaje exacto, con cualquier valor.
	for _, v := range []bool{true, false} {
		_, err := svc.Update(owner, "s1", "u-recep", attendsPatch(v))
		if !errors.Is(err, domain.ErrValidation) || err.Error() != "Ese rol no atiende clientes" {
			t.Fatalf("recepción en %v: %v", v, err)
		}
	}
	if got, _ := mems.Find("u-recep", "s1"); got.Attends {
		t.Fatalf("recepción no atiende nunca: %+v", got)
	}
	// Miembro de otro salón: 404 (no se confirma ni se niega su existencia).
	testutil.Miembro(mems, "u-ajeno", "s2", domain.RoleAdmin)
	if _, err := svc.Update(owner, "s1", "u-ajeno", attendsPatch(true)); !errors.Is(err, domain.ErrNotFound) {
		t.Fatalf("miembro de otro salón: %v", err)
	}
	if got, _ := mems.Find("u-ajeno", "s2"); got.Attends {
		t.Fatalf("la marca del otro salón no se toca: %+v", got)
	}
	// Sin permiso: el profesional y el dueño de otro salón reciben 403.
	if _, err := svc.Update(prof, "s1", "u-recep", attendsPatch(true)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("profesional administrando: %v", err)
	}
	if _, err := svc.Update(recep, "s1", "u-admin", attendsPatch(true)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("recepción administrando: %v", err)
	}
	ajeno, _ := mems.Find("u-owner", "s2")
	if _, err := svc.Update(ajeno, "s1", "u-admin", attendsPatch(true)); !errors.Is(err, domain.ErrForbidden) {
		t.Fatalf("dueño de otro salón: %v", err)
	}
}

// attendsPatch arma el patch de solo marca (el PATCH de F2a tal cual).
func attendsPatch(v bool) MemberPatch { return MemberPatch{Attends: &v} }
