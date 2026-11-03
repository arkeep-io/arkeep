package policyutil

import (
	"testing"
)

func TestSourcePaths(t *testing.T) {
	t.Run("directory entries pass through as plain paths", func(t *testing.T) {
		got, err := SourcePaths(`[{"type":"directory","path":"/data"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"/data"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("SourcePaths() = %v, want %v", got, want)
		}
	})

	t.Run("docker-volume entries are prefixed", func(t *testing.T) {
		got, err := SourcePaths(`[{"type":"docker-volume","path":"my-volume"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := "docker-volume://my-volume"
		if len(got) != 1 || got[0] != want {
			t.Errorf("SourcePaths() = %v, want [%q]", got, want)
		}
	})

	t.Run("excludes command entries", func(t *testing.T) {
		got, err := SourcePaths(`[{"type":"directory","path":"/data"},{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []string{"/data"}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("SourcePaths() = %v, want %v (command entry should be excluded)", got, want)
		}
	})

	t.Run("skips malformed docker-volume entry with no name", func(t *testing.T) {
		got, err := SourcePaths(`[{"type":"docker-volume","path":""}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("SourcePaths() = %v, want empty", got)
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		if _, err := SourcePaths(`not json`); err == nil {
			t.Error("expected error for malformed JSON")
		}
	})
}

func TestCommandSources(t *testing.T) {
	t.Run("extracts name and command", func(t *testing.T) {
		got, err := CommandSources(`[{"type":"command","path":"pg_dump mydb","label":"pgdump"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		want := []CommandSource{{Name: "pgdump", Command: "pg_dump mydb"}}
		if len(got) != 1 || got[0] != want[0] {
			t.Errorf("CommandSources() = %v, want %v", got, want)
		}
	})

	t.Run("excludes directory and docker-volume entries", func(t *testing.T) {
		got, err := CommandSources(`[{"type":"directory","path":"/data"},{"type":"docker-volume","path":"vol"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("CommandSources() = %v, want empty", got)
		}
	})

	t.Run("skips entries with empty name or command", func(t *testing.T) {
		got, err := CommandSources(`[{"type":"command","path":"","label":"pgdump"},{"type":"command","path":"pg_dump mydb","label":""}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 0 {
			t.Errorf("CommandSources() = %v, want empty", got)
		}
	})

	t.Run("preserves declaration order", func(t *testing.T) {
		got, err := CommandSources(`[{"type":"command","path":"a","label":"first"},{"type":"directory","path":"/data"},{"type":"command","path":"b","label":"second"}]`)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 2 || got[0].Name != "first" || got[1].Name != "second" {
			t.Errorf("CommandSources() = %v, want [first, second] in order", got)
		}
	})

	t.Run("rejects malformed JSON", func(t *testing.T) {
		if _, err := CommandSources(`not json`); err == nil {
			t.Error("expected error for malformed JSON")
		}
	})
}

func TestWithoutCommands(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"command removed, name kept",
			`[{"type":"command","path":"PGPASSWORD=x pg_dump db","label":"db"}]`,
			`[{"type":"command","path":"","label":"db"}]`},
		{"key case does not hide a command source",
			`[{"TYPE":"command","Path":"PGPASSWORD=x pg_dump db","label":"db"}]`,
			`[{"type":"command","path":"","label":"db"}]`},
		{"other sources kept as they are",
			`[{"type":"directory","path":"/data"},"/etc",{"type":"docker-volume","path":"pg"}]`,
			`[{"type":"directory","path":"/data"},"/etc",{"type":"docker-volume","path":"pg"}]`},
		{"not a JSON array", `not json`, `[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WithoutCommands(tt.in); got != tt.want {
				t.Errorf("WithoutCommands(%s) = %s, want %s", tt.in, got, tt.want)
			}
		})
	}
}
