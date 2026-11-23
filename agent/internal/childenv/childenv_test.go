package childenv

import (
	"slices"
	"testing"
)

func TestFilter(t *testing.T) {
	environ := []string{
		"PATH=/usr/bin:/bin",
		"ARKEEP_AGENT_SECRET=s3cr3t",
		"ARKEEP_SERVER_ADDR=arkeep.example.com:9090",
		"arkeep_agent_secret=lowercase",
		"HOME=/root",
		"NOT_ARKEEP_X=kept",
		"ARKEEP=kept, no underscore",
		"=C:=C:\\dir",
		"EMPTY=",
	}
	want := []string{
		"PATH=/usr/bin:/bin",
		"HOME=/root",
		"NOT_ARKEEP_X=kept",
		"ARKEEP=kept, no underscore",
		"=C:=C:\\dir",
		"EMPTY=",
	}
	got := Filter(environ)
	if !slices.Equal(got, want) {
		t.Errorf("Filter() = %q, want %q", got, want)
	}
	if len(environ) != 9 || environ[1] != "ARKEEP_AGENT_SECRET=s3cr3t" {
		t.Errorf("Filter modified its input: %q", environ)
	}
}

func TestName(t *testing.T) {
	tests := map[string]string{
		"PATH=/bin":    "PATH",
		"A=b=c":        "A",
		"EMPTY=":       "EMPTY",
		"NOVALUE":      "NOVALUE",
		"=C:=C:\\dir":  "=C:",
		"":             "",
	}
	for kv, want := range tests {
		if got := name(kv); got != want {
			t.Errorf("name(%q) = %q, want %q", kv, got, want)
		}
	}
}
