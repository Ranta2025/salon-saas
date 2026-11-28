package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// demoAsset es una foto versionada en scripts/demo-assets. El seed NO descarga
// nada: lee estos archivos (o los encuentra montados en /assets dentro del
// stack de Docker) y los sube por la API, que se encarga de normalizarlos.
type demoAsset struct {
	// File es el nombre del archivo dentro de la carpeta de assets.
	File string
	// Caption es la leyenda que ve el público en la galería.
	Caption string
}

// galleryAssets es la galería pública del salón, en el orden en que se muestra.
var galleryAssets = []demoAsset{
	{File: "salon-interior.webp", Caption: "El salón: tres puestos con espejo redondo y luz cálida."},
	{File: "corte-sillon.webp", Caption: "Corte y barba en el sillón de siempre."},
	{File: "manicura-torno.webp", Caption: "Manicura con torno: prolijidad de cerca."},
	{File: "manicura-esmaltado.webp", Caption: "Esmaltado semipermanente bajo la lámpara."},
	{File: "manicura-lima.webp", Caption: "Lima y pulido antes del color."},
	{File: "manicura-torno-rosa.webp", Caption: "Detalle del trabajo con torno."},
}

// clientPhotoAssets son las fotos de dos fichas: el corte y una manicura.
var clientPhotoAssets = []demoAsset{
	{File: "corte-sillon.webp"},
	{File: "manicura-detalle.webp"},
}

// maxAssetBytes es el tope por archivo: la galería se sirve a 1200 px y cada
// foto tiene que pesar menos de 200 KB.
const maxAssetBytes = 200 * 1024

// assetDirCandidates son las ubicaciones donde el seed busca las fotos, en
// orden: la imagen de Docker monta la carpeta en /assets; desde backend/ es
// ../scripts/demo-assets; desde la raíz del repo, scripts/demo-assets.
var assetDirCandidates = []string{
	"/assets",
	"assets",
	"../scripts/demo-assets",
	"scripts/demo-assets",
	"../../scripts/demo-assets",
	"../../../scripts/demo-assets",
}

// AssetsDir resuelve la carpeta de fotos: si hay una explícita se usa esa (y si
// no existe es error, no se adivina), si no se prueba la lista de candidatas.
func AssetsDir(explicit string) (string, error) {
	if explicit != "" {
		if !isAssetDir(explicit) {
			return "", fmt.Errorf("seed: SEED_ASSETS_DIR=%q no tiene las fotos de la demo", explicit)
		}
		return explicit, nil
	}
	for _, dir := range assetDirCandidates {
		if isAssetDir(dir) {
			return dir, nil
		}
	}
	return "", errors.New("seed: no encuentro las fotos de la demo (scripts/demo-assets); pasá SEED_ASSETS_DIR")
}

// isAssetDir dice si la carpeta tiene TODAS las fotos de la galería.
func isAssetDir(dir string) bool {
	for _, asset := range galleryAssets {
		info, err := os.Stat(filepath.Join(dir, asset.File))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}

// loadAsset lee una foto y valida que realmente sea un WebP de tamaño razonable.
func loadAsset(dir string, asset demoAsset) ([]byte, error) {
	path := filepath.Join(dir, asset.File)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("seed: leer %s: %w", path, err)
	}
	if err := validateWebP(data); err != nil {
		return nil, fmt.Errorf("seed: %s: %w", path, err)
	}
	return data, nil
}

// validateWebP mira el encabezado RIFF/WEBP: alcanza para no subir un HTML de
// error de descarga disfrazado de foto.
func validateWebP(data []byte) error {
	if len(data) < 16 {
		return errors.New("no es una imagen WebP (archivo demasiado chico)")
	}
	if !bytes.HasPrefix(data, []byte("RIFF")) || string(data[8:12]) != "WEBP" {
		return errors.New("no es una imagen WebP (falta el encabezado RIFF/WEBP)")
	}
	if len(data) > maxAssetBytes {
		return fmt.Errorf("pesa %d bytes: el tope es %d", len(data), maxAssetBytes)
	}
	return nil
}
