package httpkit

import (
	"encoding/json"
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

func Keys(t *testing.T, v map[string]any) []string {
	t.Helper()
	out := make([]string, 0, len(v))
	for k := range v {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func SameKeys(t *testing.T, what string, got map[string]any, want ...string) {
	t.Helper()
	sort.Strings(want)
	g := Keys(t, got)
	if len(g) != len(want) {
		t.Fatalf("%s: claves %v, esperaba %v", what, g, want)
	}
	for i := range want {
		if g[i] != want[i] {
			t.Fatalf("%s: claves %v, esperaba %v", what, g, want)
		}
	}
}

func OkErr(t *testing.T, body []byte) string {
	t.Helper()
	var out struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("json de error inválido: %v (%s)", err, body)
	}
	return out.Error
}
