package domain

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// S-F2e-2: nombres de equipo (trim, 1..60, sin saltos de línea) con los
// mensajes exactos del contrato.
func TestResourceType_name_validation(t *testing.T) {
	rt, err := NewResourceType("s1", "  Puesto de peluquería  ")
	if err != nil {
		t.Fatalf("nombre válido: %v", err)
	}
	if rt.Name != "Puesto de peluquería" || rt.SalonID != "s1" || rt.ID == "" {
		t.Fatalf("tipo mal armado: %+v", rt)
	}

	if _, err := NewResourceType("s1", "   "); err == nil || err.Error() != "El nombre del equipo es obligatorio" {
		t.Fatalf("vacío: %v", err)
	}
	if _, err := NewResourceType("s1", strings.Repeat("á", MaxResourceName+1)); err == nil ||
		err.Error() != "El nombre no puede superar los 60 caracteres" {
		t.Fatalf("largo: %v", err)
	}
	if _, err := NewResourceType("s1", "Puesto\nde peluquería"); !errors.Is(err, ErrValidation) {
		t.Fatalf("salto de línea: %v", err)
	}
	if _, err := NewResourceType("", "Puesto"); !errors.Is(err, ErrValidation) {
		t.Fatalf("sin salón: %v", err)
	}
}

// SetName revalida lo mismo que el alta: renombrar no puede saltear las reglas.
func TestResourceType_setName(t *testing.T) {
	rt, _ := NewResourceType("s1", "Silla")
	renamed, err := rt.SetName("  Silla de barbería ")
	if err != nil || renamed.Name != "Silla de barbería" || renamed.ID != rt.ID {
		t.Fatalf("renombrar: %+v %v", renamed, err)
	}
	if _, err := rt.SetName("   "); err == nil || err.Error() != "El nombre del equipo es obligatorio" {
		t.Fatalf("renombrar a vacío: %v", err)
	}
}

// S-F2e-2: límites con los mensajes del contrato (20 tipos, 50 unidades).
func TestResource_limits(t *testing.T) {
	if err := EnsureResourceTypeBudget(MaxResourceTypes - 1); err != nil {
		t.Fatalf("19 tipos todavía entran: %v", err)
	}
	if err := EnsureResourceTypeBudget(MaxResourceTypes); err == nil || err.Error() != "Máximo 20 tipos de equipo" {
		t.Fatalf("21º tipo: %v", err)
	}
	if err := EnsureResourceUnitBudget(0, MaxResourceUnitsPerType); err != nil {
		t.Fatalf("50 unidades en un tipo vacío entran: %v", err)
	}
	if err := EnsureResourceUnitBudget(1, MaxResourceUnitsPerType); err == nil || err.Error() != "Máximo 50 unidades por tipo" {
		t.Fatalf("51ª unidad: %v", err)
	}
	if err := EnsureResourceUnitBudget(10, 0); !errors.Is(err, ErrValidation) {
		t.Fatalf("agregar 0 unidades: %v", err)
	}
}

// Unidades: nombre propio, activo por defecto, y quitar equipo = desactivar.
func TestResourceUnit_details(t *testing.T) {
	u, err := NewResourceUnit("s1", "t1", "  Puesto 1 ")
	if err != nil || u.Name != "Puesto 1" || !u.Active || u.TypeID != "t1" {
		t.Fatalf("unidad: %+v %v", u, err)
	}
	if _, err := NewResourceUnit("s1", "t1", " "); err == nil || err.Error() != "El nombre del equipo es obligatorio" {
		t.Fatalf("unidad sin nombre: %v", err)
	}
	// name vacío = no se cambia; active=false = desactivada.
	off, err := u.SetDetails("", false)
	if err != nil || off.Active || off.Name != "Puesto 1" {
		t.Fatalf("desactivar: %+v %v", off, err)
	}
	renamed, err := u.SetDetails(" Puesto A ", true)
	if err != nil || renamed.Name != "Puesto A" || !renamed.Active {
		t.Fatalf("renombrar unidad: %+v %v", renamed, err)
	}
}

// S-F2e-4: una unidad está libre si NINGÚN intervalo ocupado la pisa. Los
// extremos que se tocan no se pisan (mismo criterio que la EXCLUDE).
func TestFreeUnits(t *testing.T) {
	units := []ResourceUnit{
		{ID: "u1", TypeID: "t1", Active: true},
		{ID: "u2", TypeID: "t1", Active: true},
		{ID: "u3", TypeID: "t1", Active: false},
	}
	base := time.Date(2026, 12, 2, 15, 0, 0, 0, time.UTC)
	busy := []UnitOccupancy{
		{UnitID: "u1", StartsAt: base, EndsAt: base.Add(time.Hour)},
	}
	free := FreeUnits(units, busy, base, base.Add(time.Hour))
	if len(free) != 1 || free[0].ID != "u2" {
		t.Fatalf("solo u2 está libre (u3 está desactivada): %+v", free)
	}
	// Empieza justo cuando u1 termina: cabe.
	free = FreeUnits(units, busy, base.Add(time.Hour), base.Add(2*time.Hour))
	if len(free) != 2 {
		t.Fatalf("u1 se libera al terminar su turno: %+v", free)
	}
	if !UnitFree(busy, "u2", base, base.Add(time.Hour)) {
		t.Fatal("u2 no tiene turnos: está libre")
	}
	if UnitFree(busy, "u1", base.Add(30*time.Minute), base.Add(90*time.Minute)) {
		t.Fatal("u1 está tomada: no puede estar libre")
	}
}

// La unidad de un turno ocupado tampoco se puede dar a otro en el mismo rango;
// una unidad ocupada pero en otro horario sí.
func TestFreeUnits_durations(t *testing.T) {
	units := []ResourceUnit{{ID: "u1", Active: true}}
	day := time.Date(2026, 12, 2, 0, 0, 0, 0, time.UTC)
	// Un servicio de 3 horas ocupa la unidad de 10:00 a 13:00.
	busy := []UnitOccupancy{{UnitID: "u1", StartsAt: day.Add(10 * time.Hour), EndsAt: day.Add(13 * time.Hour)}}
	if got := FreeUnits(units, busy, day.Add(11*time.Hour), day.Add(11*time.Hour+30*time.Minute)); len(got) != 0 {
		t.Fatalf("la unidad está ocupada a las 11: %+v", got)
	}
	if got := FreeUnits(units, busy, day.Add(13*time.Hour), day.Add(13*time.Hour+30*time.Minute)); len(got) != 1 {
		t.Fatalf("a las 13 está libre: %+v", got)
	}
}
