package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"strings"
	"sync"
	"time"
)

// apiClient habla con la API real de Salón SaaS. Todo lo que la API sabe hacer
// (validaciones, hash de claves, auditoría, eventos) se hace por acá: el seed no
// reimplementa reglas de negocio.
type apiClient struct {
	base  string
	token string
	http  *http.Client

	// paced lleva la cuenta de las escrituras públicas y de autenticación: la
	// API limita a 20 por minuto por IP y el seed hace unas cuantas seguidas.
	mu    sync.Mutex
	paced map[string][]time.Time
}

func newAPI(base string) *apiClient {
	return &apiClient{
		base:  base,
		http:  &http.Client{Timeout: 30 * time.Second},
		paced: map[string][]time.Time{},
	}
}

// pacedPrefixes son los prefijos con límite por IP (B-07) que el seed toca en
// ráfaga: /auth/ (registro y login de las siete cuentas) y las escrituras
// públicas (reservas y lista de espera).
var pacedPrefixes = []string{"/auth/", "/public/salons/"}

// pace espera lo justo para no pasar el límite por IP. No reemplaza al 429 (que
// igual se reintenta): lo evita, para que una corrida no tarde minutos de más.
func (c *apiClient) pace(ctx context.Context, path string) error {
	matched := ""
	for _, prefix := range pacedPrefixes {
		if strings.HasPrefix(path, prefix) {
			matched = prefix
			break
		}
	}
	if matched == "" {
		return nil
	}
	const limit = 15
	const window = time.Minute
	c.mu.Lock()
	now := time.Now()
	recent := c.paced[matched][:0]
	for _, at := range c.paced[matched] {
		if now.Sub(at) < window {
			recent = append(recent, at)
		}
	}
	c.paced[matched] = recent
	wait := time.Duration(0)
	if len(recent) >= limit {
		wait = window - now.Sub(recent[0]) + time.Second
	}
	c.mu.Unlock()
	if wait <= 0 {
		c.mu.Lock()
		c.paced[matched] = append(c.paced[matched], now)
		c.mu.Unlock()
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(wait):
	}
	c.mu.Lock()
	c.paced[matched] = append([]time.Time{}, time.Now())
	c.mu.Unlock()
	return nil
}

// response es la respuesta cruda: el seed mira el status y decodifica el JSON
// solo cuando lo necesita (muchos endpoints devuelven cuerpos que no usa).
type response struct {
	status int
	body   []byte
}

func (r response) ok() bool { return r.status >= 200 && r.status < 300 }

// errorText busca la clave "error" del cuerpo; si no está, devuelve el cuerpo.
func (r response) errorText() string {
	var payload struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(r.body, &payload) == nil && payload.Error != "" {
		return payload.Error
	}
	text := strings.TrimSpace(string(r.body))
	if len(text) > 300 {
		text = text[:300] + "…"
	}
	return text
}

// decode parsea el cuerpo en `out`.
func (r response) decode(out any) error {
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.body, out); err != nil {
		return fmt.Errorf("respuesta ilegible: %w", err)
	}
	return nil
}

// rateLimitWait es lo que se espera entre reintentos cuando la API responde 429
// (el límite por IP es de 20 pedidos por minuto en /auth/ y en las escrituras
// públicas: el seed crea siete cuentas y treinta turnos y los reintenta solo).
const rateLimitWait = 20 * time.Second

// maxRateLimitRetries es cuántas veces se reintenta un 429.
const maxRateLimitRetries = 4

// do ejecuta un pedido JSON contra /api/v1<path>, reintentando el 429 con
// espera: un seed no puede fallar porque la API lo frenó por ráfaga.
func (c *apiClient) do(ctx context.Context, method, path string, in any) (response, error) {
	for attempt := 0; ; attempt++ {
		res, err := c.once(ctx, method, path, in)
		if err != nil {
			return res, err
		}
		if res.status != http.StatusTooManyRequests || attempt >= maxRateLimitRetries {
			return res, nil
		}
		select {
		case <-ctx.Done():
			return res, ctx.Err()
		case <-time.After(rateLimitWait):
		}
	}
}

// once es un pedido sin reintentos.
func (c *apiClient) once(ctx context.Context, method, path string, in any) (response, error) {
	if method != http.MethodGet {
		if err := c.pace(ctx, path); err != nil {
			return response{}, err
		}
	}
	var body io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return response{}, err
		}
		body = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+"/api/v1"+path, body)
	if err != nil {
		return response{}, err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return response{}, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return response{}, fmt.Errorf("%s %s: leer respuesta: %w", method, path, err)
	}
	return response{status: res.StatusCode, body: raw}, nil
}

// must ejecuta y exige 2xx: el error incluye el texto que devolvió la API.
func (c *apiClient) must(ctx context.Context, method, path string, in, out any) error {
	res, err := c.do(ctx, method, path, in)
	if err != nil {
		return err
	}
	if !res.ok() {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, path, res.status, res.errorText())
	}
	return res.decode(out)
}

// upload sube un archivo con multipart/form-data (fotos de la galería y de las
// fichas). El campo se llama `file`, como espera la API.
func (c *apiClient) upload(ctx context.Context, path, filename string, data []byte, out any) error {
	var buf bytes.Buffer
	writer := multipart.NewWriter(&buf)
	// El Content-Type de la parte es obligatorio: la API solo acepta fotos
	// declaradas como image/* (con multipart.CreateFormFile llegan como
	// application/octet-stream y el caso de uso devuelve 400).
	header := make(textproto.MIMEHeader)
	header.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename="%s"`, filename))
	header.Set("Content-Type", mediaTypeFor(filename))
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if _, err := part.Write(data); err != nil {
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/api/v1"+path, &buf)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer func() { _ = res.Body.Close() }()
	raw, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		return err
	}
	resp := response{status: res.StatusCode, body: raw}
	if !resp.ok() {
		return fmt.Errorf("POST %s: HTTP %d: %s", path, res.StatusCode, resp.errorText())
	}
	return resp.decode(out)
}

// mediaTypeFor elige el tipo declarado de la foto por su extensión.
func mediaTypeFor(filename string) string {
	if strings.HasSuffix(strings.ToLower(filename), ".webp") {
		return "image/webp"
	}
	return "image/jpeg"
}

// getList arma un GET y devuelve una lista de objetos JSON tal cual, sin tipos:
// el seed solo necesita los ids, y así no hay que modelar cada respuesta.
func (c *apiClient) getList(ctx context.Context, path, key string) ([]map[string]any, error) {
	res, err := c.do(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	if !res.ok() {
		return nil, fmt.Errorf("GET %s: HTTP %d: %s", path, res.status, res.errorText())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(res.body, &payload); err != nil {
		return nil, fmt.Errorf("GET %s: %w", path, err)
	}
	raw, ok := payload[key]
	if !ok {
		return nil, nil
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("GET %s (%s): %w", path, key, err)
	}
	return list, nil
}

// stringField lee un campo string de un objeto JSON suelto.
func stringField(obj map[string]any, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}
