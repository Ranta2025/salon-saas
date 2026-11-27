package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// ===== F9: fidelidad, cada N visitas un servicio de regalo (inicio) =====

// LoyaltyProgramRepository: el programa de fidelidad del salón (migración 033,
// D3): a lo sumo UNA fila por salón. Toda consulta va scopeada por salon_id.
type LoyaltyProgramRepository interface {
	// Find lee el programa con el nombre del servicio de regalo. ErrNotFound si
	// el salón nunca lo guardó.
	Find(salonID string) (domain.LoyaltyProgram, error)
	// Save guarda el programa en UN upsert: el primer guardado fija
	// counting_from = p.CountingFrom y los siguientes LO CONSERVAN (D2/D4).
	// Devuelve el programa tal como quedó guardado, con el nombre del servicio.
	Save(p domain.LoyaltyProgram) (domain.LoyaltyProgram, error)
}

// LoyaltyRewardRepository: los vales entregados y las visitas de una clienta
// (D2, D5..D7). Toda consulta va scopeada por salon_id y client_id.
type LoyaltyRewardRepository interface {
	// Visits cuenta, con una consulta de SOLO LECTURA, las ventas de la clienta
	// con created_at >= since y al menos una línea de servicio en sale_items (una
	// venta solo de productos y la compra de un paquete no cuentan, D2).
	Visits(salonID, clientID string, since time.Time) (int, error)
	// CountGranted cuenta los vales ya entregados a la clienta (usados o no).
	CountGranted(salonID, clientID string) (int, error)
	// ListByClient trae los vales de la clienta, los más nuevos primero, con
	// tope MaxLoyaltyListed.
	ListByClient(salonID, clientID string) ([]domain.LoyaltyReward, error)
	// Grant entrega un vale en UNA transacción bajo un advisory lock por clienta:
	// recalcula visitas y entregados y aplica domain.NewLoyaltyGrant. UNIQUE
	// (client_id, reward_number) es la guarda final. Un client_op_id repetido
	// devuelve el MISMO vale sin entregar otro. 409 «La fidelidad no está
	// activada» / «No hay premios disponibles».
	Grant(r domain.LoyaltyGrantRequest) (domain.LoyaltyReward, error)
	// Redeem usa UN vale con un UPDATE condicional (no usado y no vencido en
	// r.Now): dos usos simultáneos => UNO gana. Sin filas afectadas distingue
	// ErrNotFound («Premio no encontrado»), usado y vencido (409 con su texto) en
	// la misma transacción. Un client_op_id repetido devuelve el estado actual
	// SIN usar otra vez.
	Redeem(r domain.LoyaltyRedeemRequest) (domain.LoyaltyReward, error)
}

// ===== F9: fidelidad, cada N visitas un servicio de regalo (fin) =====
