package notification

import (
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
			msg := string(buildEmail(tt.from, tt.fromName, tt.to, tt.subject, tt.body))

			parts := strings.SplitN(msg, "\r\n\r\n", 2)
			if len(parts) != 2 {
				t.Fatalf("message has no header/body separator:\n%q", msg)
			}
			headers, gotBody := parts[0], parts[1]

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
