package api

import (
	"strings"
	"testing"
)

func TestValidateSourcesJSON(t *testing.T) {
	t.Run("accepts object form", func(t *testing.T) {
		valid := []string{
			`[{"type":"directory","path":"/data"}]`,
			`[{"type":"directory","path":"/data"},{"type":"directory","path":"/var/log"}]`,
			`[{"type":"directory","path":"C:\\Users\\Filippo\\data"}]`,
			`[{"type":"directory","path":"\\\\server\\share\\backups"}]`,
			`[{"type":"docker-volume","path":"my-volume"}]`,
		}
		for _, sources := range valid {
			if err := validateSourcesJSON(sources); err != nil {
				t.Errorf("valid sources %q rejected: %v", sources, err)
			}
		}
	})

	t.Run("rejects flat-string form (unsupported shape, unchanged from prior behavior)", func(t *testing.T) {
		// sources has always been an array of {type, path} objects (the shape
		// the GUI sends); a bare string array was never a supported shape,
		// before or after this fix.
		if err := validateSourcesJSON(`["/data", "/var/log"]`); err == nil {
			t.Error("expected error for flat-string sources shape")
		}
	})

	t.Run("rejects flag-like path in object form", func(t *testing.T) {
		cases := []string{
			`[{"type":"directory","path":"--password-command=touch /tmp/pwned"}]`,
			`[{"type":"directory","path":"-r"}]`,
			`[{"type":"directory","path":"--repo=s3:evil.example.com/bucket"}]`,
		}
		for _, sources := range cases {
			if err := validateSourcesJSON(sources); err == nil {
				t.Errorf("flag-like source not rejected: %q", sources)
			}
		}
	})

	t.Run("rejects empty array", func(t *testing.T) {
		if err := validateSourcesJSON(`[]`); err == nil {
			t.Error("expected error for empty sources array")
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		if err := validateSourcesJSON(`not json`); err == nil {
			t.Error("expected error for malformed sources JSON")
		}
	})

	t.Run("error message identifies the offending path", func(t *testing.T) {
		err := validateSourcesJSON(`[{"type":"directory","path":"--password-command=x"}]`)
		if err == nil {
			t.Fatal("expected error")
		}
		if !strings.Contains(err.Error(), "--password-command=x") {
			t.Errorf("error = %q, want it to name the offending path", err.Error())
		}
	})

	t.Run("accepts a command-only policy", func(t *testing.T) {
		if err := validateSourcesJSON(`[{"type":"command","path":"pg_dump -U postgres mydb","label":"pgdump"}]`); err != nil {
			t.Errorf("command-only sources rejected: %v", err)
		}
	})

	t.Run("accepts a mix of directory and command sources", func(t *testing.T) {
		sources := `[{"type":"directory","path":"/data"},{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`
		if err := validateSourcesJSON(sources); err != nil {
			t.Errorf("mixed sources rejected: %v", err)
		}
	})

	t.Run("rejects invalid command source names", func(t *testing.T) {
		cases := []string{
			`[{"type":"command","path":"pg_dump mydb","label":""}]`,
			`[{"type":"command","path":"pg_dump mydb","label":"-leading-dash"}]`,
			`[{"type":"command","path":"pg_dump mydb","label":"has space"}]`,
			`[{"type":"command","path":"pg_dump mydb","label":"has:colon"}]`,
			`[{"type":"command","path":"pg_dump mydb","label":"has/slash"}]`,
		}
		for _, sources := range cases {
			if err := validateSourcesJSON(sources); err == nil {
				t.Errorf("invalid command source name not rejected: %q", sources)
			}
		}
	})

	t.Run("rejects duplicate command source names", func(t *testing.T) {
		sources := `[{"type":"command","path":"pg_dump db1","label":"dump"},{"type":"command","path":"pg_dump db2","label":"dump"}]`
		if err := validateSourcesJSON(sources); err == nil {
			t.Error("duplicate command source name not rejected")
		}
	})

	t.Run("rejects an empty command", func(t *testing.T) {
		if err := validateSourcesJSON(`[{"type":"command","path":"","label":"pgdump"}]`); err == nil {
			t.Error("empty command not rejected")
		}
	})

	t.Run("rejects a command failing hook-style validation", func(t *testing.T) {
		sources := `[{"type":"command","path":"echo $(cat /etc/passwd)","label":"pgdump"}]`
		if err := validateSourcesJSON(sources); err == nil {
			t.Error("command substitution in command source not rejected")
		}
	})

	t.Run("does not reject a command containing a leading-dash-style flag mid-string", func(t *testing.T) {
		// The leading-dash rejection is a restic-argv-injection defense that
		// only applies to path entries — a command legitimately contains
		// flags anywhere in the string (it is validated like a hook, not a
		// path).
		if err := validateSourcesJSON(`[{"type":"command","path":"pg_dump -U postgres mydb","label":"pgdump"}]`); err != nil {
			t.Errorf("command with flags rejected: %v", err)
		}
	})
}

func TestPolicyHasCommandSource(t *testing.T) {
	cases := []struct {
		name    string
		sources string
		want    bool
	}{
		{"no sources", `[]`, false},
		{"directory only", `[{"type":"directory","path":"/data"}]`, false},
		{"has a command source", `[{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`, true},
		{"malformed JSON", `not json`, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := policyHasCommandSource(tt.sources); got != tt.want {
				t.Errorf("policyHasCommandSource(%q) = %v, want %v", tt.sources, got, tt.want)
			}
		})
	}
}

func TestCommandSourcesChanged(t *testing.T) {
	dirOnly := `[{"type":"directory","path":"/data"}]`
	withCmd := `[{"type":"directory","path":"/data"},{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`
	withCmdReordered := `[{"type":"command","path":"pg_dump mydb","label":"pgdump"},{"type":"directory","path":"/data"}]`
	withDifferentCmd := `[{"type":"directory","path":"/data"},{"type":"command","path":"pg_dump other","label":"pgdump"}]`

	cases := []struct {
		name string
		old  string
		new  string
		want bool
	}{
		{"no command sources on either side", dirOnly, `[{"type":"directory","path":"/var/log"}]`, false},
		{"unchanged command source, directory-only fields differ elsewhere", withCmd, withCmd, false},
		{"unchanged command source, different ordering", withCmd, withCmdReordered, false},
		{"command source added", dirOnly, withCmd, true},
		{"command source removed", withCmd, dirOnly, true},
		{"command source's command text changed", withCmd, withDifferentCmd, true},
		{"unparsable old value fails closed", `not json`, dirOnly, true},
		{"unparsable new value fails closed", dirOnly, `not json`, true},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := commandSourcesChanged(tt.old, tt.new); got != tt.want {
				t.Errorf("commandSourcesChanged(%q, %q) = %v, want %v", tt.old, tt.new, got, tt.want)
			}
		})
	}
}
