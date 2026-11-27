package security

import (
	"strings"
	"testing"
	"time"
)

// T6: la url de una foto se firma con vencimiento (24 h). Sin firma válida o
// vencida, el streaming responde 403.
func TestPhotoSigner(t *testing.T) {
	signer := NewPhotoSigner("un-secreto-de-pruebas-suficientemente-largo")
	exp := time.Now().Add(24 * time.Hour).Unix()

	sig := signer.Sign("foto-1", exp)
	if sig == "" {
		t.Fatal("la firma no puede ser vacía")
	}
	if strings.ContainsAny(sig, "+/=") {
		t.Fatalf("la firma debe ser segura para URL, dio %q", sig)
	}
	if !signer.Verify("foto-1", exp, sig) {
		t.Fatal("la firma recién emitida debe verificar")
	}
	// Otra foto, otro vencimiento o una firma retocada: 403.
	if signer.Verify("foto-2", exp, sig) {
		t.Fatal("la firma es del id, no de cualquier foto")
	}
	if signer.Verify("foto-1", exp+1, sig) {
		t.Fatal("la firma ata el vencimiento")
	}
	retocada := sig[:len(sig)-1] + string(sig[len(sig)-1]^1)
	if signer.Verify("foto-1", exp, retocada) {
		t.Fatal("una firma alterada no puede verificar")
	}
	if signer.Verify("foto-1", exp, "") {
		t.Fatal("sin firma no se sirve la foto")
	}
	// Vencida: la url deja de servir aunque la firma sea legítima.
	if signer.Verify("foto-1", time.Now().Add(-time.Second).Unix(), signer.Sign("foto-1", time.Now().Add(-time.Second).Unix())) {
		t.Fatal("una firma vencida no puede verificar")
	}
	// Otro secreto (otro proceso/instancia) no valida nuestras firmas.
	other := NewPhotoSigner("otro-secreto-distinto-y-suficientemente-largo")
	if other.Verify("foto-1", exp, sig) {
		t.Fatal("la clave de firma es del proceso")
	}
	// exp=0 no es una fecha válida (evita firmas eternas accidentales).
	if signer.Verify("foto-1", 0, signer.Sign("foto-1", 0)) {
		t.Fatal("exp=0 debe rechazarse")
	}
}
