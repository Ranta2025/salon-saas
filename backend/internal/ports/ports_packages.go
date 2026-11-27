package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// ===== F8: paquetes y bonos prepagos con saldo por clienta (inicio) =====

// PackageRepository: el catálogo de paquetes del salón (migración 031). Toda
// consulta va scopeada por salon_id: el paquete de otro tenant no existe acá
// (ErrNotFound). Quitar un paquete es desactivarlo: no hay borrado.
type PackageRepository interface {
	// Create inserta el paquete. Un nombre ACTIVO repetido en el salón (sin
	// importar mayúsculas) es ErrAlreadyExists.
	Create(p domain.ServicePackage) error
	// Save reemplaza nombre, precio, vigencia y estado de un paquete existente
	// (servicio y sesiones no se editan). ErrNotFound si no es del salón;
	// ErrAlreadyExists si el nombre activo ya existe.
	Save(p domain.ServicePackage) error
	FindByID(salonID, id string) (domain.ServicePackage, error)
	// List trae los paquetes del salón (activos e inactivos) por nombre, con el
	// nombre del servicio.
	List(salonID string) ([]domain.ServicePackage, error)
}

// ClientPackageRepository: los paquetes comprados por una clienta y su libro de
// saldo (D3/D4). Toda consulta va scopeada por salon_id y client_id. Cada cambio
// de saldo escribe su movimiento en la MISMA transacción: en todo momento
// sessions_left = Σ delta.
type ClientPackageRepository interface {
	// Purchase escribe la compra en UNA transacción: la venta, el paquete de la
	// clienta y el movimiento `purchase`. Si algo falla no queda nada. Un
	// client_op_id ya usado (índice único de sales) es ErrAlreadyExists y NO
	// deja nada escrito.
	Purchase(o domain.PackagePurchaseOrder) error
	// FindPurchaseByOp resuelve la compra original de un client_op_id. Sin venta
	// con ese id es ErrNotFound; con una venta que NO es una compra de paquete es
	// ErrAlreadyExists (el id ya se usó para otra cosa).
	FindPurchaseByOp(salonID, clientOpID string) (domain.ClientPackage, error)
	// Redeem descuenta UNA sesión con un UPDATE condicional (saldo >= 1 y no
	// vencido en r.Now) más el movimiento `redeem`, en una transacción. Sin filas
	// afectadas distingue ErrNotFound («Paquete no encontrado»), sin saldo y
	// vencido (409 con su texto). Un client_op_id repetido devuelve el estado
	// actual SIN descontar otra vez.
	Redeem(r domain.PackageRedeemRequest) (domain.ClientPackage, error)
	// Adjust mueve el saldo con un UPDATE condicional (0..sessions_total) y escribe
	// el movimiento `adjust`. ErrNotFound o 409 «El ajuste deja el saldo fuera de
	// rango».
	Adjust(a domain.PackageAdjustRequest) (domain.ClientPackage, error)
	// ListByClient trae los paquetes de la clienta (los vigentes con saldo
	// primero, tope MaxPackageListed) con el nombre del servicio.
	ListByClient(salonID, clientID string, now time.Time) ([]domain.ClientPackage, error)
	// ListMovements trae el libro de UN paquete de esa clienta, más nuevo
	// primero, con tope MaxPackageListed. ErrNotFound si el paquete no es de esa
	// clienta en ese salón.
	ListMovements(salonID, clientID, clientPackageID string) ([]domain.PackageMovement, error)
}

// ===== F8: paquetes y bonos prepagos con saldo por clienta (fin) =====
