package testutil

import (
	"sort"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeProfessionalServices dice qué servicios hace cada profesional, en memoria
// y con la misma semántica que pgstore.
//
// El estado inicial imita lo que deja la migración 016: un servicio sin filas
// grabadas lo hace TODO el equipo reservable del salón (el backfill). Recién
// cuando un test asigna (aunque sea una lista vacía) el filtro por servicio se
// comporta como la base real.
type FakeProfessionalServices struct {
	Staff     *FakeStaffLister
	ByService map[string][]string // "salonID|serviceID" -> userIDs
	ByMember  map[string][]string // "salonID|userID"    -> serviceIDs
}

func (f *FakeProfessionalServices) ListProfessionalsFor(salonID, serviceID string) ([]domain.StaffMember, error) {
	key := salonID + "|" + serviceID
	ids, recorded := f.ByService[key]
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	var out []domain.StaffMember
	for _, m := range f.Staff.BySalon[salonID] {
		if !m.Bookable() {
			continue
		}
		if recorded && !want[m.ID] {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *FakeProfessionalServices) ListServiceIDs(salonID, userID string) ([]string, error) {
	return append([]string{}, f.ByMember[salonID+"|"+userID]...), nil
}

func (f *FakeProfessionalServices) SetServices(salonID, userID string, serviceIDs []string) error {
	key := salonID + "|" + userID
	if _, ok := f.Staff.BySalon[salonID]; !ok {
		return domain.ErrNotFound
	}
	uniq := map[string]bool{}
	out := make([]string, 0, len(serviceIDs))
	for _, id := range serviceIDs {
		if id == "" || uniq[id] {
			continue
		}
		uniq[id] = true
		out = append(out, id)
	}
	sort.Strings(out)
	if f.ByMember == nil {
		f.ByMember = map[string][]string{}
	}
	if f.ByService == nil {
		f.ByService = map[string][]string{}
	}
	f.ByMember[key] = out
	// Lo asignado pasa a ser el registro del servicio para quien lo ve: así el
	// camino de disponibilidad y el de la pantalla de equipo miran lo mismo.
	for _, id := range out {
		filename := salonID + "|" + id
		already := false
		for _, who := range f.ByService[filename] {
			if who == userID {
				already = true
			}
		}
		if !already {
			f.ByService[filename] = append(f.ByService[filename], userID)
		}
	}
	return nil
}

// Assign graba qué profesionales hacen un servicio (lista vacía = nadie lo hace).
func (f *FakeProfessionalServices) Assign(salonID, serviceID string, userIDs ...string) {
	if f.ByService == nil {
		f.ByService = map[string][]string{}
	}
	f.ByService[salonID+"|"+serviceID] = userIDs
}

var _ ports.ProfessionalServiceRepository = (*FakeProfessionalServices)(nil)
