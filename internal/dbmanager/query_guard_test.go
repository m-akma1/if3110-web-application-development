package dbmanager_test

import (
	"testing"

	"dbplatform/internal/dbmanager"
)

func TestValidateReadOnlySQL(t *testing.T) {
	validQueries := []string{
		"SELECT * FROM users",
		"SELECT id, username, role FROM users WHERE is_active = true;",
		"WITH active_users AS (SELECT * FROM users) SELECT * FROM active_users",
		"SELECT 'DELETE FROM users' AS contoh_teks FROM users",
	}

	for _, q := range validQueries {
		cleaned, kw, err := dbmanager.ValidateReadOnlySQL(q)
		if err != nil || cleaned == "" {
			t.Fatalf("kueri valid %q seharusnya lolos, tetapi gagal: err=%v, kw=%s", q, err, kw)
		}
	}

	invalidQueries := []struct {
		query      string
		expectedKW string
	}{
		{"DELETE FROM users WHERE id = '1'", "DELETE"},
		{"UPDATE users SET role = 'PLATFORM_ADMIN'", "UPDATE"},
		{"INSERT INTO users VALUES ('1', 'hacker')", "INSERT"},
		{"DROP TABLE users", "DROP"},
		{"ALTER TABLE users ADD COLUMNadmin BOOLEAN", "ALTER"},
		{"TRUNCATE TABLE sessions", "TRUNCATE"},
		{"/* komentar */ DELETE FROM users", "DELETE"},
		{"WITH x AS (DELETE FROM users RETURNING *) SELECT * FROM x", "DELETE"},
		{"SELECT * FROM users; DROP TABLE users;", ";"},
	}

	for _, tc := range invalidQueries {
		_, kw, err := dbmanager.ValidateReadOnlySQL(tc.query)
		if err == nil {
			t.Fatalf("kueri berbahaya %q seharusnya ditolak!", tc.query)
		}
		if kw != tc.expectedKW {
			t.Fatalf("kueri %q mengharapkan keyword terlarang %q, dapat %q", tc.query, tc.expectedKW, kw)
		}
	}
}
