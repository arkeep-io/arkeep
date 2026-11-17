package notification

import (
	"context"
	"html/template"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

func TestNewAgentEmail(t *testing.T) {
	when := time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC)

	got := newAgentEmail(false, "Agent offline: web-01", "web-01", when)
	want := emailContent{
		Title:       "Agent offline: web-01",
		Preheader:   "web-01 stopped responding.",
		StatusLabel: "Offline", StatusColor: amber, StatusBG: amberBG, StatusFG: amberFG,
		Headline: "Agent offline",
		Summary:  "web-01 stopped responding.",
		Details:  []emailItem{{"Offline since", "Nov 5, 2026, 02:00"}},
		Footer:   "Sent by your Arkeep server. Times are in UTC.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("offline email:\n%+v\nwant:\n%+v", got, want)
	}

	got = newAgentEmail(true, "Agent back online: web-01", "web-01", when)
	want = emailContent{
		Title:       "Agent back online: web-01",
		Preheader:   "web-01 reconnected.",
		StatusLabel: "Online", StatusColor: greenStripe, StatusBG: greenBG, StatusFG: green,
		Headline: "Agent back online",
		Summary:  "web-01 reconnected.",
		Details:  []emailItem{{"Online since", "Nov 5, 2026, 02:00"}},
		Footer:   "Sent by your Arkeep server. Times are in UTC.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("online email:\n%+v\nwant:\n%+v", got, want)
	}
}

// TestEmailBodies_Agent checks that agent notifications are sent as text and
// HTML built from the payload, with the agent name escaped and the time in
// the server's zone.
func TestEmailBodies_Agent(t *testing.T) {
	old := emailLocation
	emailLocation = time.FixedZone("CET", 3600)
	defer func() { emailLocation = old }()

	svc, _ := newTestService(t)
	svc.baseURL = testBaseURL
	n := &db.Notification{
		Type:      "agent_offline",
		Title:     "Agent offline: <b>web</b>",
		Body:      "short body",
		Payload:   `{"agent_id":"0192f3a1-7c2e-7b3a-9d41-5e6f7a8b9c0d","agent_name":"<b>web</b>"}`,
		CreatedAt: time.Date(2026, 11, 5, 2, 0, 0, 0, time.UTC),
	}
	text, html := svc.emailBodies(context.Background(), n)

	wantText := `Agent offline
<b>web</b> stopped responding.

Offline since:  Nov 5, 2026, 03:00

-- 
Sent by your Arkeep server. Times are in CET.
`
	if text != wantText {
		t.Errorf("text:\n%s\nwant:\n%s", text, wantText)
	}
	for _, want := range []string{"Agent offline</h1>", "&lt;b&gt;web&lt;/b&gt; stopped responding.", ">Offline<", ">Offline since<", "Nov 5, 2026, 03:00", "Times are in CET.", `src="cid:arkeep-logo"`} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	for _, bad := range []string{"<b>web</b>", "<a ", "View agent"} {
		if strings.Contains(html, bad) {
			t.Errorf("HTML contains %q", bad)
		}
	}

	n.Type, n.Title = "agent_online", "Agent back online: web"
	text, html = svc.emailBodies(context.Background(), n)
	if !strings.HasPrefix(text, "Agent back online\n<b>web</b> reconnected.\n\nOnline since:  Nov 5, 2026, 03:00\n") {
		t.Errorf("online text:\n%s", text)
	}
	for _, want := range []string{"Agent back online</h1>", ">Online<", ">Online since<"} {
		if !strings.Contains(html, want) {
			t.Errorf("online HTML is missing %q", want)
		}
	}
}

func TestPasswordResetEmail(t *testing.T) {
	link := "https://arkeep.example.com/auth/reset-password?token=abc_DEF-123"
	subject, text, html, err := PasswordResetEmail(link, time.Hour)
	if err != nil {
		t.Fatalf("PasswordResetEmail: %v", err)
	}
	if subject != "Reset your Arkeep password" {
		t.Errorf("subject = %q", subject)
	}

	wantText := `Reset your password
A password reset was requested for your Arkeep account.
The link expires in 1 hour.

Choose a new password: ` + link + `

If you did not request this, you can ignore this email: your password will not change.

-- 
Sent by your Arkeep server.
`
	if text != wantText {
		t.Errorf("text:\n%s\nwant:\n%s", text, wantText)
	}

	// The link is the button and is written out below it.
	for _, want := range []string{
		"Reset your password</h1>", ">Account<",
		"A password reset was requested for your Arkeep account.<br>The link expires in 1 hour.",
		`href="` + link + `"`, ">Choose a new password</a>",
		"copy this link into your browser:", ">" + link + "</td>",
		"If you did not request this", `src="cid:arkeep-logo"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
	if strings.Contains(html, "Times are in") {
		t.Error("HTML names a time zone, but the email shows no time")
	}
}

func TestFormatExpiry(t *testing.T) {
	tests := []struct {
		d    time.Duration
		want string
	}{
		{time.Hour, "1 hour"},
		{2 * time.Hour, "2 hours"},
		{30 * time.Minute, "30 minutes"},
		{90 * time.Minute, "90 minutes"},
		{time.Minute, "1 minute"},
	}
	for _, tt := range tests {
		if got := formatExpiry(tt.d); got != tt.want {
			t.Errorf("formatExpiry(%v) = %q, want %q", tt.d, got, tt.want)
		}
	}
}

// TestSendTestEmail checks that the SMTP test is sent like the other emails:
// plain text, HTML and the logo.
func TestSendTestEmail(t *testing.T) {
	port, received := startFakeSMTP(t)
	cfg := SMTPConfig{Host: "127.0.0.1", Port: port, From: "noreply@test.local"}
	if err := SendTestEmail(context.Background(), cfg, "admin@test.local"); err != nil {
		t.Fatalf("SendTestEmail: %v", err)
	}
	var msg []byte
	select {
	case msg = <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the SMTP server")
	}
	if !strings.Contains(string(msg), "Subject: Arkeep test email") {
		t.Error("message has no test subject")
	}
	parts := readEmailParts(t, msg)
	if len(parts) != 3 || parts[0].contentType != "text/plain" || parts[1].contentType != "text/html" || parts[2].contentType != "image/png" {
		t.Fatalf("test email has parts %+v, want text, HTML and logo", parts)
	}
	const sentence = "If you can read this, Arkeep can send emails with these SMTP settings."
	if text := strings.ReplaceAll(parts[0].body, "\r\n", "\n"); !strings.HasPrefix(text, "Test email\n"+sentence+"\n") {
		t.Errorf("text part:\n%s", text)
	}
	for _, want := range []string{"Test email</h1>", sentence, ">Test<"} {
		if !strings.Contains(parts[1].body, want) {
			t.Errorf("HTML is missing %q", want)
		}
	}
}

// breakEmailTemplate makes rendering the HTML email fail until the test ends.
func breakEmailTemplate(t *testing.T) {
	t.Helper()
	old := emailTemplate
	emailTemplate = template.Must(template.New("broken").Parse("{{.NoSuchField}}"))
	t.Cleanup(func() { emailTemplate = old })
}

// TestPasswordResetEmail_RenderFailure checks that the plain text with the
// link is still returned when the HTML body cannot be rendered.
func TestPasswordResetEmail_RenderFailure(t *testing.T) {
	breakEmailTemplate(t)
	link := "https://arkeep.example.com/auth/reset-password?token=abc"
	subject, text, html, err := PasswordResetEmail(link, time.Hour)
	if err == nil || html != "" {
		t.Fatalf("err = %v, HTML %q; want an error and no HTML", err, html)
	}
	if subject != "Reset your Arkeep password" || !strings.Contains(text, "Choose a new password: "+link+"\n") {
		t.Errorf("subject %q, text:\n%s\nwant the usual subject and the link", subject, text)
	}
}

// TestSendTestEmail_RenderFailure checks that the SMTP test is still sent, as
// plain text only, when the HTML body cannot be rendered.
func TestSendTestEmail_RenderFailure(t *testing.T) {
	breakEmailTemplate(t)
	port, received := startFakeSMTP(t)
	cfg := SMTPConfig{Host: "127.0.0.1", Port: port, From: "noreply@test.local"}
	if err := SendTestEmail(context.Background(), cfg, "admin@test.local"); err != nil {
		t.Fatalf("SendTestEmail: %v", err)
	}
	var msg string
	select {
	case m := <-received:
		msg = strings.ReplaceAll(string(m), "\r\n", "\n")
	case <-time.After(5 * time.Second):
		t.Fatal("no message reached the SMTP server")
	}
	if !strings.Contains(msg, "Content-Type: text/plain") || strings.Contains(msg, "multipart/") || strings.Contains(msg, "text/html") {
		t.Errorf("message is not plain text only:\n%s", msg)
	}
	if !strings.Contains(msg, "Test email\nIf you can read this, Arkeep can send emails with these SMTP settings.\n") {
		t.Errorf("message is missing the test text:\n%s", msg)
	}
}
