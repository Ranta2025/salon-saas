package httpapi

import "github.com/gin-gonic/gin"

// registerTenantRoutes: alta de salón.
func registerTenantRoutes(authed *gin.RouterGroup, d Deps) {
	authed.POST("/salons", d.Tenants.CreateSalon)
}

// registerMemberRoutes: equipo del salón (alta/baja de miembros, solo
// owner/admin) y el oficio de cada miembro.
func registerMemberRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// Equipo del salón (alta/baja de miembros, solo owner/admin)
	authed.GET("/salons/:id/members", d.Members.List)
	authed.POST("/salons/:id/members", audit("member.add"), d.Members.Add)
	authed.DELETE("/salons/:id/members/:uid", audit("member.remove"), d.Members.Remove)
	// F2g2: `DELETE` da de baja (ended_at, no borra la fila) y esta ruta deshace
	// la baja conservando rol, marca, oficio y servicios.
	authed.POST("/salons/:id/members/:uid/reactivate", audit("member.reactivate"), d.Members.Reactivate)
	// F2a/F2c: "atiende clientes" y oficio del miembro (el dueño-barbero), y
	// qué servicios hace (PUT, lista completa).
	authed.PATCH("/salons/:id/members/:uid", audit("member.update"), d.Members.Update)
	authed.PUT("/salons/:id/members/:uid/services", audit("member.services"), d.Members.SetServices)
}

// registerInvitationRoutes: invitaciones de equipo (alta, listado, revocación y
// el canje para una cuenta que ya existe).
func registerInvitationRoutes(authed *gin.RouterGroup, d Deps, audit auditFunc) {
	// F2g1: invitaciones de equipo (alta, listado y revocación). Solo
	// dueño/administración; la regla de qué rol puede invitar cada quien vive en
	// el caso de uso (la misma de `POST /members`).
	authed.POST("/salons/:id/invitations", audit("invitation.create"), d.Invitations.Create)
	authed.GET("/salons/:id/invitations", d.Invitations.List)
	authed.DELETE("/salons/:id/invitations/:iid", audit("invitation.revoke"), d.Invitations.Revoke)
	// Canje para una cuenta que YA existe: la sesión es la del login, la ruta no
	// es pública y la invitación tiene que ser de SU email.
	authed.POST("/invitations/accept", d.Invitations.AcceptAs)
}

// registerStaffRoutes: select de profesional de la agenda.
func registerStaffRoutes(authed *gin.RouterGroup, d Deps) {
	// Equipo del salón (select de profesional de la agenda)
	authed.GET("/salons/:id/staff", d.Staff.List)
}

// registerInvitationsPublicRoutes: el lookup y el canje público de una
// invitación (F2g1).
func registerInvitationsPublicRoutes(v1 *gin.RouterGroup, d Deps) {
	// F2g1: el canje de una invitación también es público (la persona todavía no
	// tiene sesión) y va bajo el rate limit por IP, como /auth. El token viaja en
	// el CUERPO: nunca en la ruta ni en el query, así no queda en ningún log.
	publicInvitation := func(h gin.HandlerFunc) []gin.HandlerFunc {
		if d.PublicInvitationLimit == nil {
			return []gin.HandlerFunc{h}
		}
		return []gin.HandlerFunc{d.PublicInvitationLimit, h}
	}
	v1.POST("/public/invitations/lookup", publicInvitation(d.Invitations.Lookup)...)
	v1.POST("/public/invitations/accept", publicInvitation(d.Invitations.Accept)...)
}
