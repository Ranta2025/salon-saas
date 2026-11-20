package imaging

import (
	"bytes"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os/exec"
	"testing"

	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

var _ ports.ImageProcessor = WebP{}

func requireCwebp(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("cwebp"); err != nil {
		if _, statErr := exec.LookPath("/home/linuxbrew/.linuxbrew/bin/cwebp"); statErr != nil {
			t.Skip("cwebp no está instalado: no hay conversión real que probar")
		}
	}
}

// pngBytes arma un PNG w x h en memoria con un patrón simple.
func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 120, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png: %v", err)
	}
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("jpeg: %v", err)
	}
	return buf.Bytes()
}

func isWebP(b []byte) bool {
	return len(b) > 12 && string(b[0:4]) == "RIFF" && string(b[8:12]) == "WEBP"
}

// La variante que se guarda es WebP y su lado mayor no pasa de 1280.
func TestWebP_convierte_y_acota_el_lado_mayor(t *testing.T) {
	requireCwebp(t)
	conv := WebP{MaxSide: domain.MaxPhotoSide}
	out, w, h, err := conv.ToWebP(jpegBytes(t, 1600, 1200))
	if err != nil {
		t.Fatalf("convertir jpeg: %v", err)
	}
	if !isWebP(out) {
		t.Fatalf("no es webp: %q", out[:min(16, len(out))])
	}
	if w != 1280 || h != 960 {
		t.Fatalf("esperaba 1280x960 (mantiene la proporción), dio %dx%d", w, h)
	}
	// Las dimensiones informadas son las reales del archivo servido.
	hw, hh, err := webpDims(out)
	if err != nil || hw != w || hh != h {
		t.Fatalf("dimensiones del webp resultante: %dx%d (%v), informé %dx%d", hw, hh, err, w, h)
	}
	if len(out) >= len(pngBytes(t, 1600, 1200)) {
		t.Fatal("la variante webp no debería ser más grande que el original")
	}
}

// Una foto chica no se agranda: solo se re-encodea.
func TestWebP_no_agranda(t *testing.T) {
	requireCwebp(t)
	conv := WebP{MaxSide: 1280}
	out, w, h, err := conv.ToWebP(pngBytes(t, 800, 600))
	if err != nil {
		t.Fatalf("convertir png: %v", err)
	}
	if !isWebP(out) || w != 800 || h != 600 {
		t.Fatalf("800x600 debe quedar igual, dio %dx%d", w, h)
	}
}

// El lado mayor se acota también cuando el formato de entrada ya es webp.
func TestWebP_reprocesa_webp_de_entrada(t *testing.T) {
	requireCwebp(t)
	conv := WebP{MaxSide: 640}
	first, _, _, err := conv.ToWebP(pngBytes(t, 640, 320))
	if err != nil {
		t.Fatalf("primera: %v", err)
	}
	// Re-entrada webp: el adaptador la acepta y la deja dentro del límite.
	out, w, h, err := conv.ToWebP(first)
	if err != nil {
		t.Fatalf("reprocesar webp: %v", err)
	}
	if !isWebP(out) || w != 640 || h != 320 {
		t.Fatalf("esperaba 640x320, dio %dx%d", w, h)
	}
}

func TestWebP_rechaza_lo_que_no_es_imagen(t *testing.T) {
	requireCwebp(t)
	conv := WebP{MaxSide: 1280}
	for _, data := range [][]byte{
		[]byte("hola, no soy una foto"),
		[]byte("%PDF-1.7 no soy una imagen"),
		{},
	} {
		if _, _, _, err := conv.ToWebP(data); !errors.Is(err, domain.ErrValidation) {
			t.Fatalf("%q debe dar ErrValidation, dio %v", data, err)
		}
	}
}

// Parser de cabeceras webp (VP8 / VP8L / VP8X): sin él no se pueden acotar las
// dimensiones de una entrada que ya viene en webp (stdlib no decodifica webp).
func TestWebPDims(t *testing.T) {
	if w, h, err := webpDims([]byte("no soy webp")); err == nil || w != 0 {
		t.Fatalf("basura debe fallar: %dx%d %v", w, h, err)
	}

	// VP8 (lossy): frame tag + start code + 14 bits de ancho y alto.
	vp8 := make([]byte, 30)
	copy(vp8[0:], "RIFF")
	copy(vp8[8:], "WEBP")
	copy(vp8[12:], "VP8 ")
	vp8[20], vp8[21], vp8[22] = 0x00, 0x00, 0x00 // frame tag
	vp8[23], vp8[24], vp8[25] = 0x9d, 0x01, 0x2a
	vp8[26], vp8[27] = byte(640&0xff), byte(640>>8)
	vp8[28], vp8[29] = byte(480&0xff), byte(480>>8)
	if w, h, err := webpDims(vp8); err != nil || w != 640 || h != 480 {
		t.Fatalf("VP8: %dx%d %v", w, h, err)
	}

	// VP8L (lossless): signature 0x2f + 14 bits (ancho-1) + 14 bits (alto-1).
	vp8l := make([]byte, 30)
	copy(vp8l[0:], "RIFF")
	copy(vp8l[8:], "WEBP")
	copy(vp8l[12:], "VP8L")
	vp8l[20] = 0x2f
	w, h := 100, 50
	bits := uint32(w-1) | uint32(h-1)<<14
	vp8l[21], vp8l[22], vp8l[23], vp8l[24] = byte(bits), byte(bits>>8), byte(bits>>16), byte(bits>>24)
	if gw, gh, err := webpDims(vp8l); err != nil || gw != w || gh != h {
		t.Fatalf("VP8L: %dx%d %v (esperaba %dx%d)", gw, gh, err, w, h)
	}

	// VP8X (extendido): canvas 24 bits (tamaño-1) después de los flags.
	vp8x := make([]byte, 30)
	copy(vp8x[0:], "RIFF")
	copy(vp8x[8:], "WEBP")
	copy(vp8x[12:], "VP8X")
	vp8x[20] = 0x00
	cw, ch := 1920, 1080
	for i := 0; i < 3; i++ {
		vp8x[24+i] = byte((cw - 1) >> (8 * i))
		vp8x[27+i] = byte((ch - 1) >> (8 * i))
	}
	if gw, gh, err := webpDims(vp8x); err != nil || gw != cw || gh != ch {
		t.Fatalf("VP8X: %dx%d %v (esperaba %dx%d)", gw, gh, err, cw, ch)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TestWebP_rechaza_bomba_de_pixeles: un PNG de pocos bytes que declara
// dimensiones gigantes (100000x100000) debe rechazarse por validación ANTES
// de pasarlo a cwebp. Sin la guarda, cwebp intentaría decodificar 10 gigapíxeles
// (OOM o minutos de CPU por request). Solo se escribe la cabecera IHDR: basta
// para que cualquier decoder crea que la imagen es enorme.
func TestWebP_rechaza_bomba_de_pixeles(t *testing.T) {
	data := pngCabeceraGigante(t, 100000, 100000)
	if len(data) > 1<<20 {
		t.Fatalf("el fixture debe ser chico, pesa %d", len(data))
	}
	_, _, _, err := WebP{}.ToWebP(data)
	if !errors.Is(err, domain.ErrValidation) {
		t.Fatalf("bomba de píxeles debe dar ErrValidation, dio %v", err)
	}
}

// pngCabeceraGigante arma un PNG válido de solo-cabecera con las dimensiones
// pedidas (firma + IHDR con CRC correcto + IEND). image.DecodeConfig lo lee
// sin necesitar los píxeles.
func pngCabeceraGigante(t *testing.T, w, h uint32) []byte {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("\x89PNG\r\n\x1a\n")
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:4], w)
	binary.BigEndian.PutUint32(ihdr[4:8], h)
	ihdr[8] = 8 // bit depth
	ihdr[9] = 2 // truecolor
	escribirChunk(&buf, "IHDR", ihdr)
	escribirChunk(&buf, "IEND", nil)
	return buf.Bytes()
}

func escribirChunk(buf *bytes.Buffer, tipo string, datos []byte) {
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(datos)))
	buf.Write(l[:])
	buf.WriteString(tipo)
	buf.Write(datos)
	crc := crc32.ChecksumIEEE(append([]byte(tipo), datos...))
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc)
	buf.Write(c[:])
}
