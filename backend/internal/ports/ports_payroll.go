package ports

import (
	"salon-saas/internal/domain"
	"time"
)

// --- Nómina y comisiones (F3.2 del contrato F3) ---

// MemberPayRepository: el modelo de pago de cada miembro del salón (D3). Sin
// fila rige domain.DefaultMemberPay (solo comisión), así que la ausencia de
// datos es comportamiento, no un error.
//
// Todas las queries van scopeadas por salon_id: el modelo de pago de otro
// tenant no se lista, no se encuentra y no se escribe (ErrNotFound -> 404).
type MemberPayRepository interface {
	// ListPayModels trae el modelo de CADA miembro del salón —activos y dados de
	// baja— con nombre, rol y estado. Es la lectura de la pantalla de Equipo y la
	// que arma las líneas de la vista previa.
	ListPayModels(salonID string) ([]domain.MemberPayView, error)
	// FindPayModel resuelve una persona del salón. Una persona que no es miembro
	// es ErrNotFound; un miembro sin fila devuelve el default de D3.
	FindPayModel(salonID, userID string) (domain.MemberPayView, error)
	// SavePayModel inserta o actualiza el modelo. La FK compuesta a
	// memberships(user_id, salon_id) es la autorización: una persona de otro
	// salón no tiene dónde guardarse.
	SavePayModel(p domain.MemberPay) error
}

// PayrollSalesRepository: los agregados de ventas por profesional de una
// ventana, con el NUMERADOR de la comisión (Σ qty × precio × pct, D2). El
// adaptador suma en enteros (bigint) y domain hace la única división.
//
// La foto del porcentaje (sale_items.commission_pct) manda; una línea anterior a
// F3.1 (NULL) se estima con la tarifa ACTUAL del servicio y marca la fila.
type PayrollSalesRepository interface {
	// CommissionTotals agrupa por profesional las ventas del salón en [from,to).
	// Un profesional sin ventas no aparece; uno con ventas siempre aparece, aunque
	// ya no esté en el equipo.
	CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error)
	// CommissionTotalFor es el mismo agregado para UNA persona: es la única
	// lectura de «Mis comisiones» (nunca trae los números de otra).
	// Sin ventas devuelve el total en cero, no ErrNotFound.
	CommissionTotalFor(salonID, userID string, from, to time.Time) (domain.PayrollSalesTotal, error)
}

// --- Liquidaciones de nómina (F3.3, D6 del contrato F3) ---

// PayrollRunRepository: las liquidaciones del salón. La cabecera congela el
// período y las líneas congelan los montos de cada persona; toda consulta va
// scopeada por salon_id (una liquidación de otro tenant no existe acá).
type PayrollRunRepository interface {
	// ListRuns trae las liquidaciones del salón, la más nueva primero (por
	// from_date desc), con la cantidad de líneas y los totales.
	ListRuns(salonID string) ([]domain.PayrollRunSummary, error)
	// FindRun trae una liquidación con sus líneas. Inexistente o de otro salón
	// es ErrNotFound (404), sin distinguir cuál de las dos cosas pasó.
	FindRun(salonID, runID string) (domain.PayrollRun, error)
}

// PayrollRunTx es la vista TRANSACCIONAL de las liquidaciones. Cada escritura
// lleva la condición de que la corrida siga ABIERTA: la inmutabilidad de una
// pagada la sostiene la DB y no solo la API.
type PayrollRunTx interface {
	// SaveRun inserta la cabecera y TODAS sus líneas. La EXCLUDE por salón es la
	// que rechaza días ya cubiertos: ErrAlreadyExists (409), nunca un 500.
	SaveRun(run domain.PayrollRun) error
	// FindRun lee la corrida recién escrita (o la que se está por pagar) dentro
	// de la misma transacción.
	FindRun(salonID, runID string) (domain.PayrollRun, error)
	// AdjustLine cambia el ajuste y la nota de una línea SOLO si la corrida está
	// abierta y la línea existe. Pagada -> ErrAlreadyExists; sin línea o sin
	// corrida -> ErrNotFound.
	AdjustLine(salonID, runID, userID string, adjustmentCents int, note string) error
	// PayRun marca la corrida como pagada SOLO si está abierta. Pagada (o un
	// segundo pago simultáneo) -> ErrAlreadyExists; inexistente -> ErrNotFound.
	PayRun(salonID, runID, paidBy string) error
	// DeleteRun borra la corrida (y sus líneas en cascada) SOLO si está abierta,
	// liberando los días. Pagada -> ErrAlreadyExists.
	DeleteRun(salonID, runID string) error
	// Settings lee la configuración del salón DENTRO de la transacción, ya con
	// el advisory lock por salón tomado: la creación deriva el período con la
	// zona que vale en ese instante, nunca con una que el PUT de settings está
	// cambiando en paralelo. Fila ausente -> ErrNotFound (el caso de uso aplica
	// los defaults, igual que la lectura normal).
	Settings(salonID string) (domain.SalonSettings, error)
	// PreviewSource da las lecturas de la vista previa sobre la MISMA conexión
	// de la transacción. La creación ya tiene el advisory lock tomado: si estas
	// lecturas pidieran otra conexión del pool, muchas creaciones simultáneas
	// del mismo salón podrían agotarlo y esperarse entre sí.
	PreviewSource() PayrollPreviewSource
}

// PayrollPreviewSource: lo que el motor de la vista previa necesita leer de la
// nómina (modelos de pago y agregados de ventas) DENTRO de la transacción de una
// liquidación. Es un subconjunto de MemberPayRepository/PayrollSalesRepository.
type PayrollPreviewSource interface {
	// ListPayModels trae el modelo de pago de cada miembro del salón.
	ListPayModels(salonID string) ([]domain.MemberPayView, error)
	// CommissionTotals agrupa por profesional las ventas del salón en [from,to).
	CommissionTotals(salonID string, from, to time.Time) ([]domain.PayrollSalesTotal, error)
}

// PayrollTxRunner ejecuta una escritura de liquidaciones en UNA transacción que
// primero toma un advisory lock por salón (prefijo "payroll-write:"). El lock
// serializa las creaciones del mismo salón; la EXCLUDE es la red de seguridad.
type PayrollTxRunner interface {
	InPayrollTx(salonID string, fn func(tx PayrollRunTx) error) error
}
