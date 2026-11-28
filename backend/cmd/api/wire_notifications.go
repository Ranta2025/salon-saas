package main

import (
	"salon-saas/internal/adapters/realtime"
	notifsvc "salon-saas/internal/notifications"
	notifstore "salon-saas/internal/notifications/postgres"
)

// wireNotifications arma el hub de tiempo real y la bandeja de avisos.
func wireNotifications(w *wiring) {
	// Realtime: el hub es el EventPublisher de los casos de uso (no saben que
	// hay websockets). Autentica el JWT del query param, valida el Origin con la
	// MISMA allowlist de CORS (B-01) y arma una room por salón.
	w.hub = realtime.NewHub(w.tokens, w.mems, realtime.HubOrigins(w.cfg.CORSOrigins...), realtime.HubSessionGate(w.sessionGate))
	notifications := notifstore.Notifications{Pool: w.pool}
	w.notifSvc = notifsvc.NotificationService{Notifications: notifications, Salons: w.mems, Events: w.hub}
}
