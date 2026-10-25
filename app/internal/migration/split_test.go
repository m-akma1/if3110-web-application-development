package migration

import (
	"reflect"
	"testing"
)

func texts(stmts []Statement) []string {
	out := make([]string, len(stmts))
	for i, st := range stmts {
		out[i] = st.Text
	}
	return out
}

func TestSplitStatements(t *testing.T) {
	cases := []struct {
		name   string
		script string
		want   []string
	}{
		{"single without semicolon", "SELECT 1", []string{"SELECT 1"}},
		{"trailing semicolon and spaces", "  SELECT 1 ;  \n", []string{"SELECT 1"}},
		{"two statements", "CREATE TABLE a(x int);\nINSERT INTO a VALUES (1);", []string{"CREATE TABLE a(x int)", "INSERT INTO a VALUES (1)"}},
		{"empty statements skipped", ";; SELECT 1;;; SELECT 2;", []string{"SELECT 1", "SELECT 2"}},
		{"only comments", "-- nothing\n/* still ; nothing */", nil},
		{"semicolon in string", "INSERT INTO t VALUES ('a;b'); SELECT 2", []string{"INSERT INTO t VALUES ('a;b')", "SELECT 2"}},
		{"doubled quote in string", "SELECT 'it''s;'; SELECT 2", []string{"SELECT 'it''s;'", "SELECT 2"}},
		{"E string with backslash quote", `SELECT E'a\';b'; SELECT 2`, []string{`SELECT E'a\';b'`, "SELECT 2"}},
		{"backslash in standard string is literal", `SELECT 'a\'; SELECT 2`, []string{`SELECT 'a\'`, "SELECT 2"}},
		{"semicolon in quoted identifier", `CREATE TABLE "a;b"(x int); SELECT 2`, []string{`CREATE TABLE "a;b"(x int)`, "SELECT 2"}},
		{"line comment hides semicolon", "SELECT 1 -- ; not a split\n+ 1; SELECT 2", []string{"SELECT 1 -- ; not a split\n+ 1", "SELECT 2"}},
		{"nested block comment", "SELECT /* a /* b; */ c; */ 1; SELECT 2", []string{"SELECT /* a /* b; */ c; */ 1", "SELECT 2"}},
		{"leading comment not part of text", "-- header\n/* x */ SELECT 1;", []string{"SELECT 1"}},
		{
			"dollar quoted body",
			"CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; END $$ LANGUAGE plpgsql; SELECT 2",
			[]string{"CREATE FUNCTION f() RETURNS void AS $$ BEGIN PERFORM 1; END $$ LANGUAGE plpgsql", "SELECT 2"},
		},
		{
			"tagged dollar quote containing $$",
			"DO $body$ BEGIN RAISE NOTICE '$$;'; END $body$; SELECT 2",
			[]string{"DO $body$ BEGIN RAISE NOTICE '$$;'; END $body$", "SELECT 2"},
		},
		{"positional parameter is not a dollar quote", "PREPARE p AS SELECT $1; SELECT 2", []string{"PREPARE p AS SELECT $1", "SELECT 2"}},
		{"dollar inside identifier", "SELECT a$b; SELECT 2", []string{"SELECT a$b", "SELECT 2"}},
		{
			"BEGIN ATOMIC body with CASE",
			"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END; SELECT 3;",
			[]string{"CREATE FUNCTION f() RETURNS int LANGUAGE sql BEGIN ATOMIC SELECT CASE WHEN true THEN 1 END; SELECT 2; END", "SELECT 3"},
		},
		{"unterminated string runs to end", "SELECT 'oops; SELECT 2", []string{"SELECT 'oops; SELECT 2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SplitStatements(tc.script)
			if !reflect.DeepEqual(texts(got), tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Fatalf("SplitStatements(%q)\n got  %q\n want %q", tc.script, texts(got), tc.want)
			}
			for _, st := range got {
				if tc.script[st.Start:st.End] != st.Text {
					t.Fatalf("offsets [%d:%d] give %q, want %q", st.Start, st.End, tc.script[st.Start:st.End], st.Text)
				}
			}
		})
	}
}

func TestSplitStatementsWords(t *testing.T) {
	got := SplitStatements(`create unique index /* x */ "Quoted" on t (col); copy t from stdin`)
	if len(got) != 2 {
		t.Fatalf("want 2 statements, got %d", len(got))
	}
	if want := []string{"CREATE", "UNIQUE", "INDEX", "ON", "T", "COL"}; !reflect.DeepEqual(got[0].Words, want) {
		t.Fatalf("words = %q, want %q", got[0].Words, want)
	}
	if want := []string{"COPY", "T", "FROM", "STDIN"}; !reflect.DeepEqual(got[1].Words, want) {
		t.Fatalf("words = %q, want %q", got[1].Words, want)
	}
}

func TestLineColumn(t *testing.T) {
	script := "SELECT 1;\nINSERT INTO é VALUES (1);"
	cases := []struct {
		pos, line, col int
		ok             bool
	}{
		{1, 1, 1, true},
		{10, 1, 10, true}, // the newline itself
		{11, 2, 1, true},
		{23, 2, 13, true}, // 'é' counts as one character
		{24, 2, 14, true},
		{runeCount(script) + 1, 2, 26, true}, // end of input
		{0, 0, 0, false},
		{runeCount(script) + 2, 0, 0, false},
	}
	for _, tc := range cases {
		line, col, _, ok := lineColumn(script, tc.pos)
		if line != tc.line || col != tc.col || ok != tc.ok {
			t.Errorf("lineColumn(pos=%d) = %d:%d ok=%v, want %d:%d ok=%v", tc.pos, line, col, ok, tc.line, tc.col, tc.ok)
		}
	}
}

func TestStatementAt(t *testing.T) {
	script := "SELECT 1;  SELECT 2;\nSELECT 3"
	stmts := SplitStatements(script)
	cases := map[int]int{0: 1, 7: 1, 9: 2, 11: 2, 21: 3, len(script): 3}
	for off, want := range cases {
		if got := statementAt(stmts, off); got != want {
			t.Errorf("statementAt(%d) = %d, want %d", off, got, want)
		}
	}
	if got := statementAt(nil, 0); got != 0 {
		t.Errorf("statementAt(nil) = %d, want 0", got)
	}
}

func runeCount(s string) int { return len([]rune(s)) }
