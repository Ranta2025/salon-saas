// Package deposits: la SEÑA online con banco simulado (F11) — política del
// salón, alta de la seña de una reserva, callbacks del banco, barrido de vencidas
// y reembolsos.
//
// Es un módulo por funcionalidad: los adaptadores viven en deposits/postgres y
// deposits/httpapi, el banco simulado en internal/simbank y el cableado en
// cmd/api. No importa internal/bookings: las tres cosas que necesita de la
// agenda (cancelar el turno, el aviso del turno y los enlaces firmados) son
// puertos de internal/ports.
package deposits
