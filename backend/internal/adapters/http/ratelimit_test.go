package httpapi

import (
	"fmt"
	"testing"
	"time"
)

// ipDe arma IPs distintas para llenar el mapa del limiter.
func ipDe(i int) string {
	return fmt.Sprintf("198.51.%d.%d", (i/256)%256, i%256)
}

// H2: los cubos vencidos se desalojan. Antes el mapa crecía con cada IP nueva
// (un cliente que no vuelve deja su cubo para siempre) y la memoria la elegía
// el atacante.
func TestIPRateLimiter_desaloja_cubos_vencidos(t *testing.T) {
	rl := NewIPRateLimiter(10, 10*time.Millisecond)

	for i := 0; i < purgeEvery; i++ {
		rl.Allow(ipDe(i))
	}
	if len(rl.hits) != purgeEvery {
		t.Fatalf("antes de la purga el mapa tiene una entrada por IP: %d", len(rl.hits))
	}

	// Nadie vuelve: la ventana vence y el barrido de la próxima purga los saca.
	time.Sleep(40 * time.Millisecond)
	for i := 0; i < purgeEvery; i++ {
		rl.Allow("203.0.113.200")
	}
	if len(rl.hits) != 1 {
		t.Fatalf("los cubos vencidos deben desalojarse: quedan %d entradas (esperaba 1)", len(rl.hits))
	}
	if _, ok := rl.hits["203.0.113.200"]; !ok {
		t.Fatal("el cubo de la IP que sigue activa no se puede borrar")
	}
}

// H2: con muchas IPs distintas el mapa no crece sin límite: se mantiene del
// orden de las IPs de una ventana, no del total histórico.
func TestIPRateLimiter_no_crece_con_ip_nuevas(t *testing.T) {
	rl := NewIPRateLimiter(10, 10*time.Millisecond)

	for ronda := 0; ronda < 3; ronda++ {
		for i := 0; i < purgeEvery; i++ {
			rl.Allow(ipDe(ronda*purgeEvery + i))
		}
		if len(rl.hits) > purgeEvery {
			t.Fatalf("ronda %d: el mapa creció a %d entradas para %d IPs históricas", ronda, len(rl.hits), (ronda+1)*purgeEvery)
		}
		// Entre rondas la ventana vence: la purga de la ronda siguiente limpia.
		time.Sleep(40 * time.Millisecond)
	}
}

// PRESERVE: la semántica del limiter no cambia con la purga: N intentos por
// ventana, la IP ajena no se limita y la ventana vencida vuelve a habilitar.
func TestIPRateLimiter_sigue_limitando_por_ventana(t *testing.T) {
	rl := NewIPRateLimiter(2, 30*time.Millisecond)
	ip := "192.0.2.10"

	if !rl.Allow(ip) || !rl.Allow(ip) {
		t.Fatal("los primeros intentos deben pasar")
	}
	if rl.Allow(ip) {
		t.Fatal("el tercer intento dentro de la ventana debe rechazarse")
	}
	if !rl.Allow("192.0.2.11") {
		t.Fatal("otra IP no está limitada")
	}
	time.Sleep(60 * time.Millisecond)
	if !rl.Allow(ip) {
		t.Fatal("pasada la ventana la IP vuelve a tener intentos")
	}
}
