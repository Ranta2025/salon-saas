// Package mail es el adaptador SMTP del puerto ports.MailSender.
//
// Habla SMTP con la stdlib: sin cliente externo, sin dependencias nuevas. Tres
// modos de transporte (SMTP_TLS):
//   - none:     SMTP plano (Mailpit de desarrollo, relay interno).
//   - starttls: empieza plano y sube a TLS con STARTTLS (587 típico).
//   - tls:      TLS implícito desde el primer byte (465 típico).
//
// Todo envío tiene deadline: un servidor que no contesta no puede colgar un
// caso de uso.
package mail

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"mime"
	"net"
	"net/smtp"
	"strings"
	"time"

	"salon-saas/internal/ports"
)

// Modos de SMTP_TLS.
const (
	TLSNone     = "none"
	TLSStartTLS = "starttls"
	TLSImplicit = "tls"
)

// defaultTimeout acota TODO el diálogo SMTP de un mensaje (conexión, saludo,
// auth y datos). Es de transporte, no de negocio.
const defaultTimeout = 10 * time.Second

// Config es la configuración del adaptador. Host vacío = email deshabilitado
// (entorno de desarrollo sin servidor de correo).
type Config struct {
	Host    string
	Port    string // default 25
	User    string
	Pass    string
	From    string // remitente de todos los mensajes
	TLS     string // none|starttls|tls (default none)
	Timeout time.Duration
}

// Sender implementa ports.MailSender.
type Sender struct {
	cfg     Config
	timeout time.Duration
}

// New valida la configuración y devuelve el sender. Con Host vacío devuelve un
// sender deshabilitado: Send no hace nada y devuelve nil.
func New(cfg Config) (Sender, error) {
	cfg.Host = strings.TrimSpace(cfg.Host)
	cfg.Port = strings.TrimSpace(cfg.Port)
	cfg.User = strings.TrimSpace(cfg.User)
	cfg.From = strings.TrimSpace(cfg.From)
	cfg.TLS = strings.TrimSpace(strings.ToLower(cfg.TLS))
	switch cfg.TLS {
	case "":
		cfg.TLS = TLSNone
	case TLSNone, TLSStartTLS, TLSImplicit:
	default:
		return Sender{}, fmt.Errorf("SMTP_TLS desconocido %q (usá none, starttls o tls)", cfg.TLS)
	}
	if cfg.Port == "" {
		cfg.Port = "25"
	}
	if cfg.From == "" {
		cfg.From = "turnos@salon.local"
	}
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return Sender{cfg: cfg, timeout: timeout}, nil
}

// Enabled dice si hay un servidor configurado: con false, el proceso no debe
// arrancar la tarea de recordatorios (los reclamos quedarían sin enviar).
func (s Sender) Enabled() bool { return s.cfg.Host != "" }

// Send arma el mensaje y lo entrega por SMTP. Rechaza CR/LF en los headers
// antes de abrir la conexión: un asunto con "\nBcc:" sería un envío a
// destinatarios que nadie pidió (header injection).
func (s Sender) Send(ctx context.Context, msg ports.Message) error {
	msg.To = strings.TrimSpace(msg.To)
	msg.Subject = strings.TrimSpace(msg.Subject)
	// Aunque el sender esté deshabilitado se valida igual: un llamado con
	// basura es un bug del caso de uso y no debe pasar en silencio.
	if err := validHeaders(s.cfg.From, msg.To, msg.Subject); err != nil {
		return err
	}
	if !s.Enabled() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	addr := net.JoinHostPort(s.cfg.Host, s.cfg.Port)
	deadline := time.Now().Add(s.timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	dialer := net.Dialer{Timeout: s.timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp: conectar a %s: %w", addr, err)
	}
	defer conn.Close()
	// El deadline cubre saludo, STARTTLS, auth y DATA: ningún paso puede
	// quedarse esperando a un servidor mudo.
	if err := conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("smtp: deadline: %w", err)
	}

	clientConn := conn
	if s.cfg.TLS == TLSImplicit {
		tlsConn := tls.Client(conn, &tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12})
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return fmt.Errorf("smtp: handshake TLS: %w", err)
		}
		clientConn = tlsConn
	}

	client, err := smtp.NewClient(clientConn, s.cfg.Host)
	if err != nil {
		return fmt.Errorf("smtp: saludo: %w", err)
	}
	defer client.Close()

	if s.cfg.TLS == TLSStartTLS {
		if err := client.StartTLS(&tls.Config{ServerName: s.cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("smtp: STARTTLS: %w", err)
		}
	}
	if s.cfg.User != "" {
		if err := client.Auth(smtp.PlainAuth("", s.cfg.User, s.cfg.Pass, s.cfg.Host)); err != nil {
			return fmt.Errorf("smtp: autenticación: %w", err)
		}
	}
	if err := client.Mail(s.cfg.From); err != nil {
		return fmt.Errorf("smtp: remitente: %w", err)
	}
	if err := client.Rcpt(msg.To); err != nil {
		return fmt.Errorf("smtp: destinatario: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("smtp: DATA: %w", err)
	}
	if _, err := w.Write(buildMessage(msg, s.cfg.From, newBoundary(), time.Now())); err != nil {
		return fmt.Errorf("smtp: cuerpo: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("smtp: cerrar mensaje: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("smtp: QUIT: %w", err)
	}
	return nil
}

// validHeaders es la barrera contra header injection: ningún valor que viaja en
// un header puede llevar un salto de línea. La dirección tiene que tener forma
// de dirección (sin espacios: "a@b.com Bcc:..." no entra).
func validHeaders(from, to, subject string) error {
	for name, value := range map[string]string{"From": from, "To": to, "Subject": subject} {
		if strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("smtp: el header %s no puede tener saltos de línea", name)
		}
	}
	if from == "" || !strings.Contains(from, "@") || strings.ContainsAny(from, " \t") {
		return fmt.Errorf("smtp: remitente inválido %q", from)
	}
	if to == "" || !strings.Contains(to, "@") || strings.ContainsAny(to, " \t<>") {
		return fmt.Errorf("smtp: destinatario inválido %q", to)
	}
	if subject == "" {
		return fmt.Errorf("smtp: el asunto es obligatorio")
	}
	return nil
}

// newBoundary es aleatorio para que ningún cuerpo pueda contenerlo.
func newBoundary() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return "salon-" + hex.EncodeToString(b[:])
}

// buildMessage arma el MIME: multipart/alternative con texto plano y HTML, sin
// imágenes remotas. El asunto viaja codificado (mime.QEncoding) y las líneas
// usan CRLF, que es lo que espera SMTP.
func buildMessage(msg ports.Message, from, boundary string, now time.Time) []byte {
	var buf bytes.Buffer
	write := func(s string) { buf.WriteString(s) }
	write("From: " + from + "\r\n")
	write("To: " + msg.To + "\r\n")
	write("Subject: " + mime.QEncoding.Encode("utf-8", msg.Subject) + "\r\n")
	write("Date: " + now.Format(time.RFC1123Z) + "\r\n")
	write("MIME-Version: 1.0\r\n")
	write("Content-Type: multipart/alternative; boundary=\"" + boundary + "\"\r\n")
	write("\r\n")
	write("--" + boundary + "\r\n")
	write("Content-Type: text/plain; charset=\"utf-8\"\r\n")
	write("Content-Transfer-Encoding: 8bit\r\n")
	write("\r\n")
	write(withFinalNewline(msg.Text))
	write("--" + boundary + "\r\n")
	write("Content-Type: text/html; charset=\"utf-8\"\r\n")
	write("Content-Transfer-Encoding: 8bit\r\n")
	write("\r\n")
	write(withFinalNewline(msg.HTML))
	write("--" + boundary + "--\r\n")
	return buf.Bytes()
}

// withFinalNewline normaliza el cuerpo a CRLF (lo que espera SMTP) y se
// asegura de que termine en salto de línea: sin eso, el delimitador final del
// multipart quedaría pegado al último caracter del cuerpo.
func withFinalNewline(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.HasSuffix(s, "\n") {
		s += "\n"
	}
	return strings.ReplaceAll(s, "\n", "\r\n")
}

var _ ports.MailSender = Sender{}
