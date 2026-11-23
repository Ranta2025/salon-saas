package app

import (
	"testing"

	"salon-saas/internal/domain"
)

// La marca `estimated` avisa que la COMISIÓN se calculó con el porcentaje actual
// porque alguna venta no tenía foto. Si la persona no cobra comisión (fijo puro)
// no hay nada estimado: la comisión es 0 y el badge «estimado» sería engañoso
// (hallazgo del verificador independiente de F3.2).
func TestPayroll_estimated_no_se_marca_si_la_comision_esta_apagada(t *testing.T) {
	env := newPayrollEnv(t)
	env.sales.add("s-pay", domain.PayrollSalesTotal{
		UserID: "u-prof", Name: "Ana", SalesCount: 3, SalesTotalCents: 3499,
		CommissionBase: 49990, Estimated: true,
	})
	off, _ := domain.NewMemberPay("s-pay", "u-prof", 100000, "month", false)
	if err := env.pay.SavePayModel(off); err != nil {
		t.Fatalf("modelo de Ana: %v", err)
	}

	prev, err := env.svc.Preview(env.actor(t, "u-owner"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	var ana PayrollLine
	for _, l := range prev.Lines {
		if l.UserID == "u-prof" {
			ana = l
		}
	}
	if ana.CommissionCents != 0 || ana.SalesCount != 3 || ana.SalesTotalCents != 3499 {
		t.Fatalf("sin comisión, las ventas igual cuentan: %+v", ana)
	}
	if ana.Estimated {
		t.Fatalf("la comisión está apagada: la línea no puede ser «estimada»: %+v", ana)
	}

	me, err := env.svc.Me(env.actor(t, "u-prof"), "s-pay", "month", "2026-03-10")
	if err != nil {
		t.Fatalf("me: %v", err)
	}
	if me.CommissionCents != 0 || me.Estimated {
		t.Fatalf("mis comisiones con la comisión apagada: %+v", me)
	}

	// Y con la comisión encendida la marca sigue como siempre.
	on, _ := domain.NewMemberPay("s-pay", "u-prof", 0, "month", true)
	if err := env.pay.SavePayModel(on); err != nil {
		t.Fatalf("modelo de Ana (encendido): %v", err)
	}
	me, _ = env.svc.Me(env.actor(t, "u-prof"), "s-pay", "month", "2026-03-10")
	if me.CommissionCents != 499 || !me.Estimated {
		t.Fatalf("con la comisión encendida sigue estimada: %+v", me)
	}
}
