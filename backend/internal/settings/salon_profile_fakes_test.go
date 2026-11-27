package settings

import (
	"errors"
	"sort"
	"sync"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// fakeSalonProfiles imita a pgstore.SalonProfiles: scope por id de salón, el
// slug no se escribe, un salón inexistente es ErrNotFound.
type fakeSalonProfiles struct {
	rows  map[string]domain.SalonProfile
	saves int
}

func (f *fakeSalonProfiles) Profile(salonID string) (domain.SalonProfile, error) {
	p, ok := f.rows[salonID]
	if !ok {
		return domain.SalonProfile{}, domain.ErrNotFound
	}
	return p, nil
}

func (f *fakeSalonProfiles) SaveProfile(p domain.SalonProfile) error {
	cur, ok := f.rows[p.SalonID]
	if !ok {
		return domain.ErrNotFound
	}
	p.Slug = cur.Slug
	f.rows[p.SalonID] = p
	f.saves++
	return nil
}

var _ ports.SalonProfileRepository = (*fakeSalonProfiles)(nil)

// fakeGallery imita a pgstore.SalonGallery: tope bajo un lock, orden de subida y
// scope por salón. Los slugs se resuelven con `slugs` (id de salón -> slug).
type fakeGallery struct {
	mu      sync.Mutex
	All     []domain.SalonPhoto
	slugs   map[string]string
	addErr  error // simula que la foto 13 llegó justo después del chequeo previo
	adds    int
	deletes int
}

func (f *fakeGallery) Add(p domain.SalonPhoto, max int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.addErr != nil {
		return f.addErr
	}
	n := 0
	for _, x := range f.All {
		if x.SalonID == p.SalonID {
			n++
		}
	}
	if n >= max {
		return domain.NewMessage(domain.ErrAlreadyExists, domain.MsgGalleryFull)
	}
	f.All = append(f.All, p)
	f.adds++
	return nil
}

func (f *fakeGallery) Count(salonID string) (int, error) {
	n := 0
	for _, x := range f.All {
		if x.SalonID == salonID {
			n++
		}
	}
	return n, nil
}

func (f *fakeGallery) List(salonID string) ([]domain.SalonPhoto, error) {
	var out []domain.SalonPhoto
	for _, x := range f.All {
		if x.SalonID == salonID {
			out = append(out, x)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}

func (f *fakeGallery) Find(salonID, id string) (domain.SalonPhoto, error) {
	for _, x := range f.All {
		if x.SalonID == salonID && x.ID == id {
			return x, nil
		}
	}
	return domain.SalonPhoto{}, domain.ErrNotFound
}

func (f *fakeGallery) FindPublic(slug, id string) (domain.SalonPhoto, error) {
	for _, x := range f.All {
		if x.ID == id && f.slugs[x.SalonID] == slug {
			return x, nil
		}
	}
	return domain.SalonPhoto{}, domain.ErrNotFound
}

func (f *fakeGallery) SetCaption(salonID, id, caption string) error {
	for i := range f.All {
		if f.All[i].SalonID == salonID && f.All[i].ID == id {
			f.All[i].Caption = caption
			return nil
		}
	}
	return domain.ErrNotFound
}

func (f *fakeGallery) Delete(salonID, id string) error {
	for i := range f.All {
		if f.All[i].SalonID == salonID && f.All[i].ID == id {
			f.All = append(f.All[:i], f.All[i+1:]...)
			f.deletes++
			return nil
		}
	}
	return domain.ErrNotFound
}

var _ ports.SalonGalleryRepository = (*fakeGallery)(nil)

var errBoom = errors.New("boom")
