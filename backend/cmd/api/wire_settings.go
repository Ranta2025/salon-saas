package main

import (
	"salon-saas/internal/adapters/imaging"
	"salon-saas/internal/domain"
	"salon-saas/internal/settings"
	settingspostgres "salon-saas/internal/settings/postgres"
)

// wireSettings arma la configuración del salón, su perfil público, la galería y
// la vidriera (F16).
func wireSettings(w *wiring) {
	// Settings: los avisos de turno se formatean en la zona horaria del salón.
	w.settingsSvc = settings.SettingsService{Settings: w.salons, Memberships: w.mems}
	// F16: perfil público del salón (migración 032) y su galería de fotos. La
	// galería reutiliza el MISMO storage y el MISMO conversor a WebP que las fotos
	// de clientas, pero en su propia tabla: la ruta pública nunca sirve una foto
	// de clienta.
	w.salonProfileRepo = settingspostgres.SalonProfiles{Pool: w.pool}
	w.salonGalleryRepo = settingspostgres.SalonGallery{Pool: w.pool}
	w.salonProfileSvc = settings.SalonProfileService{Profiles: w.salonProfileRepo, Memberships: w.mems}
	w.gallerySvc = settings.GalleryService{
		Gallery: w.salonGalleryRepo, Memberships: w.mems, Salons: w.salons,
		Photos: w.store, Images: imaging.WebP{MaxSide: domain.MaxPhotoSide},
	}
	// F16 (D4): la vidriera sale del panel: perfil, equipo que atiende, galería y
	// paquetes activos, y la carta se filtra con la MISMA regla que la
	// disponibilidad (ListProfessionalsFor).
	w.publicSvc = settings.PublicSalonService{
		Salons: w.salons, Services: w.servicesRepo, Settings: w.salons, Hours: w.weeklyHours,
		Profiles: w.salonProfileRepo, Staff: w.mems, Professionals: w.professionals,
		Gallery: w.salonGalleryRepo, Packages: w.packagesRepo, Loyalty: w.loyaltyProgramsRepo,
	}
}
