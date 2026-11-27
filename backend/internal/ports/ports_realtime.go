package ports

import "time"

// --- Realtime ---

// Event es lo que se publica a los clientes conectados de UN salón.
// Type viaja tal cual al frontend ("booking.created", "notify", ...) y Data es
// el payload que ya viene listo para serializar.
type Event struct {
	Type    string
	SalonID string
	Data    any
	TS      time.Time
}

// EventPublisher es el puerto de salida del realtime. Los casos de uso avisan
// "pasó algo en este salón" sin saber que atrás hay websockets (o mañana Redis
// pub/sub, o nada): el hub es un adaptador más.
type EventPublisher interface {
	Publish(ev Event) error
}
