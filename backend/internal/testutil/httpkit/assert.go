package httpkit

import (
	"net/http/httptest"
	"sort"
	"testing"
)

func KeysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func ErrText(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error string `json:"error"`
	}
	MustJSON(t, w.Body.Bytes(), &e)
	return e.Error
}

func WantHTTP(t *testing.T, w *httptest.ResponseRecorder, code int, text string) {
	t.Helper()
	if w.Code != code || (text != "" && ErrText(t, w) != text) {
		t.Fatalf("esperaba %d %q, dio %d %s", code, text, w.Code, w.Body.String())
	}
}
