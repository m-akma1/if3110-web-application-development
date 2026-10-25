// Package ids generates identifiers for platform entities.
package ids

import (
	"crypto/rand"
	"fmt"
	"regexp"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// New returns a random (version 4) UUID in canonical lowercase form.
func New() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// crypto/rand only fails if the OS entropy source is broken; nothing sensible to continue with.
		panic(fmt.Sprintf("ids: crypto/rand failed: %v", err))
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// IsValid reports whether s is a canonical lowercase UUID.
// Use it before passing user input to a UUID column, so bad input becomes "not found" instead of a SQL error.
func IsValid(s string) bool {
	return uuidRe.MatchString(s)
}
