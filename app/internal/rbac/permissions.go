// Package rbac holds every authorization rule of the platform in one place:
// which access levels exist, which actions each level allows, and how a user's
// effective level on a database is derived from their groups' grants.
// Handlers call Authorizer.Authorize; they never compare levels themselves.
package rbac

// AccessLevel mendefinisikan tingkat akses group terhadap suatu database
type AccessLevel string

const (
	AccessNone     AccessLevel = "NONE"
	AccessViewOnly AccessLevel = "VIEW_ONLY" // Hanya melihat schema, data, dan riwayat migration
	AccessViewEdit AccessLevel = "VIEW_EDIT" // Seluruh akses View Only + membuat, mengubah, & menjalankan migration
)

// Action mendefinisikan jenis aksi yang diperiksa pada sisi backend
type Action string

const (
	ActionViewSchema       Action = "database:view_schema"
	ActionViewData         Action = "database:view_data"
	ActionViewMigration    Action = "migration:view_history"
	ActionCreateMigration  Action = "migration:create"
	ActionExecuteMigration Action = "migration:execute"
)

// IsAllowed memeriksa apakah tingkat akses group mengizinkan aksi tertentu
func IsAllowed(level AccessLevel, action Action) bool {
	switch level {
	case AccessViewEdit:
		switch action {
		case ActionViewSchema, ActionViewData, ActionViewMigration, ActionCreateMigration, ActionExecuteMigration:
			return true
		}
		return false
	case AccessViewOnly:
		return action == ActionViewSchema || action == ActionViewData || action == ActionViewMigration
	default:
		return false
	}
}

// rank orders levels from least to most permissive. Unknown values rank as NONE.
func rank(l AccessLevel) int {
	switch l {
	case AccessViewEdit:
		return 2
	case AccessViewOnly:
		return 1
	default:
		return 0
	}
}

// Raises reports whether moving from old to new grants more access (used to rotate sessions).
func Raises(old, new AccessLevel) bool {
	return rank(new) > rank(old)
}

// Max returns the more permissive of two levels.
func Max(a, b AccessLevel) AccessLevel {
	if rank(b) > rank(a) {
		return b
	}
	return Normalize(a)
}

// Effective is the documented rule for users in several groups: the most permissive grant wins
// (VIEW_EDIT > VIEW_ONLY > NONE). There are no deny rules. No grants means NONE.
func Effective(levels ...AccessLevel) AccessLevel {
	eff := AccessNone
	for _, l := range levels {
		eff = Max(eff, l)
	}
	return eff
}

// Normalize maps unknown strings to NONE.
func Normalize(l AccessLevel) AccessLevel {
	switch l {
	case AccessViewEdit, AccessViewOnly:
		return l
	default:
		return AccessNone
	}
}

// ParseLevel converts form input to a level; anything unrecognized becomes NONE.
func ParseLevel(s string) AccessLevel {
	return Normalize(AccessLevel(s))
}
