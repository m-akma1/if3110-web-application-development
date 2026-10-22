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
		return true
	case AccessViewOnly:
		return action == ActionViewSchema || action == ActionViewData || action == ActionViewMigration
	default:
		return false
	}
}
