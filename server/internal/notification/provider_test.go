package notification

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProviderURL(t *testing.T) {
	tests := []struct {
		name    string
		s       ProviderSettings
		want    string
		wantErr bool
	}{
		{"gotify https", ProviderSettings{Type: ProviderGotify,
			Config: map[string]string{"url": "https://push.example.com"}, Secrets: map[string]string{"token": "Atok"}},
			"gotify://push.example.com/Atok", false},
		{"gotify bare host, port, base path", ProviderSettings{Type: ProviderGotify,
			Config: map[string]string{"url": "push.example.com:8080/gotify/"}, Secrets: map[string]string{"token": "Atok"}},
			"gotify://push.example.com:8080/gotify/Atok", false},
		{"gotify http", ProviderSettings{Type: ProviderGotify,
			Config: map[string]string{"url": "http://10.0.0.2"}, Secrets: map[string]string{"token": "Atok"}},
			"gotify://10.0.0.2/Atok?disabletls=yes", false},
		{"gotify url with credentials", ProviderSettings{Type: ProviderGotify,
			Config: map[string]string{"url": "https://u:p@push.example.com"}, Secrets: map[string]string{"token": "Atok"}},
			"", true},
		{"ntfy with token", ProviderSettings{Type: ProviderNtfy,
			Config: map[string]string{"url": "https://ntfy.sh"}, Secrets: map[string]string{"topic": "backups", "token": "tk_1"}},
			"ntfy://ntfy.sh/backups?scheme=https&token=tk_1", false},
		{"pushover", ProviderSettings{Type: ProviderPushover,
			Config: map[string]string{"devices": "phone, tablet"}, Secrets: map[string]string{"api_token": "app", "user_key": "usr"}},
			"pushover://shoutrrr:app@usr/?devices=phone%2Ctablet", false},
		{"telegram", ProviderSettings{Type: ProviderTelegram,
			Config: map[string]string{"chats": "123, @channel"}, Secrets: map[string]string{"bot_token": "123456789:abc"}},
			"telegram://123456789:abc@telegram?chats=123%2C%40channel", false},
		{"matrix", ProviderSettings{Type: ProviderMatrix,
			Config: map[string]string{"url": "https://matrix.example.com", "user": "bot", "rooms": "!a:example.com"}, Secrets: map[string]string{"password": "p@ss"}},
			"matrix://bot:p%40ss@matrix.example.com/?rooms=%21a%3Aexample.com", false},
		{"matrix with path", ProviderSettings{Type: ProviderMatrix,
			Config: map[string]string{"url": "https://matrix.example.com/x", "user": "bot"}, Secrets: map[string]string{"password": "p"}},
			"", true},
		{"discord", ProviderSettings{Type: ProviderDiscord,
			Secrets: map[string]string{"webhook_url": "https://discord.com/api/webhooks/123/tok-en"}},
			"discord://tok-en@123", false},
		{"discord other host", ProviderSettings{Type: ProviderDiscord,
			Secrets: map[string]string{"webhook_url": "https://evil.example.com/api/webhooks/123/tok"}},
			"", true},
		{"slack", ProviderSettings{Type: ProviderSlack,
			Secrets: map[string]string{"webhook_url": "https://hooks.slack.com/services/T0/B0/XX"}},
			"slack://hook:T0-B0-XX@webhook", false},
		{"google chat", ProviderSettings{Type: ProviderGoogleChat,
			Secrets: map[string]string{"webhook_url": "https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t"}},
			"googlechat://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t", false},
		{"mattermost", ProviderSettings{Type: ProviderMattermost,
			Secrets: map[string]string{"webhook_url": "https://mm.example.com/hooks/abc"}},
			"mattermost://mm.example.com/abc", false},
		{"mattermost subpath", ProviderSettings{Type: ProviderMattermost,
			Secrets: map[string]string{"webhook_url": "https://mm.example.com/mm/hooks/abc"}},
			"", true},
		{"custom", ProviderSettings{Type: ProviderCustom,
			Secrets: map[string]string{"url": "bark://key@api.day.app"}},
			"bark://key@api.day.app", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := providerURL(tt.s)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("url = %q, want %q", got, tt.want)
			}
		})
	}
}

// fakeTelegramToken has the shape of a bot token (ID, colon, 35 characters).
// It is built at run time so that no literal in the source looks like a real
// token to secret scanners.
var fakeTelegramToken = "123456789:" + strings.Repeat("x", 35)

func TestValidateProvider(t *testing.T) {
	gotify := func(priority string) ProviderSettings {
		return ProviderSettings{Type: ProviderGotify,
			Config:     map[string]string{"url": "https://push.example.com"},
			Secrets:    map[string]string{"token": "AbCdEfGhIjKlMnO"},
			Priorities: map[string]string{"job_failure": priority}}
	}
	tests := []struct {
		name    string
		s       ProviderSettings
		wantErr string
	}{
		{"gotify with priority", gotify("8"), ""},
		{"gotify without priority", gotify(""), ""},
		{"gotify priority not a number", gotify("high"), "job_failure priority"},
		{"missing required field", ProviderSettings{Type: ProviderTelegram,
			Secrets: map[string]string{"bot_token": "1:a"}}, "chats is required"},
		{"unknown type", ProviderSettings{Type: "fax"}, "unknown provider type"},
		{"ntfy named priority", ProviderSettings{Type: ProviderNtfy,
			Config: map[string]string{"url": "ntfy.sh"}, Secrets: map[string]string{"topic": "t"},
			Priorities: map[string]string{"job_success": "min", "job_failure": "5"}}, ""},
		{"ntfy priority 0", ProviderSettings{Type: ProviderNtfy,
			Config: map[string]string{"url": "ntfy.sh"}, Secrets: map[string]string{"topic": "backups"},
			Priorities: map[string]string{"job_success": "0"}}, "job_success priority"},
		{"telegram", ProviderSettings{Type: ProviderTelegram,
			Config: map[string]string{"chats": "123"}, Secrets: map[string]string{"bot_token": fakeTelegramToken}}, ""},
		{"matrix password with @", ProviderSettings{Type: ProviderMatrix,
			Config: map[string]string{"url": "https://matrix.example.com", "user": "bot"}, Secrets: map[string]string{"password": "p@ss/word"}}, ""},
		{"discord has no priority", ProviderSettings{Type: ProviderDiscord,
			Secrets:    map[string]string{"webhook_url": "https://discord.com/api/webhooks/123/tok"},
			Priorities: map[string]string{"agent_offline": "5"}}, "does not accept a priority"},
		{"custom url of unknown service", ProviderSettings{Type: ProviderCustom,
			Secrets: map[string]string{"url": "fax://secret-number"}}, "unknown service"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateProvider(tt.s)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			for _, v := range tt.s.Secrets {
				if strings.Contains(err.Error(), v) {
					t.Fatalf("error %q leaks secret %q", err, v)
				}
			}
		})
	}
}

func TestSendProvider_TitleAndPriority(t *testing.T) {
	var got map[string]any
	var gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.URL.Query().Get("token")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	s := ProviderSettings{Type: ProviderGotify,
		Config:     map[string]string{"url": srv.URL},
		Secrets:    map[string]string{"token": "AbCdEfGhIjKlMnO"},
		Priorities: map[string]string{"job_failure": "8", "job_success": "0"}}
	if err := SendProvider(context.Background(), s, "job_failure", "Backup failed: web", "it broke"); err != nil {
		t.Fatalf("SendProvider: %v", err)
	}
	if gotToken != "AbCdEfGhIjKlMnO" {
		t.Errorf("token = %q", gotToken)
	}
	if got["title"] != "Backup failed: web" || got["message"] != "it broke" || got["priority"] != float64(8) {
		t.Errorf("body = %v", got)
	}
}

func TestSendProvider_TitleInMessageWithoutTitleParam(t *testing.T) {
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()

	// Mattermost has no title parameter.
	s := ProviderSettings{Type: ProviderMattermost,
		Secrets: map[string]string{"webhook_url": srv.URL + "/hooks/abc"}}
	if err := SendProvider(context.Background(), s, "job_success", "Backup completed: web", "all good"); err != nil {
		t.Fatalf("SendProvider: %v", err)
	}
	if !strings.Contains(body, `Backup completed: web\n`) || !strings.Contains(body, "all good") {
		t.Errorf("body = %s", body)
	}
}

func TestSendProvider_ErrorRedactsSecrets(t *testing.T) {
	// Nothing listens there: the dial error quotes the request URL.
	s := ProviderSettings{Type: ProviderGotify,
		Config:  map[string]string{"url": "http://127.0.0.1:1"},
		Secrets: map[string]string{"token": "AbCdEfGhIjKlMnO"}}
	err := SendProvider(context.Background(), s, "job_success", "t", "b")
	if !errors.Is(err, ErrSendFailed) {
		t.Fatalf("err = %v, want ErrSendFailed", err)
	}
	if strings.Contains(err.Error(), "AbCdEfGhIjKlMnO") {
		t.Fatalf("error leaks the token: %v", err)
	}
}

func TestRedact_URLSecretParts(t *testing.T) {
	secrets := map[string]string{
		"webhook_url": "https://discord.com/api/webhooks/123456789/SeCrEtToKeN",
		"url":         "bark://DeViCeKeY@api.day.app?sound=minuet",
	}
	err := redact(errors.New(`Post "https://discord.com/api/v10/webhooks/123456789/SeCrEtToKeN": EOF; `+
		`bark: device DeViCeKeY failed`), secrets)
	for _, leak := range []string{"SeCrEtToKeN", "123456789", "DeViCeKeY"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("redacted error %q still contains %q", err, leak)
		}
	}
	custom := redact(errors.New(`Post "https://ntfy.sh/arkeepalerts": EOF; pushover user UsErKeY99: invalid`),
		map[string]string{"a": "ntfy://ntfy.sh/arkeepalerts", "b": "pushover://shoutrrr:tok@UsErKeY99/"})
	for _, leak := range []string{"arkeepalerts", "UsErKeY99"} {
		if strings.Contains(custom.Error(), leak) {
			t.Errorf("redacted error %q still contains %q", custom, leak)
		}
	}
	escaped := redact(errors.New(`matrix://bot:p%40ss%2Fword@host failed`), map[string]string{"password": "p@ss/word"})
	if strings.Contains(escaped.Error(), "p%40ss") {
		t.Errorf("redacted error %q still contains the escaped password", escaped)
	}
	if !strings.Contains(err.Error(), "discord.com/api/v10/webhooks") {
		t.Errorf("redacted error %q lost the non-secret parts", err)
	}
}
