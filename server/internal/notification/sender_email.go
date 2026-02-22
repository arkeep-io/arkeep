package notification

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// emailSender delivers notifications via SMTP. It reloads configuration on
// every Send call so changes made through the settings API take effect
// immediately without restarting the server.
//
// Supports two connection modes depending on SMTPConfig.TLS:
//   - true:  implicit TLS (SMTPS, typically port 465) via tls.Dial
//   - false: plaintext or STARTTLS (typically port 587) via smtp.SendMail
type emailSender struct {
	loader func(ctx context.Context) (*SMTPConfig, error)
}

// newEmailSender creates an emailSender. loader is called on every Send to
// retrieve the current SMTP configuration from the settings repository.
func newEmailSender(loader func(ctx context.Context) (*SMTPConfig, error)) *emailSender {
	return &emailSender{loader: loader}
}

// Send delivers an email notification to all provided recipient addresses.
// If the SMTP configuration is missing (ErrConfigNotFound) the send is skipped
// silently — SMTP is optional and may not be configured. Any other error is
// returned wrapped in ErrSendFailed.
func (s *emailSender) Send(ctx context.Context, to []string, subject, body string) error {
	if len(to) == 0 {
		return nil
	}

	cfg, err := s.loader(ctx)
	if err != nil {
		if err == ErrConfigNotFound {
			// SMTP not configured — skip silently.
			return nil
		}
		return fmt.Errorf("%w: failed to load smtp config: %s", ErrSendFailed, err)
	}

	msg := buildEmail(cfg.From, to, subject, body)
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	if cfg.TLS {
		return s.sendTLS(addr, cfg, to, msg)
	}
	return s.sendPlain(addr, cfg, to, msg)
}

// sendPlain uses smtp.SendMail which handles both plaintext and STARTTLS
// negotiation automatically. Suitable for port 25 and 587.
func (s *emailSender) sendPlain(addr string, cfg *SMTPConfig, to []string, msg []byte) error {
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}

	if err := smtp.SendMail(addr, auth, cfg.From, to, msg); err != nil {
		return fmt.Errorf("%w: smtp.SendMail: %s", ErrSendFailed, err)
	}
	return nil
}

// sendTLS establishes an implicit TLS connection (SMTPS) before the SMTP
// handshake. Required for servers that expect TLS from the first byte (port 465).
func (s *emailSender) sendTLS(addr string, cfg *SMTPConfig, to []string, msg []byte) error {
	tlsCfg := &tls.Config{
		ServerName: cfg.Host,
		MinVersion: tls.VersionTLS12,
	}

	conn, err := tls.Dial("tcp", addr, tlsCfg)
	if err != nil {
		return fmt.Errorf("%w: tls.Dial: %s", ErrSendFailed, err)
	}
	defer conn.Close()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("%w: smtp.NewClient: %s", ErrSendFailed, err)
	}
	defer client.Close()

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("%w: smtp auth: %s", ErrSendFailed, err)
		}
	}

	if err := client.Mail(cfg.From); err != nil {
		return fmt.Errorf("%w: MAIL FROM: %s", ErrSendFailed, err)
	}
	for _, r := range to {
		if err := client.Rcpt(r); err != nil {
			return fmt.Errorf("%w: RCPT TO %s: %s", ErrSendFailed, r, err)
		}
	}

	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("%w: DATA: %s", ErrSendFailed, err)
	}
	if _, err := w.Write(msg); err != nil {
		return fmt.Errorf("%w: write body: %s", ErrSendFailed, err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("%w: close DATA: %s", ErrSendFailed, err)
	}

	return client.Quit()
}

// buildEmail composes a minimal RFC 5322 email message.
func buildEmail(from string, to []string, subject, body string) []byte {
	var sb strings.Builder
	sb.WriteString("From: " + from + "\r\n")
	sb.WriteString("To: " + strings.Join(to, ", ") + "\r\n")
	sb.WriteString("Subject: " + subject + "\r\n")
	sb.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	sb.WriteString("MIME-Version: 1.0\r\n")
	sb.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
	sb.WriteString("\r\n")
	sb.WriteString(body)
	return []byte(sb.String())
}