package migration

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Execution modes of a migration.
const (
	ModeStandard = "STANDARD" // the whole script in one transaction
	ModeBatch    = "BATCH"    // one UPDATE/DELETE, one transaction per batch of rows
)

const (
	DefaultBatchSize = 1000
	MaxBatchSize     = 100000
)

var (
	ErrBatchConfig = fmt.Errorf("mode batch membutuhkan kolom cursor (huruf kecil, angka, _) dan ukuran batch 1–%d", MaxBatchSize)
	// ErrBatchScript explains which scripts can run per batch.
	ErrBatchScript = errors.New("mode batch hanya mendukung satu statement UPDATE atau DELETE pada satu tabel, tanpa WITH, RETURNING, ORDER BY, LIMIT, atau WHERE CURRENT OF")
)

var batchAttributeRe = regexp.MustCompile(`^[a-z_][a-z0-9_]*$`)

// BatchPlan is a script prepared for per-batch execution.
type BatchPlan struct {
	Verb      string // UPDATE or DELETE
	Table     string // the target table as written, e.g. orders or public."Orders"
	Attribute string // the cursor column (an integer key)
	// Statement is the user's statement restricted to one key range: $1 is the exclusive lower
	// bound (NULL for the first batch) and $2 the inclusive upper bound.
	Statement string
	// Bounds returns the upper bound and the row count of the next batch: $1 is the previous
	// upper bound (NULL at the start) and $2 the batch size. No row means no batch is left.
	Bounds string
	// Remaining counts the keys after $1, to estimate the number of batches left.
	Remaining string

	// Recovery data (see recoveryTable), UPDATE only. Every query takes $1/$2 as above, $3 the
	// migration ID and $4 the batch number.
	//
	// Capture stores the before-image of the rows the batch is about to change and locks them
	// (FOR UPDATE), in the batch's transaction, right before Returning, and returns their keys.
	// It filters on the statement's WHERE; with UPDATE ... FROM (whose WHERE joins other tables)
	// it takes every row of the key range instead, a superset.
	Capture string
	// Returning is Statement returning the key of every row it changed ($1/$2 only). The engine
	// refuses the batch when one of them is not among the keys Capture returned (a row that
	// started to match in between). The comparison is done in Go, so it never depends on the
	// planner's view of the recovery table, which grows by a whole batch at a time.
	Returning string
	// Verify compares, after SQL Down ran over the key range, the stored before-images with the
	// rows now in the range: it returns the number of stored rows and how many of them are not
	// found unchanged (EXCEPT ALL, so a non-unique cursor column works too).
	Verify string
}

// PrepareBatch checks that script can run per batch over attribute and builds the SQL for it.
// The range predicate is added by the platform, so the user writes the statement as for a
// normal run, for example: UPDATE orders SET status = 'ARCHIVED' WHERE status = 'CANCELLED'.
func PrepareBatch(script, attribute string) (*BatchPlan, error) {
	if !batchAttributeRe.MatchString(attribute) || len(attribute) > 63 {
		return nil, ErrBatchConfig
	}
	stmts := SplitStatements(script)
	if len(stmts) != 1 {
		return nil, fmt.Errorf("%w (script berisi %d statement)", ErrBatchScript, len(stmts))
	}
	text := stmts[0].Text
	toks := topLevelTokens(text)

	// UPDATE [ONLY] table [*] [[AS] alias] SET ...   |   DELETE FROM [ONLY] table [*] [[AS] alias] ...
	i := 0
	next := func() (tok, bool) {
		if i < len(toks) {
			i++
			return toks[i-1], true
		}
		return tok{}, false
	}
	first, _ := next()
	switch first.word {
	case "UPDATE":
	case "DELETE":
		if t, ok := next(); !ok || t.word != "FROM" {
			return nil, ErrBatchScript
		}
	default:
		return nil, ErrBatchScript
	}
	only := ""
	if i < len(toks) && toks[i].word == "ONLY" {
		only = "ONLY "
		i++
	}
	tableStart := i
	if i >= len(toks) || !toks[i].ident {
		return nil, ErrBatchScript
	}
	i++
	for i+1 < len(toks) && toks[i].text == "." && toks[i+1].ident {
		i += 2
	}
	table := text[toks[tableStart].start:toks[i-1].end]
	if i < len(toks) && toks[i].text == "*" {
		i++
	}
	ref := table
	if i < len(toks) && toks[i].word == "AS" {
		i++
	}
	if i < len(toks) && toks[i].ident && !clauseWords[toks[i].word] {
		ref = toks[i].text
		i++
	}

	where := -1
	joins := false // UPDATE ... FROM / DELETE ... USING: the WHERE may refer to other tables
	for j := i; j < len(toks); j++ {
		switch toks[j].word {
		case "FROM", "USING":
			if where < 0 {
				joins = true
			}
		case "WHERE":
			if where >= 0 {
				return nil, ErrBatchScript
			}
			where = j
			if j+1 < len(toks) && toks[j+1].word == "CURRENT" {
				return nil, ErrBatchScript
			}
		case "RETURNING", "ORDER", "LIMIT", "WITH":
			return nil, ErrBatchScript
		}
	}

	pred := rangePredicate(ref, attribute)
	stmt := text + "\nWHERE " + pred
	userWhere := ""
	if where >= 0 {
		cut := toks[where].end
		userWhere = strings.TrimSpace(text[cut:])
		stmt = text[:cut] + " (" + userWhere + ")\n  AND " + pred
	}
	plan := &BatchPlan{
		Verb:      first.word,
		Table:     table,
		Attribute: attribute,
		Statement: stmt,
		Bounds: fmt.Sprintf(`SELECT max(%[1]s), count(*) FROM (
	SELECT %[1]s FROM %[2]s WHERE ($1::bigint IS NULL OR %[1]s > $1) ORDER BY %[1]s LIMIT $2) b
HAVING count(*) > 0`, attribute, table),
		Remaining: fmt.Sprintf(`SELECT count(*) FROM %[2]s WHERE ($1::bigint IS NULL OR %[1]s > $1)`, attribute, table),
	}
	if plan.Verb == "UPDATE" {
		plan.addRecovery(only, ref, userWhere, joins)
	}
	return plan, nil
}

// recoveryRow is the alias the recovery queries give the target table.
const recoveryRow = "_dbplatform_row"

// rangePredicate restricts ref to one batch: keys after $1 (all keys when NULL) up to $2.
func rangePredicate(ref, attribute string) string {
	return fmt.Sprintf("($1::bigint IS NULL OR %[1]s.%[2]s > $1) AND %[1]s.%[2]s <= $2::bigint", ref, attribute)
}

// addRecovery builds Capture, Changed and Verify (see BatchPlan).
func (p *BatchPlan) addRecovery(only, ref, userWhere string, joins bool) {
	rng := rangePredicate(recoveryRow, p.Attribute)
	filter := rng
	if userWhere != "" && !joins {
		// The user's WHERE uses the user's names for the table, so it is applied in a subquery
		// that reads the table the way the statement does.
		from := only + p.Table
		if ref != p.Table {
			from += " " + ref
		}
		filter = fmt.Sprintf("%s\n  AND %s.%s IN (SELECT %s.%s FROM %s WHERE (%s) AND %s)",
			rng, recoveryRow, p.Attribute, ref, p.Attribute, from, userWhere, rangePredicate(ref, p.Attribute))
	}
	p.Capture = fmt.Sprintf(`INSERT INTO %[1]s (migration_id, batch_no, row_key, before)
SELECT $3::uuid, $4::int, %[2]s.%[3]s, to_jsonb(%[2]s) FROM %[4]s%[5]s %[2]s
WHERE %[6]s
FOR UPDATE OF %[2]s
RETURNING row_key`, recoveryTable, recoveryRow, p.Attribute, only, p.Table, filter)
	p.Returning = fmt.Sprintf("%s\nRETURNING %s.%s", p.Statement, ref, p.Attribute)
	p.Verify = fmt.Sprintf(`SELECT (SELECT count(*) FROM %[1]s WHERE migration_id = $3::uuid AND batch_no = $4::int),
	(SELECT count(*) FROM (
		SELECT before FROM %[1]s WHERE migration_id = $3::uuid AND batch_no = $4::int
		EXCEPT ALL
		SELECT to_jsonb(%[2]s) FROM %[3]s%[4]s %[2]s WHERE %[5]s) d)`,
		recoveryTable, recoveryRow, only, p.Table, rng)
}

// clauseWords end the table reference; any other identifier after the table is its alias.
var clauseWords = map[string]bool{"SET": true, "USING": true, "WHERE": true, "RETURNING": true, "FROM": true}

// tok is one top-level token of a statement: a word, a quoted identifier or one other character.
// Tokens inside parentheses, strings and comments are left out, so a WHERE in a subquery or in
// a string literal is never mistaken for the statement's WHERE.
type tok struct {
	text       string
	word       string // upper-cased keyword or identifier; empty otherwise
	ident      bool   // usable as a table name or alias
	start, end int
}

func topLevelTokens(s string) []tok {
	var out []tok
	depth := 0
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLineComment(s, i)
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlockComment(s, i)
		case c == '\'':
			i = skipQuoted(s, i, '\'', false)
		case c == '$':
			if end, ok := skipDollarQuoted(s, i); ok {
				i = end
			} else {
				i++
			}
		case c == '"':
			end := skipQuoted(s, i, '"', false)
			if depth == 0 {
				out = append(out, tok{text: s[i:end], ident: true, start: i, end: end})
			}
			i = end
		case c == '(':
			depth++
			i++
		case c == ')':
			depth--
			i++
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			if (s[i:j] == "E" || s[i:j] == "e") && j < len(s) && s[j] == '\'' {
				i = skipQuoted(s, j, '\'', true)
				continue
			}
			if depth == 0 {
				out = append(out, tok{text: s[i:j], word: strings.ToUpper(s[i:j]), ident: true, start: i, end: j})
			}
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		default:
			if depth == 0 {
				out = append(out, tok{text: s[i : i+1], start: i, end: i + 1})
			}
			i++
		}
	}
	return out
}
