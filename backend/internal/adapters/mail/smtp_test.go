package mail

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"salon-saas/internal/ports"
)

// fakeSMTP es un servidor SMTP mínimo en memoria: alcanza para verificar qué
// manda el adaptador (comandos, headers y cuerpo) sin red externa.
//
// stall=true acepta la conexión y no saluda nunca: sirve para probar el timeout.
type fakeSMTP struct {
	ln    net.Listener
	stall bool

	mu   sync.Mutex
	cmds []string
	data []byte
}

func newFakeSMTP(t *testing.T, stall bool) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	f := &fakeSMTP{ln: ln, stall: stall}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

func (f *fakeSMTP) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.session(conn)
	}
}

func (f *fakeSMTP) session(conn net.Conn) {
	defer conn.Close()
	if f.stall {
		// Mantener viva la conexión hasta que el cliente corte (deadline).
		buf := make([]byte, 1)
		_, _ = conn.Read(buf)
		return
	}
	w := bufio.NewWriter(conn)
	write := func(s string) {
		_, _ = w.WriteString(s + "\r\n")
		_ = w.Flush()
	}
	write("220 fake ESMTP")
	r := bufio.NewReader(conn)
	inData := false
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		if inData {
			if line == "." {
				inData = false
				write("250 OK mensaje aceptado")
				continue
			}
			f.mu.Lock()
			f.data = append(f.data, line...)
			f.data = append(f.data, '\n')
			f.mu.Unlock()
			continue
		}
		f.mu.Lock()
		f.cmds = append(f.cmds, line)
		f.mu.Unlock()
		switch {
		case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
			_, _ = w.WriteString("250-fake\r\n250 OK\r\n")
			_ = w.Flush()
		case strings.HasPrefix(line, "MAIL FROM"), strings.HasPrefix(line, "RCPT TO"):
			write("250 OK")
		case line == "DATA":
			inData = true
			write("354 escribí el mensaje")
		case line == "QUIT":
			write("221 chau")
			return
		default:
			write("250 OK")
		}
	}
}

func (f *fakeSMTP) snapshot() ([]string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.cmds...), string(f.data)
}

func (f *fakeSMTP) config(t *testing.T) Config {
	t.Helper()
	host, port, err := net.SplitHostPort(f.ln.Addr().String())
	if err != nil {
		t.Fatalf("addr: %v", err)
	}
	return Config{Host: host, Port: port, From: "turnos@salon.local", Timeout: 2 * time.Second}
}

// SMTP_HOST vacío: no-op. El caso de uso no tiene que saber si hay email.
func TestSender_deshabilitado(t *testing.T) {
	s, err := New(Config{})
	if err != nil {
		t.Fatalf("config vacía: %v", err)
	}
	if s.Enabled() {
		t.Fatal("sin SMTP_HOST el sender debe estar deshabilitado")
	}
	if err := s.Send(context.Background(), ports.Message{To: "lia@mail.com", Subject: "x", Text: "y"}); err != nil {
		t.Fatalf("el no-op no puede fallar: %v", err)
	}
	// TLS desconocido es error de configuración, no un default silencioso.
	if _, err := New(Config{Host: "127.0.0.1", TLS: "ssl"}); err == nil {
		t.Fatal("SMTP_TLS desconocido debe fallar")
	}
}

// multipart/alternative con text y HTML, subject UTF-8 codificado.
func TestSender_multipart(t *testing.T) {
	srv := newFakeSMTP(t, false)
	s, err := New(srv.config(t))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	msg := ports.Message{
		To:      "Lia@Mail.com",
		Subject: "Turno confirmado · Navajas de Oro",
		Text:    "Hola Lía:\nTu turno quedó confirmado.",
		HTML:    "<p>Hola Lía:</p><p>Tu turno quedó confirmado.</p>",
	}
	if err := s.Send(context.Background(), msg); err != nil {
		t.Fatalf("send: %v", err)
	}
	cmds, body := srv.snapshot()
	joined := strings.Join(cmds, "\n")
	if !strings.Contains(joined, "MAIL FROM:<turnos@salon.local>") {
		t.Fatalf("MAIL FROM: %s", joined)
	}
	if !strings.Contains(joined, "RCPT TO:<Lia@Mail.com>") {
		t.Fatalf("RCPT TO: %s", joined)
	}
	if !strings.Contains(joined, "DATA") || !strings.Contains(joined, "QUIT") {
		t.Fatalf("secuencia SMTP: %s", joined)
	}
	for _, want := range []string{
		"Subject: =?utf-8?q?Turno_confirmado_=C2=B7_Navajas_de_Oro?=",
		"Content-Type: multipart/alternative",
		"Content-Type: text/plain; charset=\"utf-8\"",
		"Content-Type: text/html; charset=\"utf-8\"",
		"Tu turno quedó confirmado.",
		"<p>Hola Lía:</p>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("falta %q en el mensaje:\n%s", want, body)
		}
	}
}

// Cabeceras con CR/LF (header injection): se rechazan antes de abrir conexión.
func TestSender_rechaza_injection(t *testing.T) {
	srv := newFakeSMTP(t, false)
	s, err := New(srv.config(t))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	base := ports.Message{To: "lia@mail.com", Subject: "Turno", Text: "x", HTML: "<p>x</p>"}
	for _, bad := range []ports.Message{
		{To: "lia@mail.com\r\nBcc: evil@mail.com", Subject: base.Subject, Text: base.Text},
		{To: base.To, Subject: "Hola\nBcc: evil@mail.com", Text: base.Text},
		{To: "sin-arroba", Subject: base.Subject, Text: base.Text},
		{To: base.To, Subject: "  ", Text: base.Text},
	} {
		if err := s.Send(context.Background(), bad); err == nil {
			t.Fatalf("debe rechazar %+v", bad)
		}
	}
	if cmds, _ := srv.snapshot(); len(cmds) != 0 {
		t.Fatalf("un mensaje inválido no puede abrir la conexión SMTP: %v", cmds)
	}
}

// Un servidor que no saluda no puede colgar el envío: el deadline corta.
func TestSender_timeout(t *testing.T) {
	srv := newFakeSMTP(t, true)
	cfg := srv.config(t)
	cfg.Timeout = 150 * time.Millisecond
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	start := time.Now()
	err = s.Send(context.Background(), ports.Message{To: "lia@mail.com", Subject: "x", Text: "y", HTML: "<p>y</p>"})
	if err == nil {
		t.Fatal("el servidor mudo debe dar timeout")
	}
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("el timeout no se aplicó: %v", elapsed)
	}
}

// Un contexto ya vencido no intenta enviar.
func TestSender_contexto_vencido(t *testing.T) {
	srv := newFakeSMTP(t, false)
	s, err := New(srv.config(t))
	if err != nil {
		t.Fatalf("config: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.Send(ctx, ports.Message{To: "lia@mail.com", Subject: "x", Text: "y", HTML: "<p>y</p>"}); err == nil {
		t.Fatal("contexto cancelado debe fallar")
	}
	if cmds, _ := srv.snapshot(); len(cmds) != 0 {
		t.Fatalf("no debe mandar comandos con el contexto vencido: %v", cmds)
	}
}

// buildMessage es puro: permite fijar el boundary en el test.
func TestBuildMessage_limpia(t *testing.T) {
	raw := string(buildMessage(ports.Message{
		To: "lia@mail.com", Subject: "Recordatorio de tu turno · Salón",
		Text: "texto", HTML: "<p>html</p>",
	}, "turnos@salon.local", "BOUND", time.Date(2026, 12, 1, 15, 0, 0, 0, time.UTC)))
	for _, want := range []string{
		"From: turnos@salon.local\r\n",
		"To: lia@mail.com\r\n",
		"Date: Tue, 01 Dec 2026 15:00:00 +0000\r\n",
		"Content-Type: multipart/alternative; boundary=\"BOUND\"\r\n",
		"\r\n--BOUND\r\n",
		"\r\n--BOUND--\r\n",
	} {
		if !strings.Contains(raw, want) {
			t.Fatalf("falta %q:\n%s", want, raw)
		}
	}
	// El asunto nunca viaja en crudo: se codifica (mime.QEncoding).
	if strings.Contains(raw, "Subject: Recordatorio") {
		t.Fatalf("asunto sin codificar:\n%s", raw)
	}
}
