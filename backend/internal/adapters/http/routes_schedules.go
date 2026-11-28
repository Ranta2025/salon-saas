package httpapi

import "github.com/gin-gonic/gin"

// registerHoursRoutes: horario semanal del salón y de cada profesional (F2d1).
func registerHoursRoutes(authed *gin.RouterGroup, d Deps) {
	// F2d1: horario semanal del salón y de cada profesional.
	authed.GET("/salons/:id/hours", d.Hours.GetSalon)
	authed.PUT("/salons/:id/hours", d.Hours.UpdateSalon)
	authed.GET("/salons/:id/members/:uid/hours", d.Hours.GetMember)
	authed.PUT("/salons/:id/members/:uid/hours", d.Hours.UpdateMember)
}

// registerTimeOffRoutes: ausencias de cada profesional (F2d2).
func registerTimeOffRoutes(authed *gin.RouterGroup, d Deps) {
	// F2d2: ausencias (vacaciones y días libres puntuales) de cada profesional.
	// Cualquier miembro las lee; solo dueño/administración las carga y borra.
	authed.GET("/salons/:id/time-off", d.TimeOff.List)
	authed.POST("/salons/:id/members/:uid/time-off", d.TimeOff.Create)
	authed.DELETE("/salons/:id/time-off/:tid", d.TimeOff.Delete)
}

// registerAbsenceRoutes: cubrir una ausencia reasignando los turnos (F2d3).
func registerAbsenceRoutes(authed *gin.RouterGroup, d Deps) {
	// F2d3: cubrir una ausencia. Solo dueño/administración (el motivo es privado).
	// Reasignar es un PATCH de professional_id apoyado en la EXCLUDE de turnos: no
	// hay tabla nueva ni endpoint de alta.
	authed.GET("/salons/:id/absences", d.Absences.List)
	authed.POST("/salons/:id/absences/:tid/reassign", d.Absences.ReassignAbsence)
	authed.POST("/salons/:id/bookings/:bid/reassign", d.Absences.ReassignBooking)
}
