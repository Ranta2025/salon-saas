// Package httpapi traduce HTTP <-> casos de uso.
// Es DELGADO a propósito: valida JSON, llama app, mapea errores a status.
// La lógica vive en app/domain, nunca aquí (SRP).
package httpapi

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/ports"
)

// --- Auth ---

type AuthHandler struct {
	Auth app.AuthService
}

func (h AuthHandler) Register(c *gin.Context) {
	var in struct {
		Name     string `json:"name"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil || in.Password == "" {
		badBody(c)
		return
	}
	u, err := h.Auth.Register(in.Name, in.Email, in.Password)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": u.ID, "name": u.Name, "email": u.Email})
}

func (h AuthHandler) Login(c *gin.Context) {
	var in struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	tok, err := h.Auth.Login(in.Email, in.Password)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tok})
}

// --- Tenants ---

type TenantsHandler struct {
	Tenants     app.TenantService
	Memberships ports.MembershipRepository
}

func (h TenantsHandler) CreateSalon(c *gin.Context) {
	uid, _ := c.Get("userID")
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		badBody(c)
		return
	}
	s, err := h.Tenants.CreateSalon(uid.(string), in.Name, in.Slug)
	if err != nil {
		respondErr(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": s.ID, "name": s.Name, "slug": s.Slug})
}
