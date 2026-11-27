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
	if i < len(toks) && toks[i].word == "ONLY" {
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
	for j := i; j < len(toks); j++ {
		switch toks[j].word {
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

	pred := fmt.Sprintf("($1::bigint IS NULL OR %[1]s.%[2]s > $1) AND %[1]s.%[2]s <= $2::bigint", ref, attribute)
	stmt := text + "\nWHERE " + pred
	if where >= 0 {
		cut := toks[where].end
		stmt = text[:cut] + " (" + strings.TrimSpace(text[cut:]) + ")\n  AND " + pred
	}
	return &BatchPlan{
		Table:     table,
		Attribute: attribute,
		Statement: stmt,
		Bounds: fmt.Sprintf(`SELECT max(%[1]s), count(*) FROM (
	SELECT %[1]s FROM %[2]s WHERE ($1::bigint IS NULL OR %[1]s > $1) ORDER BY %[1]s LIMIT $2) b
HAVING count(*) > 0`, attribute, table),
		Remaining: fmt.Sprintf(`SELECT count(*) FROM %[2]s WHERE ($1::bigint IS NULL OR %[1]s > $1)`, attribute, table),
	}, nil
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
