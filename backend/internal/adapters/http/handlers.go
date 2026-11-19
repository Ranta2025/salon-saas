// Package httpapi traduce HTTP <-> casos de uso.
// Es DELGADO a propósito: valida JSON, llama app, mapea errores a status.
// La lógica vive en app/domain, nunca aquí (SRP).
package httpapi

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"salon-saas/internal/app"
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

func statusOf(err error) int {
	switch {
	case errors.Is(err, domain.ErrValidation):
		return http.StatusBadRequest
	case errors.Is(err, domain.ErrUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, domain.ErrForbidden):
		return http.StatusForbidden
	case errors.Is(err, domain.ErrAlreadyExists):
		return http.StatusConflict
	case errors.Is(err, domain.ErrNotFound):
		return http.StatusNotFound
	default:
		return http.StatusInternalServerError
	}
}

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
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	u, err := h.Auth.Register(in.Name, in.Email, in.Password)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	tok, err := h.Auth.Login(in.Email, in.Password)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "body inválido"})
		return
	}
	s, err := h.Tenants.CreateSalon(uid.(string), in.Name, in.Slug)
	if err != nil {
		c.JSON(statusOf(err), gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": s.ID, "name": s.Name, "slug": s.Slug})
}
