package main

import (
	"salon-saas/internal/team"
	teamstore "salon-saas/internal/team/postgres"
)

// wireTenants arma el alta de salón (el resto del equipo llega en wireTeam).
func wireTenants(w *wiring) {
	w.tenSvc = team.TenantService{Salons: w.salons, Memberships: w.mems}
}

// wireTeam arma los repositorios del equipo (invitaciones) y, más tarde,
// wireTeamServices los casos de uso.
func wireTeamRepos(w *wiring) {
	// F2g1: invitaciones de equipo. El adaptador es lectura + transacción (el
	// canje es atómico); el token se guarda solo como SHA-256.
	w.invitationRepo = teamstore.Invitations{Pool: w.pool}
}

// wireTeamServices arma el equipo: staff, miembros e invitaciones.
func wireTeamServices(w *wiring) {
	w.staffSvc = team.StaffService{Staff: w.mems, Memberships: w.mems}
	w.memberSvc = team.MemberService{
		Members: w.mems, Memberships: w.mems, Users: w.users,
		Professionals: w.professionals, Services: w.servicesRepo,
		// F2g2: la baja corre en la transacción de `mems`: desactivar la membresía
		// y revocar las invitaciones pendientes que emitió commitean juntos o no
		// pasan (mismo patrón que las invitaciones de F2g1).
		Tx: w.mems,
	}
}

// wireInvitations arma las invitaciones de equipo: el alta manda el correo y el
// canje público crea la cuenta.
func wireInvitations(w *wiring) {
	// F2g1: invitaciones de equipo (el alta manda el correo y devuelve el enlace
	// una sola vez) y el canje público (cuenta nueva + membresía + invitación
	// aceptada en una transacción). Con el mailer nil la invitación existe igual
	// y la respuesta dice email_sent=false.
	w.invitationSvc = team.InvitationService{
		Invitations: w.invitationRepo, Tx: w.invitationRepo, Memberships: w.mems, Users: w.users,
		Salons: w.salons, Settings: w.salons, Mailer: w.mailer, AppURL: w.cfg.AppURL,
	}
	w.invitationPublicSvc = team.InvitationPublicService{
		Invitations: w.invitationRepo, Tx: w.invitationRepo, Users: w.users, Salons: w.salons,
		Hasher: w.hasher, Tokens: w.tokens,
	}
}
