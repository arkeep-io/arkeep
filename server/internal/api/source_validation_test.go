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
}
