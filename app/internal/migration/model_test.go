package migration

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeDraft(t *testing.T) {
	name, script, err := NormalizeDraft("  add orders  ", "CREATE TABLE orders (id int);")
	if err != nil || name != "add orders" || script != "CREATE TABLE orders (id int);" {
		t.Fatalf("NormalizeDraft = %q, %q, %v", name, script, err)
	}
	cases := []struct {
		name, script string
		want         error
	}{
		{"", "SELECT 1", ErrInvalidName},
		{"   ", "SELECT 1", ErrInvalidName},
		{strings.Repeat("é", MaxNameLength+1), "SELECT 1", ErrInvalidName},
		{"ok", "  \n ", ErrEmptyScript},
		{"ok", strings.Repeat("x", MaxScriptLength+1), ErrScriptTooLarge},
	}
	for _, tc := range cases {
		if _, _, err := NormalizeDraft(tc.name, tc.script); !errors.Is(err, tc.want) {
			t.Errorf("NormalizeDraft(%q, %d bytes) = %v, want %v", tc.name, len(tc.script), err, tc.want)
		}
	}
	if _, _, err := NormalizeDraft(strings.Repeat("é", MaxNameLength), "SELECT 1"); err != nil {
		t.Errorf("a %d-character name should be accepted: %v", MaxNameLength, err)
	}
}

func TestScriptErrorString(t *testing.T) {
	e := &ScriptError{Message: `relation "missing" does not exist`, Code: "42P01", Line: 2, Column: 13, Statement: 2, Hint: "check the name"}
	got := e.String()
	for _, want := range []string{"ERROR 42P01", `relation "missing" does not exist`, "statement 2, baris 2, kolom 13", "HINT: check the name"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, missing %q", got, want)
		}
	}
	if got := (&ScriptError{Message: "timeout"}).String(); got != "timeout" {
		t.Errorf("String() without code/position = %q", got)
	}
	var nilErr *ScriptError
	if nilErr.String() != "" {
		t.Error("nil ScriptError should format as empty")
	}
}
