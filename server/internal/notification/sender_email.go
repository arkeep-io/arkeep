package notification

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"net/smtp"
	"net/textproto"
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
// htmlBody is optional: when set, the email carries it next to the plain-text
// body (multipart/alternative) with the Arkeep logo attached inline; when
// empty, the email is plain text only (notifications not about a job,
// password reset, test email).
// If the SMTP configuration is missing (ErrConfigNotFound) the send is skipped
// silently — SMTP is optional and may not be configured. Any other error is
// returned wrapped in ErrSendFailed.
func (s *emailSender) Send(ctx context.Context, to []string, subject, body, htmlBody string) error {
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

	// Settings saved before From was validated could still hold a malformed
	// address: refuse to send rather than put it in MAIL FROM and the headers.
	if !IsBareAddress(cfg.From) {
		return fmt.Errorf("%w: invalid from address", ErrSendFailed)
	}

	msg, err := buildEmail(cfg.From, cfg.FromName, to, subject, body, htmlBody)
	if err != nil {
		return fmt.Errorf("%w: build message: %s", ErrSendFailed, err)
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprintf("%d", cfg.Port))

	if cfg.TLS {
		return s.sendTLS(addr, cfg, to, msg)
	}
	return s.sendPlain(addr, cfg, to, msg)
}

// sendPlain opens a plain TCP connection and drives the SMTP session manually.
// It upgrades to STARTTLS if the server advertises it, and authenticates only
// if a username is configured. This avoids the Go stdlib restriction that
// rejects AUTH advertisements on unencrypted connections — which would break
// test servers like MailHog that announce AUTH without offering TLS.
func (s *emailSender) sendPlain(addr string, cfg *SMTPConfig, to []string, msg []byte) error {
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("%w: dial: %s", ErrSendFailed, err)
	}

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("%w: smtp.NewClient: %s", ErrSendFailed, err)
	}
	defer func() { _ = client.Close() }()

	if ok, _ := client.Extension("STARTTLS"); ok {
		tlsCfg := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}
		if err := client.StartTLS(tlsCfg); err != nil {
			return fmt.Errorf("%w: STARTTLS: %s", ErrSendFailed, err)
		}
	}

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
	defer func() {
		if err := conn.Close(); err != nil {
			_ = err // TLS conn close error is non-actionable after SMTP session ends
		}
	}()

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return fmt.Errorf("%w: smtp.NewClient: %s", ErrSendFailed, err)
	}
	defer func() {
		if err := client.Close(); err != nil {
			_ = err // SMTP client close error is non-actionable after session ends
		}
	}()

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

// IsBareAddress reports whether s is a single RFC 5322 address with no display
// name, comments or surrounding text (e.g. "noreply@example.com"), so it is
// safe to use verbatim in MAIL FROM and in the From header.
func IsBareAddress(s string) bool {
	if s == "" || strings.ContainsAny(s, "\r\n") {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Name == "" && a.Address == s
}

// sanitizeHeader removes CR and LF to prevent email header injection (CWE-93).
// Every value interpolated into a header field must pass through this.
func sanitizeHeader(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// buildEmail composes an RFC 5322 email message. When fromName is set the
// From header uses the "Name <address>" form (encoded/quoted via net/mail);
// otherwise it falls back to the bare address.
//
// Text parts are quoted-printable: SMTP limits lines to 998 characters, and
// an error message in a notification is often one long line. With
// htmlBody set the message is
//
//	multipart/alternative
//	├── text/plain
//	└── multipart/related
//	    ├── text/html
//	    └── image/png  (Content-ID <arkeep-logo>)
func buildEmail(from, fromName string, to []string, subject, body, htmlBody string) ([]byte, error) {
	recipients := make([]string, len(to))
	for i, r := range to {
		recipients[i] = sanitizeHeader(r)
	}

	fromHeader := sanitizeHeader(from)
	if fromName != "" {
		// mail.Address.String() handles quoting and RFC 2047 encoding; the
		// final sanitizeHeader keeps the CRLF-injection guard from a hostile name.
		fromHeader = sanitizeHeader((&mail.Address{Name: fromName, Address: from}).String())
	}

	var buf bytes.Buffer
	buf.WriteString("From: " + fromHeader + "\r\n")
	buf.WriteString("To: " + strings.Join(recipients, ", ") + "\r\n")
	buf.WriteString("Subject: " + sanitizeHeader(subject) + "\r\n")
	buf.WriteString("Date: " + time.Now().UTC().Format(time.RFC1123Z) + "\r\n")
	buf.WriteString("MIME-Version: 1.0\r\n")

	if htmlBody == "" {
		buf.WriteString("Content-Type: text/plain; charset=UTF-8\r\n")
		buf.WriteString("Content-Transfer-Encoding: quoted-printable\r\n")
		buf.WriteString("\r\n")
		if err := writeQuotedPrintable(&buf, body); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}

	// The related part is built first: its boundary goes into the header of
	// the alternative part that contains it.
	var related bytes.Buffer
	rw := multipart.NewWriter(&related)
	if err := writeTextPart(rw, "text/html; charset=UTF-8", htmlBody); err != nil {
		return nil, err
	}
	// No Content-Disposition and no file name: several clients (Apple Mail,
	// Thunderbird, Outlook) list a part that has a file name as an attachment,
	// even when the HTML shows it inline.
	logo, err := rw.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {"image/png"},
		"Content-Transfer-Encoding": {"base64"},
		"Content-ID":                {"<" + logoCID + ">"},
	})
	if err != nil {
		return nil, err
	}
	if err := writeBase64Lines(logo, logoPNG); err != nil {
		return nil, err
	}
	if err := rw.Close(); err != nil {
		return nil, err
	}

	var alt bytes.Buffer
	aw := multipart.NewWriter(&alt)
	buf.WriteString("Content-Type: multipart/alternative; boundary=\"" + aw.Boundary() + "\"\r\n")
	buf.WriteString("\r\n")
	if err := writeTextPart(aw, "text/plain; charset=UTF-8", body); err != nil {
		return nil, err
	}
	rp, err := aw.CreatePart(textproto.MIMEHeader{
		"Content-Type": {`multipart/related; type="text/html"; boundary="` + rw.Boundary() + `"`},
	})
	if err != nil {
		return nil, err
	}
	if _, err := rp.Write(related.Bytes()); err != nil {
		return nil, err
	}
	if err := aw.Close(); err != nil {
		return nil, err
	}
	buf.Write(alt.Bytes())
	return buf.Bytes(), nil
}

// writeTextPart adds a quoted-printable text part of the given content type.
func writeTextPart(w *multipart.Writer, contentType, text string) error {
	part, err := w.CreatePart(textproto.MIMEHeader{
		"Content-Type":              {contentType},
		"Content-Transfer-Encoding": {"quoted-printable"},
	})
	if err != nil {
		return err
	}
	return writeQuotedPrintable(part, text)
}

func writeQuotedPrintable(w io.Writer, text string) error {
	qp := quotedprintable.NewWriter(w)
	if _, err := qp.Write([]byte(text)); err != nil {
		return err
	}
	return qp.Close()
}

// writeBase64Lines writes data base64-encoded in lines of 76 characters.
func writeBase64Lines(w io.Writer, data []byte) error {
	enc := base64.StdEncoding.EncodeToString(data)
	for len(enc) > 0 {
		n := min(76, len(enc))
		if _, err := w.Write([]byte(enc[:n] + "\r\n")); err != nil {
			return err
		}
		enc = enc[n:]
	}
	return nil
}
