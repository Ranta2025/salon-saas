package testutil

import (
	"salon-saas/internal/domain"
	"salon-saas/internal/ports"
)

// FakeStorage es el almacenamiento de fotos en memoria: mismos nombres de
// archivo que el adaptador real (Put devuelve la clave) y conteos para probar
// que un alta fallida borra lo escrito.
type FakeStorage struct {
	Files map[string][]byte
	Puts  int
	Dels  int
}

func (f *FakeStorage) Put(key string, data []byte, contentType string) (string, error) {
	if f.Files == nil {
		f.Files = map[string][]byte{}
	}
	f.Files[key] = data
	f.Puts++
	return key, nil
}

func (f *FakeStorage) Get(path string) ([]byte, error) {
	data, ok := f.Files[path]
	if !ok {
		return nil, domain.ErrNotFound
	}
	return data, nil
}

func (f *FakeStorage) Delete(path string) error {
	if f.Files != nil {
		delete(f.Files, path)
	}
	f.Dels++
	return nil
}

var _ ports.PhotoStorage = (*FakeStorage)(nil)

// FakeImages no convierte nada: devuelve un WebP de mentira con dimensiones
// fijas. Err simula bytes que no son una imagen.
type FakeImages struct {
	Out          []byte
	W, H         int
	Err          error
	GotContentIn []byte
}

func (f *FakeImages) ToWebP(data []byte) ([]byte, int, int, error) {
	f.GotContentIn = data
	if f.Err != nil {
		return nil, 0, 0, f.Err
	}
	if f.Out == nil {
		f.Out = []byte("RIFF....WEBP")
	}
	out := f.Out
	w, h := f.W, f.H
	if w == 0 {
		w, h = 1280, 960
	}
	return out, w, h, nil
}

var _ ports.ImageProcessor = (*FakeImages)(nil)
