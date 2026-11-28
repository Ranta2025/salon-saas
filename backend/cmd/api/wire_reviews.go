package main

import (
	"salon-saas/internal/reviews"
	reviewsstore "salon-saas/internal/reviews/postgres"
)

// wireReviews arma las reseñas posvisita (F13).
func wireReviews(w *wiring) {
	// F13: reseñas posvisita (migración 038). Un solo adaptador implementa los
	// pedidos de reseña y las reseñas; el módulo SOLO LEE bookingRepo por su propia
	// consulta (D1) y no toca la agenda. El sender y APP_URL se completan abajo,
	// cuando el mailer existe: sin SMTP el barrido no pide nada.
	w.reviewsRepo = reviewsstore.Reviews{Pool: w.pool}
	w.reviewsSvc = reviews.Service{
		Reviews: w.reviewsRepo, Requests: w.reviewsRepo, Memberships: w.mems,
		AppURL: w.cfg.AppURL,
	}
}
