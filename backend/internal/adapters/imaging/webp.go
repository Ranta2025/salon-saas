// Package imaging es el adaptador de ports.ImageProcessor: normaliza la foto
// subida a WebP y acota el lado mayor para que la ficha del cliente no guarde
// (ni sirva) fotos de 8 MB.
//
// Se usa el binario `cwebp` (libwebp) en lugar de una librería Go: no hay
// encoder WebP puro en Go sin dependencias, y `cwebp` es lo que ya usa el
// pipeline del proyecto. Si mañana se quiere volver a puro Go, se cambia este
// archivo y nada más (el resto del sistema depende del puerto).
package imaging

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	_ "image/jpeg" // registra el decoder JPEG para DecodeConfig
	_ "image/png"  // registra el decoder PNG
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"salon-saas/internal/domain"
)

// WebP convierte con cwebp. MaxSide 0 = domain.MaxPhotoSide; Quality 0 = 82.
type WebP struct {
	MaxSide int
	Quality int
	// Bin permite fijar el binario (tests o entornos sin PATH); "" = cwebp.
	Bin string
}

// ToWebP devuelve la variante WebP y las dimensiones que tiene ESE archivo.
// Acepta jpg, png y webp; cualquier otra cosa es ErrValidation (un 400, no un
// 500): el usuario subió algo que no es una foto.
func (w WebP) ToWebP(data []byte) ([]byte, int, int, error) {
	if len(data) == 0 {
		return nil, 0, 0, fmt.Errorf("%w: archivo vacío", domain.ErrValidation)
	}
	width, height, err := imageDims(data)
	if err != nil {
		return nil, 0, 0, err
	}
	tw, th := fit(width, height, w.maxSide())
	out, err := w.convert(data, tw, th)
	if err != nil {
		return nil, 0, 0, err
	}
	if len(out) == 0 {
		return nil, 0, 0, fmt.Errorf("cwebp devolvió un archivo vacío")
	}
	// Las dimensiones que se informan son las del archivo resultante.
	gw, gh, err := webpDims(out)
	if err != nil {
		return nil, 0, 0, err
	}
	return out, gw, gh, nil
}

func (w WebP) maxSide() int {
	if w.MaxSide > 0 {
		return w.MaxSide
	}
	return domain.MaxPhotoSide
}

func (w WebP) quality() int {
	if w.Quality > 0 {
		return w.Quality
	}
	return 82
}

func (w WebP) binary() string {
	if w.Bin != "" {
		return w.Bin
	}
	return "cwebp"
}

// convert corre cwebp con archivos temporales del sistema (cwebp de esta
// versión no escribe a stdout) y devuelve el contenido del .webp.
func (w WebP) convert(data []byte, width, height int) ([]byte, error) {
	dir, err := os.MkdirTemp("", "salon-webp-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir) // temporal propio: se borra siempre

	src := filepath.Join(dir, "in")
	if err := os.WriteFile(src, data, 0o600); err != nil {
		return nil, err
	}
	dst := filepath.Join(dir, "out.webp")
	args := []string{"-quiet", "-mt", "-q", fmt.Sprint(w.quality())}
	args = append(args, "-resize", fmt.Sprint(width), fmt.Sprint(height))
	args = append(args, src, "-o", dst)

	cmd := exec.Command(w.binary(), args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("no se pudo convertir la imagen a webp: %s", msg)
	}
	return os.ReadFile(dst)
}

// fit calcula el tamaño destino manteniendo la proporción: el lado mayor queda
// en maxSide y el otro se escala. Nunca agranda.
func fit(width, height, maxSide int) (int, int) {
	longest := width
	if height > longest {
		longest = height
	}
	if maxSide <= 0 || longest <= maxSide {
		return width, height
	}
	if width >= height {
		nh := height * maxSide / width
		return maxSide, atLeastOne(nh)
	}
	nw := width * maxSide / height
	return atLeastOne(nw), maxSide
}

func atLeastOne(n int) int {
	if n < 1 {
		return 1
	}
	return n
}

// imageDims lee el tamaño sin decodificar la imagen entera (barato y sin
// cargar 8 MB de píxeles en memoria).
func imageDims(data []byte) (int, int, error) {
	switch http.DetectContentType(data) {
	case "image/jpeg", "image/png":
		cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil {
			return 0, 0, fmt.Errorf("%w: imagen ilegible", domain.ErrValidation)
		}
		return cfg.Width, cfg.Height, nil
	case "image/webp":
		return webpDims(data)
	default:
		return 0, 0, fmt.Errorf("%w: solo se aceptan imágenes jpg, png o webp", domain.ErrValidation)
	}
}

// webpDims parsea la cabecera de un WebP (VP8 lossy, VP8L lossless o VP8X
// extendido). La stdlib no decodifica webp, así que el tamaño se lee a mano.
func webpDims(b []byte) (int, int, error) {
	if len(b) < 30 || string(b[0:4]) != "RIFF" || string(b[8:12]) != "WEBP" {
		return 0, 0, fmt.Errorf("%w: no es un archivo webp", domain.ErrValidation)
	}
	chunk := string(b[12:16])
	payload := b[20:]
	switch chunk {
	case "VP8 ":
		// frame tag (3) + start code (3) + ancho (2) + alto (2), 14 bits cada uno
		if len(payload) < 10 {
			return 0, 0, fmt.Errorf("%w: cabecera webp truncada", domain.ErrValidation)
		}
		w := int(binary.LittleEndian.Uint16(payload[6:8]) & 0x3fff)
		h := int(binary.LittleEndian.Uint16(payload[8:10]) & 0x3fff)
		return w, h, nil
	case "VP8L":
		if len(payload) < 5 {
			return 0, 0, fmt.Errorf("%w: cabecera webp truncada", domain.ErrValidation)
		}
		bits := binary.LittleEndian.Uint32(payload[1:5])
		return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
	case "VP8X":
		if len(payload) < 10 {
			return 0, 0, fmt.Errorf("%w: cabecera webp truncada", domain.ErrValidation)
		}
		w := int(uint32(payload[4])|uint32(payload[5])<<8|uint32(payload[6])<<16) + 1
		h := int(uint32(payload[7])|uint32(payload[8])<<8|uint32(payload[9])<<16) + 1
		return w, h, nil
	default:
		return 0, 0, fmt.Errorf("%w: formato webp desconocido", domain.ErrValidation)
	}
}
