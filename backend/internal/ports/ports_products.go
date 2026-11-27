package ports

import "salon-saas/internal/domain"

// --- Productos e inventario del POS (F6 del contrato Fase 6) ---

// ProductRepository: el catálogo de productos y su libro de stock (D1/D2).
// Toda consulta va scopeada por salon_id: el producto de otro tenant no existe
// acá (ErrNotFound).
type ProductRepository interface {
	// Create inserta el producto y, si initial != nil, su movimiento inicial en
	// la MISMA transacción (alta con stock > 0).
	Create(p domain.Product, initial *domain.StockMovement) error
	// Save actualiza nombre, precio, umbral y estado de un producto existente.
	// NO toca el stock: el PUT del contrato no lo cambia.
	Save(p domain.Product) error
	FindByID(salonID, id string) (domain.Product, error)
	// List trae todos los productos del salón (activos e inactivos) por nombre.
	List(salonID string) ([]domain.Product, error)
	// Adjust aplica el delta con el UPDATE condicional de stock (nunca negativo)
	// y escribe el movimiento en la MISMA transacción. Devuelve el producto ya
	// actualizado (con el stock real que quedó).
	Adjust(salonID, productID string, delta int, reason domain.StockReason, note, createdBy string) (domain.Product, error)
	// ListMovements trae el libro de un producto, más reciente primero.
	ListMovements(salonID, productID string, limit int) ([]domain.StockMovement, error)
}

// SaleProductsRepository: la venta CON productos (D3), en UNA sola transacción:
// la venta, sus líneas de servicio, las líneas de producto, el descuento de
// stock de cada producto (ordenado por id para no formar interbloqueos) y un
// movimiento `sale` por descuento. Si algo falla se deshace TODO.
//
// Devuelve los productos que CRUZARON el umbral de stock bajo en esta venta
// (D8): el caso de uso avisa DESPUÉS del commit y un fallo al avisar nunca
// cambia la respuesta de la venta.
type SaleProductsRepository interface {
	SaveWithProducts(s domain.SaleWithProducts) (crossed []domain.Product, err error)
}
