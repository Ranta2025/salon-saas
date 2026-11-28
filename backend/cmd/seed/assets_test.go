package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoAssets es la carpeta versionada vista desde backend/cmd/seed: los tests
// corren con ese directorio como cwd.
const repoAssets = "../../../scripts/demo-assets"

func TestAssetsDirFromRepo(t *testing.T) {
	dir, err := AssetsDir(repoAssets)
	if err != nil {
		t.Fatalf("AssetsDir(%q): %v", repoAssets, err)
	}
	if dir != repoAssets {
		t.Fatalf("AssetsDir devolvió %q", dir)
	}
}

func TestAssetsDirRejectsDirectoryWithoutPhotos(t *testing.T) {
	empty := t.TempDir()
	if _, err := AssetsDir(empty); err == nil {
		t.Fatal("una carpeta explícita sin fotos tiene que ser error, no un fallback")
	}
}

func TestAssetsDirFindsCandidates(t *testing.T) {
	// Sin carpeta explícita recorre los candidatos; desde backend/cmd/seed el que
	// existe es ../scripts/demo-assets resuelto hacia arriba.
	dir, err := AssetsDir("")
	if err != nil {
		t.Fatalf("no encontré las fotos por los candidatos: %v", err)
	}
	if !isAssetDir(dir) {
		t.Fatalf("%q no es una carpeta de fotos", dir)
	}
}

func TestGalleryAssetsAreRealWebP(t *testing.T) {
	if _, err := os.Stat(repoAssets); err != nil {
		t.Skipf("no está la carpeta de fotos: %v", err)
	}
	for _, asset := range galleryAssets {
		asset := asset
		t.Run(asset.File, func(t *testing.T) {
			data, err := loadAsset(repoAssets, asset)
			if err != nil {
				t.Fatalf("loadAsset: %v", err)
			}
			if len(data) > maxAssetBytes {
				t.Fatalf("%s pesa %d bytes (tope %d)", asset.File, len(data), maxAssetBytes)
			}
			if !bytes.HasPrefix(data, []byte("RIFF")) {
				t.Fatalf("%s no empieza con RIFF", asset.File)
			}
			if asset.Caption == "" {
				t.Fatalf("%s no tiene leyenda", asset.File)
			}
		})
	}
}

func TestClientPhotoAssetsExist(t *testing.T) {
	if _, err := os.Stat(repoAssets); err != nil {
		t.Skipf("no está la carpeta de fotos: %v", err)
	}
	for _, asset := range clientPhotoAssets {
		if _, err := loadAsset(repoAssets, asset); err != nil {
			t.Fatalf("foto de ficha %s: %v", asset.File, err)
		}
	}
}

func TestValidateWebP(t *testing.T) {
	if err := validateWebP([]byte("muy corto")); err == nil {
		t.Fatal("un archivo corto tiene que fallar")
	}
	if err := validateWebP([]byte(strings.Repeat("x", 64))); err == nil {
		t.Fatal("un archivo sin encabezado RIFF/WEBP tiene que fallar")
	}
	// Un HTML de error de descarga disfrazado de foto: RIFF pero sin WEBP.
	fake := append([]byte("RIFF"), make([]byte, 4)...)
	fake = append(fake, []byte("AVI ")...)
	fake = append(fake, make([]byte, 8)...)
	if err := validateWebP(fake); err == nil {
		t.Fatal("un contenedor RIFF que no es WebP tiene que fallar")
	}
	oversize := append([]byte("RIFF"), make([]byte, 4)...)
	oversize = append(oversize, []byte("WEBP")...)
	oversize = append(oversize, make([]byte, maxAssetBytes)...)
	if err := validateWebP(oversize); err == nil {
		t.Fatal("una foto de más de 200 KB tiene que fallar")
	}
}

func TestLoadAssetMissingFile(t *testing.T) {
	if _, err := loadAsset(t.TempDir(), demoAsset{File: "no-existe.webp"}); err == nil {
		t.Fatal("un archivo que no existe tiene que dar error")
	}
}

func TestAttributionFileCoversEveryAsset(t *testing.T) {
	path := filepath.Join(repoAssets, "ATTRIBUTION.md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("no está la atribución: %v", err)
	}
	text := string(raw)
	for _, asset := range append(append([]demoAsset{}, galleryAssets...), clientPhotoAssets...) {
		if !strings.Contains(text, asset.File) {
			t.Fatalf("ATTRIBUTION.md no nombra %s (autor, licencia y origen son obligatorios)", asset.File)
		}
	}
}
