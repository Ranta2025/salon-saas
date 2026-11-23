package domain

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Tope del dinero en las líneas de una liquidación (F3.3). El CHECK de la
// migración 025 ya frena un total fuera de rango, pero hasta F3.3 un ajuste que
// empujaba el total por encima de MaxPayCents pasaba el dominio y llegaba a la
// DB: la persona veía el genérico "un dato del pedido está fuera del rango
// permitido" en vez de una frase clara. El dominio tiene que frenarlo ANTES y
// con una sola frase, en buen español, para los dos caminos (ajuste y creación).

// wantOverCap es la frase EXACTA (literal, no la constante del código: así el
// test no puede pasar por copiar el error) y no puede quedar desincronizada del
// máximo real.
const wantOverCap = "El total de la línea no puede superar los 2000000000 centavos"

func TestPayrollTotalOverCap_casa_con_el_maximo(t *testing.T) {
	want := fmt.Sprintf("El total de la línea no puede superar los %d centavos", MaxPayCents)
	if wantOverCap != want {
		t.Fatalf("la frase del tope = %q, esperaba %q", wantOverCap, want)
	}
}

// WithAdjustment: comisión + fijo + ajuste nunca supera MaxPayCents. El tope
// exacto se acepta; un centavo más es 400 con la frase clara.
func TestPayrollLine_WithAdjustment_no_supera_el_tope(t *testing.T) {
	line := PayrollLine{
		UserID: "u-a", Name: "Ana",
		CommissionCents: MaxPayCents, TotalCents: MaxPayCents,
	}
	got, err := line.WithAdjustment(0, "")
	if err != nil {
		t.Fatalf("el total en el tope exacto debe aceptarse: %v", err)
	}
	if got.TotalCents != MaxPayCents {
		t.Fatalf("total = %d, esperaba %d", got.TotalCents, MaxPayCents)
	}

	for _, adj := range []int{1, MaxPayCents} {
		out, err := line.WithAdjustment(adj, "de más")
		if err == nil {
			t.Fatalf("ajuste %d debía rechazarse (total %d)", adj, out.TotalCents)
		}
		if !errors.Is(err, ErrValidation) || err.Error() != wantOverCap {
			t.Fatalf("ajuste %d: err = %v, esperaba %q", adj, err, wantOverCap)
		}
	}
	// Y un total justo por debajo del tope sigue entrando.
	line2 := PayrollLine{UserID: "u-b", Name: "Beto", CommissionCents: MaxPayCents - 1, TotalCents: MaxPayCents - 1}
	if _, err := line2.WithAdjustment(1, "uno más"); err != nil {
		t.Fatalf("el total en el tope debe aceptarse: %v", err)
	}
}

// NewPayrollRun (la foto de la vista previa) también rechaza un total fuera de
// rango con la MISMA frase: no puede quedar el mensaje viejo "el total no
// pueden superar…" ni llegar al CHECK de la DB.
func TestNewPayrollRun_total_sobre_el_tope(t *testing.T) {
	p := PayPeriod{Kind: PayMonth,
		From: time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC),
		To:   time.Date(2026, 2, 28, 0, 0, 0, 0, time.UTC)}
	now := time.Date(2026, 3, 2, 12, 0, 0, 0, time.UTC)
	over := []PayrollLine{{
		UserID: "u-a", Name: "Ana",
		CommissionCents: MaxPayCents, FixedCents: 1,
		TotalCents: MaxPayCents + 1,
	}}
	_, err := NewPayrollRun("run-1", "s-1", p, "u-owner", now, over)
	if err == nil {
		t.Fatal("un total sobre el tope debía rechazarse")
	}
	if !errors.Is(err, ErrValidation) || err.Error() != wantOverCap {
		t.Fatalf("err = %v, esperaba %q", err, wantOverCap)
	}

	// El tope exacto se acepta.
	atCap := []PayrollLine{{
		UserID: "u-a", Name: "Ana",
		CommissionCents: MaxPayCents - 1, FixedCents: 1,
		TotalCents: MaxPayCents,
	}}
	if _, err := NewPayrollRun("run-2", "s-1", p, "u-owner", now, atCap); err != nil {
		t.Fatalf("el total en el tope debe aceptarse: %v", err)
	}
}
