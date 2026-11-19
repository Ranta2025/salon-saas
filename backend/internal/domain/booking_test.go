package domain

import (
	"testing"
	"time"
)

func slot(day time.Time, h1, h2 int) (time.Time, time.Time) {
	return day.Add(time.Duration(h1) * time.Hour), day.Add(time.Duration(h2) * time.Hour)
}

func TestBooking_overlaps(t *testing.T) {
	day := time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)
	s1, e1 := slot(day, 10, 11)
	b, err := NewBooking("s", "prof1", "Lía", "555", s1, e1)
	if err != nil {
		t.Fatalf("inesperado: %v", err)
	}
	s2, e2 := slot(day, 10, 11)
	same, _ := NewBooking("s", "prof1", "Ana", "", s2, e2)
	if !b.Overlaps(same) {
		t.Fatal("mismo hueco debe solaparse")
	}
	s3, e3 := slot(day, 11, 12)
	ady, _ := NewBooking("s", "prof1", "Ana", "", s3, e3)
	if b.Overlaps(ady) {
		t.Fatal("fin==inicio no es solape (rangos [inicio,fin))")
	}
	other, _ := NewBooking("s", "prof2", "Ana", "", s2, e2)
	if b.Overlaps(other) {
		t.Fatal("distinto profesional no solapa")
	}
	if _, err := NewBooking("s", "prof1", "Ana", "", e1, s1); err == nil {
		t.Fatal("fin antes que inicio debe rechazarse")
	}
	if _, err := NewBooking("s", "prof1", "", "", s1, e1); err == nil {
		t.Fatal("cliente vacío debe rechazarse")
	}
}
