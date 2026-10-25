package ids_test

import (
	"testing"

	"dbplatform/internal/ids"
)

func TestNew_IsValidAndUnique(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 1000; i++ {
		id := ids.New()
		if !ids.IsValid(id) {
			t.Fatalf("UUID tidak valid: %q", id)
		}
		if id[14] != '4' {
			t.Fatalf("UUID seharusnya versi 4, dapat: %q", id)
		}
		if seen[id] {
			t.Fatalf("UUID duplikat: %q", id)
		}
		seen[id] = true
	}
}

func TestIsValid_RejectsBadInput(t *testing.T) {
	for _, s := range []string{"", "usr-admin-001", "not-a-uuid", "00000000-0000-0000-0000-00000000000", "00000000-0000-0000-0000-00000000000G"} {
		if ids.IsValid(s) {
			t.Fatalf("%q seharusnya bukan UUID valid", s)
		}
	}
}
