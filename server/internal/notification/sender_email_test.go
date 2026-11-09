package notification

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"testing"
)

func TestBuildEmail(t *testing.T) {
	tests := []struct {
		name        string
		from        string
		fromName    string
		to          []string
		subject     string
		body        string
		wantHeaders []string // header lines that must appear verbatim
		wantNoLine  []string // substrings that must NOT appear as injected header lines
		wantBody    string   // body must be preserved verbatim
	}{
		{
			name:        "plain values",
			from:        "noreply@arkeep.io",
			to:          []string{"admin@example.com"},
			subject:     "Backup completed",
			body:        "All good.",
			wantHeaders: []string{"From: noreply@arkeep.io", "To: admin@example.com", "Subject: Backup completed"},
			wantBody:    "All good.",
		},
		{
			name:        "display name in From header",
			from:        "noreply@arkeep.io",
			fromName:    "Arkeep",
			to:          []string{"admin@example.com"},
			subject:     "Backup completed",
			body:        "All good.",
			wantHeaders: []string{`From: "Arkeep" <noreply@arkeep.io>`},
			wantBody:    "All good.",
		},
		{
			name:       "CRLF stripped from display name",
			from:       "noreply@arkeep.io",
			fromName:   "Evil\r\nBcc: attacker@evil.com",
			to:         []string{"admin@example.com"},
			subject:    "hi",
			body:       "b",
			wantNoLine: []string{"\r\nBcc: attacker@evil.com"},
			wantBody:   "b",
		},
		{
			name:        "CRLF stripped from subject",
			from:        "noreply@arkeep.io",
			to:          []string{"admin@example.com"},
			subject:     "Backup failed\r\nBcc: attacker@evil.com",
			body:        "boom",
			wantHeaders: []string{"Subject: Backup failedBcc: attacker@evil.com"},
			wantNoLine:  []string{"\r\nBcc: attacker@evil.com"},
			wantBody:    "boom",
		},
		{
			name:        "CRLF stripped from recipient",
			from:        "noreply@arkeep.io",
			to:          []string{"admin@example.com\r\nBcc: attacker@evil.com"},
			subject:     "hi",
			body:        "b",
			wantHeaders: []string{"To: admin@example.comBcc: attacker@evil.com"},
			wantNoLine:  []string{"\r\nBcc: attacker@evil.com"},
			wantBody:    "b",
		},
		{
			name:        "CRLF stripped from sender",
			from:        "noreply@arkeep.io\r\nSender: attacker@evil.com",
			to:          []string{"admin@example.com"},
			subject:     "hi",
			body:        "b",
			wantHeaders: []string{"From: noreply@arkeep.ioSender: attacker@evil.com"},
			wantNoLine:  []string{"\r\nSender: attacker@evil.com"},
			wantBody:    "b",
		},
		{
			name:        "multi-line body preserved",
			from:        "noreply@arkeep.io",
			to:          []string{"admin@example.com"},
			subject:     "Reset",
			body:        "line one\r\n\r\nline two\r\n",
			wantHeaders: []string{"Subject: Reset"},
			wantBody:    "line one\r\n\r\nline two\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw, err := buildEmail(tt.from, tt.fromName, tt.to, tt.subject, tt.body, "")
			if err != nil {
				t.Fatalf("buildEmail: %v", err)
			}
			msg := string(raw)

			parts := strings.SplitN(msg, "\r\n\r\n", 2)
			if len(parts) != 2 {
				t.Fatalf("message has no header/body separator:\n%q", msg)
			}
			headers := parts[0]
			if !strings.Contains(headers, "Content-Transfer-Encoding: quoted-printable") {
				t.Errorf("headers missing quoted-printable encoding:\n%q", headers)
			}
			decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(parts[1])))
			if err != nil {
				t.Fatalf("decode body: %v", err)
			}
			gotBody := string(decoded)

			for _, h := range tt.wantHeaders {
				if !strings.Contains(headers, h) {
					t.Errorf("headers missing %q:\n%q", h, headers)
				}
			}
			for _, bad := range tt.wantNoLine {
				if strings.Contains(headers, bad) {
					t.Errorf("headers contain injected sequence %q:\n%q", bad, headers)
				}
			}
			if gotBody != tt.wantBody {
				t.Errorf("body = %q, want %q", gotBody, tt.wantBody)
			}
		})
	}
}

// TestBuildEmail_HTML checks the structure of an email with an HTML body:
// multipart/alternative with the text first, then the HTML and the inline
// logo in multipart/related, and no line longer than SMTP allows.
func TestBuildEmail_HTML(t *testing.T) {
	text := "Policy \"nightly\" failed: " + strings.Repeat("x", 2000)
	html := `<html><body><img src="cid:arkeep-logo">` + strings.Repeat("y", 2000) + `</body></html>`
	raw, err := buildEmail("noreply@arkeep.io", "Arkeep", []string{"admin@example.com"}, "Backup failed", text, html)
	if err != nil {
		t.Fatalf("buildEmail: %v", err)
	}
	for i, line := range strings.Split(string(raw), "\r\n") {
		if len(line) > 998 {
			t.Fatalf("line %d is %d characters long, SMTP allows 998", i+1, len(line))
		}
	}

	parts := readEmailParts(t, raw)
	if len(parts) != 3 {
		t.Fatalf("got %d leaf parts, want text, html and logo", len(parts))
	}
	if parts[0].contentType != "text/plain" || parts[0].body != text {
		t.Errorf("first part = %s %q..., want the plain text", parts[0].contentType, parts[0].body[:min(40, len(parts[0].body))])
	}
	if parts[1].contentType != "text/html" || parts[1].body != html {
		t.Errorf("second part = %s, want the HTML unchanged", parts[1].contentType)
	}
	if parts[2].contentType != "image/png" || parts[2].contentID != "<arkeep-logo>" || !bytes.Equal([]byte(parts[2].body), logoPNG) {
		t.Errorf("third part = %s %s, want the logo as <arkeep-logo>", parts[2].contentType, parts[2].contentID)
	}
	// A file name makes clients list the logo as an attachment.
	for _, bad := range []string{"Content-Disposition", "filename=", "name="} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("email contains %q, want the logo without file name", bad)
		}
	}
}

type emailPart struct {
	contentType, contentID, body string
}

// readEmailParts parses a message and returns its leaf parts in order, with
// transfer encodings undone (mime/multipart decodes quoted-printable itself).
func readEmailParts(t *testing.T, raw []byte) []emailPart {
	t.Helper()
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("parse message: %v", err)
	}
	var out []emailPart
	var walk func(contentType string, header map[string][]string, body io.Reader)
	walk = func(contentType string, header map[string][]string, body io.Reader) {
		mt, params, err := mime.ParseMediaType(contentType)
		if err != nil {
			t.Fatalf("parse content type %q: %v", contentType, err)
		}
		if strings.HasPrefix(mt, "multipart/") {
			mr := multipart.NewReader(body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					return
				}
				if err != nil {
					t.Fatalf("read part: %v", err)
				}
				walk(p.Header.Get("Content-Type"), p.Header, p)
			}
		}
		data, err := io.ReadAll(body)
		if err != nil {
			t.Fatalf("read %s: %v", mt, err)
		}
		get := func(k string) string {
			if v := header[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		switch strings.ToLower(get("Content-Transfer-Encoding")) {
		case "base64":
			data, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(data)))
			if err != nil {
				t.Fatalf("decode base64: %v", err)
			}
		case "quoted-printable":
			data, err = io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
			if err != nil {
				t.Fatalf("decode quoted-printable: %v", err)
			}
		}
		out = append(out, emailPart{contentType: mt, contentID: get("Content-Id"), body: string(data)})
	}
	walk(msg.Header.Get("Content-Type"), msg.Header, msg.Body)
	return out
}

func TestIsBareAddress(t *testing.T) {
	ok := []string{"noreply@arkeep.io", "a.b+c@example.com"}
	bad := []string{
		"", "not-an-address", "Arkeep <noreply@arkeep.io>", "noreply@arkeep.io (x)",
		"noreply@arkeep.io\r\nBcc: attacker@evil.com", "a@b.com, c@d.com",
	}
	for _, s := range ok {
		if !IsBareAddress(s) {
			t.Errorf("IsBareAddress(%q) = false, want true", s)
		}
	}
	for _, s := range bad {
		if IsBareAddress(s) {
			t.Errorf("IsBareAddress(%q) = true, want false", s)
		}
	}
}
