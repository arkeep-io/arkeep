package notification

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/format"
	"github.com/nicholas-fedor/shoutrrr/pkg/router"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"

	"github.com/arkeep-io/arkeep/server/internal/db"
)

// Notification provider types. Each one is a push or chat service whose
// shoutrrr URL is built from the provider's fields by providerURL (ADR 0019).
const (
	ProviderGotify     = "gotify"
	ProviderNtfy       = "ntfy"
	ProviderPushover   = "pushover"
	ProviderTelegram   = "telegram"
	ProviderDiscord    = "discord"
	ProviderMatrix     = "matrix"
	ProviderSlack      = "slack"
	ProviderTeams      = "teams"
	ProviderGoogleChat = "googlechat"
	ProviderMattermost = "mattermost"
	// ProviderCustom takes a raw shoutrrr URL, for the services without a
	// dedicated form.
	ProviderCustom = "custom"
)

// providerField describes one input of a provider type.
type providerField struct {
	key      string
	secret   bool // stored encrypted in Secrets, never returned by the API
	required bool
	// target marks the field naming the server the secrets are sent to. When
	// its host changes, the stored secrets are not reused (S-17).
	target bool
}

// providerTypes lists the fields of every provider type. The GUI form of each
// type must offer the same fields.
var providerTypes = map[string][]providerField{
	ProviderGotify: {
		{key: "url", required: true, target: true},
		{key: "token", secret: true, required: true},
	},
	ProviderNtfy: {
		{key: "url", required: true, target: true},
		// On a public server the topic is the only thing protecting it.
		{key: "topic", secret: true, required: true},
		{key: "token", secret: true},
	},
	ProviderPushover: {
		{key: "devices"},
		{key: "api_token", secret: true, required: true},
		{key: "user_key", secret: true, required: true},
	},
	ProviderTelegram: {
		{key: "chats", required: true},
		{key: "bot_token", secret: true, required: true},
	},
	ProviderMatrix: {
		{key: "url", required: true, target: true},
		{key: "user", required: true},
		{key: "rooms"},
		{key: "password", secret: true, required: true},
	},
	ProviderDiscord:    {{key: "webhook_url", secret: true, required: true}},
	ProviderSlack:      {{key: "webhook_url", secret: true, required: true}},
	ProviderTeams:      {{key: "webhook_url", secret: true, required: true}},
	ProviderGoogleChat: {{key: "webhook_url", secret: true, required: true}},
	ProviderMattermost: {{key: "webhook_url", secret: true, required: true}},
	ProviderCustom:     {{key: "url", secret: true, required: true}},
}

// IsProviderType reports whether t is a known notification provider type.
func IsProviderType(t string) bool {
	_, ok := providerTypes[t]
	return ok
}

// SplitProviderFields splits the submitted fields of a provider of type t into
// its config (non-secret) and secrets, dropping keys the type does not have and
// trimming spaces. Returns an error for an unknown type.
func SplitProviderFields(t string, fields map[string]string) (config, secrets map[string]string, err error) {
	defs, ok := providerTypes[t]
	if !ok {
		return nil, nil, fmt.Errorf("unknown provider type %q", t)
	}
	config = map[string]string{}
	secrets = map[string]string{}
	for _, f := range defs {
		v := strings.TrimSpace(fields[f.key])
		if v == "" {
			continue
		}
		if f.secret {
			secrets[f.key] = v
		} else {
			config[f.key] = v
		}
	}
	return config, secrets, nil
}

// ProviderTargetHost returns the server a provider of type t sends its secrets
// to, as scheme://host[:port] of its target field, or "" when the type has no
// such field: its server is fixed (Telegram, Pushover) or is itself a secret
// (webhook URLs, custom).
func ProviderTargetHost(t string, config map[string]string) string {
	for _, f := range providerTypes[t] {
		if !f.target {
			continue
		}
		raw := strings.TrimSpace(config[f.key])
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return raw
		}
		return strings.ToLower(u.Scheme + "://" + u.Host)
	}
	return ""
}

// ProviderSecretKeys returns the secret field keys of provider type t.
func ProviderSecretKeys(t string) []string {
	var keys []string
	for _, f := range providerTypes[t] {
		if f.secret {
			keys = append(keys, f.key)
		}
	}
	return keys
}

// ProviderSettings is a notification provider as the sender needs it: its
// type, fields and per-event priorities.
type ProviderSettings struct {
	Type    string
	Config  map[string]string
	Secrets map[string]string
	// Priorities maps an event type (job_success, job_failure, agent_offline,
	// agent_online) to shoutrrr's priority parameter; absent or empty leaves
	// the service default.
	Priorities map[string]string
}

// ProviderSettingsFrom decodes a stored provider.
func ProviderSettingsFrom(p *db.NotificationProvider) (ProviderSettings, error) {
	s := ProviderSettings{
		Type:    p.Type,
		Config:  map[string]string{},
		Secrets: map[string]string{},
		Priorities: map[string]string{
			"job_success":   p.PriorityJobSuccess,
			"job_failure":   p.PriorityJobFailure,
			"agent_offline": p.PriorityAgentOffline,
			"agent_online":  p.PriorityAgentOnline,
		},
	}
	if p.Config != "" {
		if err := json.Unmarshal([]byte(p.Config), &s.Config); err != nil {
			return s, fmt.Errorf("%w: provider config is not valid JSON", ErrInvalidConfig)
		}
	}
	if p.Secrets != "" {
		if err := json.Unmarshal([]byte(p.Secrets), &s.Secrets); err != nil {
			return s, fmt.Errorf("%w: provider secrets are not valid JSON", ErrInvalidConfig)
		}
	}
	return s, nil
}

// ValidateProvider checks that s has every required field, that they form a
// URL shoutrrr accepts, and that every priority is accepted by the service.
// Nothing is sent. The error is safe to show: it never contains a secret.
func ValidateProvider(s ProviderSettings) error {
	defs, ok := providerTypes[s.Type]
	if !ok {
		return fmt.Errorf("unknown provider type %q", s.Type)
	}
	for _, f := range defs {
		src := s.Config
		if f.secret {
			src = s.Secrets
		}
		if f.required && strings.TrimSpace(src[f.key]) == "" {
			return fmt.Errorf("%s is required", f.key)
		}
	}
	rawURL, err := providerURL(s)
	if err != nil {
		return err
	}
	if _, err := locateService(rawURL); err != nil {
		return redact(err, s.Secrets)
	}
	for _, event := range eventTypes {
		v := s.Priorities[event]
		if v == "" {
			continue
		}
		if err := checkParam(rawURL, "priority", v); err != nil {
			return fmt.Errorf("%s priority: %w", event, redact(err, s.Secrets))
		}
	}
	return nil
}

// eventTypes are the events a provider can be given a priority for.
var eventTypes = []string{"job_success", "job_failure", "agent_offline", "agent_online"}

// providerSendTimeout bounds one delivery to a provider.
const providerSendTimeout = 15 * time.Second

// SendProvider delivers one notification of type eventType to the provider s.
// The title goes in shoutrrr's title parameter when the service has one, and
// at the top of the message otherwise. A failure is wrapped in ErrSendFailed
// and never contains a secret.
func SendProvider(ctx context.Context, s ProviderSettings, eventType, title, body string) error {
	rawURL, err := providerURL(s)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrSendFailed, err)
	}
	service, err := locateService(rawURL)
	if err != nil {
		return fmt.Errorf("%w: %s", ErrSendFailed, redact(err, s.Secrets))
	}
	fields := queryFields(service)

	params := types.Params{}
	message := body
	if slices.Contains(fields, "title") {
		params["title"] = title
	} else {
		message = title + "\n\n" + body
	}
	if p := s.Priorities[eventType]; p != "" {
		params["priority"] = p
	}

	ctx, cancel := context.WithTimeout(ctx, providerSendTimeout)
	defer cancel()
	if err := shoutrrr.SendContext(ctx, rawURL, message, &params); err != nil {
		return fmt.Errorf("%w: %s", ErrSendFailed, redact(err, s.Secrets))
	}
	return nil
}

// SendTestProvider sends a test message to the provider s with the priority
// of job_failure, after validating it.
func SendTestProvider(ctx context.Context, s ProviderSettings) error {
	if err := ValidateProvider(s); err != nil {
		return err
	}
	return SendProvider(ctx, s, "job_failure", "Arkeep test notification",
		"This is a test notification from Arkeep. If you received it, this notification provider works.")
}

// locateService parses rawURL into its shoutrrr service without sending.
func locateService(rawURL string) (types.Service, error) {
	r, err := router.NewWithOptions(nil, types.SenderOptions{})
	if err != nil {
		return nil, err
	}
	return r.Locate(rawURL)
}

// queryFields returns the parameters the service accepts, or nil when they
// cannot be read.
func queryFields(service types.Service) (fields []string) {
	// GetServiceConfig panics for a service without a Config field.
	defer func() {
		if recover() != nil {
			fields = nil
		}
	}()
	pkr := format.NewPropKeyResolver(format.GetServiceConfig(service))
	return pkr.QueryFields()
}

// checkParam reports whether the service of rawURL accepts value for the
// parameter key, without sending.
func checkParam(rawURL, key, value string) (err error) {
	service, err := locateService(rawURL)
	if err != nil {
		return err
	}
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("this service does not accept a %s", key)
		}
	}()
	pkr := format.NewPropKeyResolver(format.GetServiceConfig(service))
	if !slices.Contains(pkr.QueryFields(), key) {
		return fmt.Errorf("this service does not accept a %s", key)
	}
	return pkr.Set(key, value)
}

// minRedactLen is the length from which redact hides a secret value.
const minRedactLen = 4

// redact replaces every secret value in err's message with "***", in plain
// and URL-escaped form: shoutrrr and net/http errors may quote the URL, which
// carries the secrets.
func redact(err error, secrets map[string]string) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	for _, secret := range secrets {
		for _, v := range secretFragments(secret) {
			// A value this short would garble the message more than it hides.
			if len(v) < minRedactLen {
				continue
			}
			userinfo := strings.TrimSuffix(url.User(v).String(), "@")
			quoted := strings.Trim(strconv.Quote(v), `"`)
			for _, form := range []string{v, url.QueryEscape(v), url.PathEscape(v), userinfo, quoted} {
				msg = strings.ReplaceAll(msg, form, "***")
			}
		}
	}
	return errors.New(msg)
}

// secretFragments returns secret and, when it is a URL (a webhook URL, a
// custom shoutrrr URL), its user info, path segments and query values: the
// service calls a URL built from these parts, which an error may quote without
// the secret as a whole. In an http(s) webhook URL only the token-like path
// segments count; in a shoutrrr URL every segment and the host may be a
// secret (an ntfy topic, a Pushover user key).
func secretFragments(secret string) []string {
	out := []string{secret}
	u, err := url.Parse(secret)
	if err != nil || u.Scheme == "" {
		return out
	}
	webhook := u.Scheme == "http" || u.Scheme == "https"
	if !webhook {
		out = append(out, u.Hostname())
	}
	if u.User != nil {
		out = append(out, u.User.Username())
		if p, ok := u.User.Password(); ok {
			out = append(out, p)
		}
	}
	for _, seg := range strings.Split(u.Path, "/") {
		if !webhook || tokenLike(seg) {
			out = append(out, seg)
		}
	}
	for _, vs := range u.Query() {
		out = append(out, vs...)
	}
	return out
}

// tokenLike reports whether a URL path segment may be an ID or a token rather
// than a fixed word such as "webhooks": it has a digit or an upper-case letter,
// or is long.
func tokenLike(seg string) bool {
	return len(seg) >= 16 || strings.ContainsFunc(seg, func(r rune) bool {
		return ('0' <= r && r <= '9') || ('A' <= r && r <= 'Z')
	})
}

// providerURL builds the shoutrrr URL of s. Its errors never contain a secret.
func providerURL(s ProviderSettings) (string, error) {
	c, sec := s.Config, s.Secrets
	switch s.Type {
	case ProviderGotify:
		base, err := parseServerURL(c["url"], true)
		if err != nil {
			return "", err
		}
		if !validPathSegment(sec["token"]) {
			return "", errors.New("token must contain only letters, digits, '.', '-' and '_'")
		}
		u := &url.URL{Scheme: "gotify", Host: base.Host, Path: base.Path + "/" + sec["token"]}
		return withTLS(u, base), nil
	case ProviderNtfy:
		base, err := parseServerURL(c["url"], true)
		if err != nil {
			return "", err
		}
		if !validPathSegment(sec["topic"]) {
			return "", errors.New("topic must contain only letters, digits, '.', '-' and '_'")
		}
		u := &url.URL{Scheme: "ntfy", Host: base.Host, Path: base.Path + "/" + sec["topic"]}
		q := url.Values{"scheme": {base.Scheme}}
		if t := sec["token"]; t != "" {
			q.Set("token", t)
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	case ProviderPushover:
		u := &url.URL{
			Scheme: "pushover",
			User:   url.UserPassword("shoutrrr", sec["api_token"]),
			Host:   sec["user_key"],
			Path:   "/",
		}
		if d := joinList(c["devices"]); d != "" {
			u.RawQuery = url.Values{"devices": {d}}.Encode()
		}
		return u.String(), nil
	case ProviderTelegram:
		// shoutrrr reads the token from the raw userinfo: its ":" must stay
		// unescaped, which url.User would not do.
		q := url.Values{"chats": {joinList(c["chats"])}}.Encode()
		return "telegram://" + url.PathEscape(sec["bot_token"]) + "@telegram?" + q, nil
	case ProviderMatrix:
		base, err := parseServerURL(c["url"], false)
		if err != nil {
			return "", err
		}
		u := &url.URL{Scheme: "matrix", User: url.UserPassword(c["user"], sec["password"]), Host: base.Host, Path: "/"}
		q := url.Values{}
		if r := joinList(c["rooms"]); r != "" {
			q.Set("rooms", r)
		}
		if base.Scheme == "http" {
			q.Set("disabletls", "yes")
		}
		u.RawQuery = q.Encode()
		return u.String(), nil
	case ProviderDiscord:
		return discordURL(sec["webhook_url"])
	case ProviderSlack:
		return slackURL(sec["webhook_url"])
	case ProviderTeams:
		hook, err := parseWebhookURL(sec["webhook_url"])
		if err != nil {
			return "", err
		}
		u := &url.URL{Scheme: "teams", Path: "/"}
		u.RawQuery = url.Values{"host": {hook.String()}}.Encode()
		return u.String(), nil
	case ProviderGoogleChat:
		hook, err := parseWebhookURL(sec["webhook_url"])
		if err != nil {
			return "", err
		}
		if hook.Host != "chat.googleapis.com" {
			return "", errors.New("webhook_url must be a chat.googleapis.com URL")
		}
		hook.Scheme = "googlechat"
		return hook.String(), nil
	case ProviderMattermost:
		return mattermostURL(sec["webhook_url"])
	case ProviderCustom:
		return sec["url"], nil
	}
	return "", fmt.Errorf("unknown provider type %q", s.Type)
}

// segmentCharsRe matches the characters of a Gotify token or an ntfy topic.
var segmentCharsRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// validPathSegment reports whether v can be appended as one URL path segment
// without changing the rest of the path: no "/", and not "." or "..".
func validPathSegment(v string) bool {
	return segmentCharsRe.MatchString(v) && v != "." && v != ".."
}

// parseServerURL parses the URL of a self-hosted server. A bare host means
// https. allowPath allows a base path, for a server behind a reverse proxy.
func parseServerURL(raw string, allowPath bool) (*url.URL, error) {
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("url must be an http(s) server URL, such as https://push.example.com")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !allowPath && u.Path != "" {
		return nil, errors.New("url must not have a path")
	}
	return u, nil
}

// withTLS adds disabletls=yes to a gotify URL when the server is plain http.
func withTLS(u *url.URL, base *url.URL) string {
	if base.Scheme == "http" {
		u.RawQuery = url.Values{"disabletls": {"yes"}}.Encode()
	}
	return u.String()
}

// parseWebhookURL parses the https URL of an incoming webhook. Its error does
// not quote the URL, which is a secret.
func parseWebhookURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return nil, errors.New("webhook_url must be the https webhook URL given by the service")
	}
	return u, nil
}

// discordURL converts https://discord.com/api/webhooks/ID/TOKEN.
func discordURL(raw string) (string, error) {
	u, err := parseWebhookURL(raw)
	if err != nil {
		return "", err
	}
	host := strings.TrimPrefix(strings.TrimPrefix(u.Hostname(), "ptb."), "canary.")
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if (host != "discord.com" && host != "discordapp.com") || len(parts) != 4 ||
		parts[0] != "api" || parts[1] != "webhooks" || parts[2] == "" || parts[3] == "" {
		return "", errors.New("webhook_url must be a Discord webhook URL (https://discord.com/api/webhooks/...)")
	}
	return (&url.URL{Scheme: "discord", User: url.User(parts[3]), Host: parts[2]}).String(), nil
}

// slackURL converts https://hooks.slack.com/services/A/B/C.
func slackURL(raw string) (string, error) {
	u, err := parseWebhookURL(raw)
	if err != nil {
		return "", err
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if u.Host != "hooks.slack.com" || len(parts) != 4 || parts[0] != "services" ||
		parts[1] == "" || parts[2] == "" || parts[3] == "" {
		return "", errors.New("webhook_url must be a Slack webhook URL (https://hooks.slack.com/services/...)")
	}
	token := parts[1] + "-" + parts[2] + "-" + parts[3]
	return (&url.URL{Scheme: "slack", User: url.UserPassword("hook", token), Host: "webhook"}).String(), nil
}

// mattermostURL converts https://HOST/hooks/TOKEN.
func mattermostURL(raw string) (string, error) {
	hook, err := url.Parse(raw)
	if err != nil || (hook.Scheme != "http" && hook.Scheme != "https") || hook.Host == "" || hook.User != nil {
		return "", errors.New("webhook_url must be a Mattermost webhook URL (https://HOST/hooks/...)")
	}
	parts := strings.Split(strings.Trim(hook.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "hooks" || parts[1] == "" {
		return "", errors.New("webhook_url must be a Mattermost webhook URL (https://HOST/hooks/...)")
	}
	u := &url.URL{Scheme: "mattermost", Host: hook.Host, Path: "/" + parts[1]}
	if hook.Scheme == "http" {
		u.RawQuery = url.Values{"disabletls": {"yes"}}.Encode()
	}
	return u.String(), nil
}

// joinList normalises a comma-separated list: trimmed items, no empty ones.
func joinList(raw string) string {
	var out []string
	for item := range strings.SplitSeq(raw, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return strings.Join(out, ",")
}
