package migration

import "strings"

// Statement is one top-level SQL statement of a script.
type Statement struct {
	Text  string   // the statement as written, without surrounding whitespace, comments or the trailing ';'
	Start int      // byte offset of Text in the script
	End   int      // byte offset just past Text
	Words []string // the first maxWords keywords/identifiers outside comments and quotes, upper-cased
}

// maxWords is enough for every rule in script_check.go.
const maxWords = 16

// SplitStatements splits a PostgreSQL script on top-level semicolons. It understands line and
// (nested) block comments, '...' and E'...' strings, "..." identifiers, $tag$...$tag$ bodies,
// and SQL-standard BEGIN ATOMIC ... END function bodies, so semicolons inside them do not split.
// Unterminated quotes or comments run to the end of the script; the server reports the error.
func SplitStatements(script string) []Statement {
	sp := splitter{s: script, start: -1}
	sp.run()
	return sp.out
}

type splitter struct {
	s   string
	out []Statement

	start    int // byte offset where the current statement begins, -1 if nothing significant yet
	end      int // byte offset just past the last significant token
	words    []string
	nWords   int    // words seen in the current statement (including those beyond maxWords)
	prev     string // previous word, to spot BEGIN ATOMIC
	atomic   int    // nesting depth inside BEGIN ATOMIC ... END
	isCreate bool
}

func (sp *splitter) run() {
	s := sp.s
	i := 0
	for i < len(s) {
		c := s[i]
		switch {
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			i = skipLineComment(s, i)
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			i = skipBlockComment(s, i)
		case c == '\'':
			i = sp.mark(i, skipQuoted(s, i, '\'', false))
		case c == '"':
			i = sp.mark(i, skipQuoted(s, i, '"', false))
		case c == '$':
			if end, ok := skipDollarQuoted(s, i); ok {
				i = sp.mark(i, end)
			} else {
				i = sp.mark(i, i+1)
			}
		case c == ';':
			if sp.atomic > 0 {
				i = sp.mark(i, i+1)
			} else {
				sp.flush()
				i++
			}
		case isIdentStart(c):
			j := i + 1
			for j < len(s) && isIdentPart(s[j]) {
				j++
			}
			word := s[i:j]
			// E'...' is a string with backslash escapes.
			if (word == "E" || word == "e") && j < len(s) && s[j] == '\'' {
				i = sp.mark(i, skipQuoted(s, j, '\'', true))
				continue
			}
			sp.mark(i, j)
			sp.addWord(strings.ToUpper(word))
			i = j
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f' || c == '\v':
			i++
		default:
			i = sp.mark(i, i+1)
		}
	}
	sp.flush()
}

// mark records [from, to) as part of the current statement and returns to.
func (sp *splitter) mark(from, to int) int {
	if sp.start < 0 {
		sp.start = from
	}
	sp.end = to
	return to
}

func (sp *splitter) addWord(w string) {
	if sp.nWords == 0 {
		sp.isCreate = w == "CREATE"
	}
	sp.nWords++
	if len(sp.words) < maxWords {
		sp.words = append(sp.words, w)
	}
	switch {
	case w == "ATOMIC" && sp.prev == "BEGIN" && sp.isCreate:
		sp.atomic++
	case sp.atomic > 0 && w == "CASE":
		sp.atomic++
	case sp.atomic > 0 && w == "END":
		sp.atomic--
	}
	sp.prev = w
}

func (sp *splitter) flush() {
	if sp.start >= 0 {
		sp.out = append(sp.out, Statement{
			Text:  sp.s[sp.start:sp.end],
			Start: sp.start,
			End:   sp.end,
			Words: sp.words,
		})
	}
	*sp = splitter{s: sp.s, out: sp.out, start: -1}
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '$'
}

func skipLineComment(s string, i int) int {
	if j := strings.IndexByte(s[i:], '\n'); j >= 0 {
		return i + j + 1
	}
	return len(s)
}

// skipBlockComment skips /* ... */, which PostgreSQL allows to nest.
func skipBlockComment(s string, i int) int {
	depth := 0
	for i < len(s) {
		switch {
		case s[i] == '/' && i+1 < len(s) && s[i+1] == '*':
			depth++
			i += 2
		case s[i] == '*' && i+1 < len(s) && s[i+1] == '/':
			depth--
			i += 2
			if depth == 0 {
				return i
			}
		default:
			i++
		}
	}
	return len(s)
}

// skipQuoted skips a quoted string or identifier starting at s[i] == quote.
// A doubled quote is an escaped quote; with backslashEscapes, \x escapes the next byte too.
func skipQuoted(s string, i int, quote byte, backslashEscapes bool) int {
	i++
	for i < len(s) {
		switch {
		case backslashEscapes && s[i] == '\\':
			i += 2
		case s[i] == quote:
			if i+1 < len(s) && s[i+1] == quote {
				i += 2
				continue
			}
			return i + 1
		default:
			i++
		}
	}
	return len(s)
}

// skipDollarQuoted skips $tag$ ... $tag$ starting at s[i] == '$'.
// It returns false when s[i] does not open a dollar quote (for example the parameter $1).
func skipDollarQuoted(s string, i int) (int, bool) {
	j := i + 1
	if j < len(s) && s[j] != '$' {
		if !isIdentStart(s[j]) {
			return 0, false
		}
		for j < len(s) && s[j] != '$' {
			if !isIdentPart(s[j]) {
				return 0, false
			}
			j++
		}
	}
	if j >= len(s) {
		return 0, false
	}
	tag := s[i : j+1]
	if k := strings.Index(s[j+1:], tag); k >= 0 {
		return j + 1 + k + len(tag), true
	}
	return len(s), true
}

// lineColumn converts a 1-based character position (as PostgreSQL reports it) into a 1-based
// line and column and the byte offset in script. ok is false when pos is outside the script.
func lineColumn(script string, pos int) (line, col, offset int, ok bool) {
	if pos < 1 {
		return 0, 0, 0, false
	}
	line, col = 1, 1
	n := 1
	for off, r := range script {
		if n == pos {
			return line, col, off, true
		}
		n++
		if r == '\n' {
			line++
			col = 1
		} else {
			col++
		}
	}
	if n == pos { // just past the last character (e.g. "syntax error at end of input")
		return line, col, len(script), true
	}
	return 0, 0, 0, false
}

// statementAt returns the 1-based index of the statement containing byte offset off,
// or of the next statement when off falls between statements (0 when there is none).
func statementAt(stmts []Statement, off int) int {
	for i, st := range stmts {
		if off < st.End || (off == st.End && i == len(stmts)-1) {
			return i + 1
		}
	}
	if len(stmts) > 0 {
		return len(stmts)
	}
	return 0
}
