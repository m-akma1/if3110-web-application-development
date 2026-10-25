package dbmanager

import "strings"

// A small PostgreSQL tokenizer for the viewer: enough to skip comments, string literals
// (including E'' and dollar quotes) and quoted identifiers, so the checks below never look
// inside text. It is not a parser.

type tokenKind int

const (
	tokWord   tokenKind = iota // unquoted identifier or keyword
	tokQuoted                  // "quoted identifier"
	tokString                  // string literal of any kind
	tokNumber
	tokPunct // any other single character
)

type sqlToken struct {
	kind tokenKind
	text string // identifiers: as written (quotes removed); punctuation: the character
}

func (t sqlToken) isKeyword(kw string) bool {
	return t.kind == tokWord && strings.EqualFold(t.text, kw)
}

func (t sqlToken) isPunct(p string) bool {
	return t.kind == tokPunct && t.text == p
}

func (t sqlToken) isIdent() bool {
	return t.kind == tokWord || t.kind == tokQuoted
}

// name is how PostgreSQL resolves the identifier: unquoted names fold to lower case.
func (t sqlToken) name() string {
	if t.kind == tokWord {
		return strings.ToLower(t.text)
	}
	return t.text
}

func isWordStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c >= 0x80
}

func isWordChar(c byte) bool {
	return isWordStart(c) || c == '$' || (c >= '0' && c <= '9')
}

func lexSQL(s string) []sqlToken {
	var out []sqlToken
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f':
			i++
		case c == '-' && i+1 < len(s) && s[i+1] == '-':
			for i < len(s) && s[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(s) && s[i+1] == '*':
			// block comments nest in PostgreSQL
			depth := 0
			for i < len(s) {
				if s[i] == '/' && i+1 < len(s) && s[i+1] == '*' {
					depth++
					i += 2
				} else if s[i] == '*' && i+1 < len(s) && s[i+1] == '/' {
					depth--
					i += 2
					if depth == 0 {
						break
					}
				} else {
					i++
				}
			}
		case c == '\'':
			i = skipQuoted(s, i, '\'', false)
			out = append(out, sqlToken{kind: tokString})
		case c == '"':
			end := skipQuoted(s, i, '"', false)
			inner := s[i+1 : max(i+1, end-1)]
			out = append(out, sqlToken{kind: tokQuoted, text: strings.ReplaceAll(inner, `""`, `"`)})
			i = end
		case c == '$':
			if tag, ok := dollarTag(s, i); ok {
				end := strings.Index(s[i+len(tag):], tag)
				if end < 0 {
					i = len(s)
				} else {
					i += len(tag) + end + len(tag)
				}
				out = append(out, sqlToken{kind: tokString})
			} else {
				out = append(out, sqlToken{kind: tokPunct, text: "$"})
				i++
			}
		case (c == 'E' || c == 'e') && i+1 < len(s) && s[i+1] == '\'':
			i = skipQuoted(s, i+1, '\'', true)
			out = append(out, sqlToken{kind: tokString})
		case isWordStart(c):
			j := i + 1
			for j < len(s) && isWordChar(s[j]) {
				j++
			}
			out = append(out, sqlToken{kind: tokWord, text: s[i:j]})
			i = j
		case c >= '0' && c <= '9':
			j := i + 1
			for j < len(s) && (isWordChar(s[j]) || s[j] == '.') {
				j++
			}
			out = append(out, sqlToken{kind: tokNumber, text: s[i:j]})
			i = j
		default:
			out = append(out, sqlToken{kind: tokPunct, text: string(c)})
			i++
		}
	}
	return out
}

// skipQuoted returns the index just past the literal that opens at s[start].
// A doubled quote is an escaped quote; with backslash, \x escapes too (E” strings).
func skipQuoted(s string, start int, q byte, backslash bool) int {
	for i := start + 1; i < len(s); i++ {
		switch {
		case backslash && s[i] == '\\':
			i++
		case s[i] == q:
			if i+1 < len(s) && s[i+1] == q {
				i++
				continue
			}
			return i + 1
		}
	}
	return len(s)
}

// dollarTag reads a $tag$ opener at s[i].
func dollarTag(s string, i int) (string, bool) {
	j := i + 1
	for j < len(s) && s[j] != '$' {
		if !isWordChar(s[j]) || s[j] == '$' || (j == i+1 && s[j] >= '0' && s[j] <= '9') {
			return "", false
		}
		j++
	}
	if j >= len(s) {
		return "", false
	}
	return s[i : j+1], true
}

// blockedFunctions are server functions the Data Viewer refuses even inside a read-only
// transaction. The target role should not be allowed most of them anyway (pg_read_file needs
// superuser or pg_read_server_files); this is the second layer if an admin registers a
// connection with more privileges than intended.
var blockedFunctions = map[string]bool{
	// A role may signal its own backends, which includes the platform's other connections.
	"pg_terminate_backend": true,
	"pg_cancel_backend":    true,
	// Session-level advisory locks outlive the rolled-back transaction on a pooled connection.
	"pg_advisory_lock":            true,
	"pg_advisory_lock_shared":     true,
	"pg_try_advisory_lock":        true,
	"pg_try_advisory_lock_shared": true,
	// Server file system and configuration.
	"pg_read_file":        true,
	"pg_read_binary_file": true,
	"pg_ls_dir":           true,
	"pg_stat_file":        true,
	"lo_import":           true,
	"lo_export":           true,
	"pg_reload_conf":      true,
	"pg_rotate_logfile":   true,
	// Connections to other servers.
	"dblink":           true,
	"dblink_exec":      true,
	"dblink_connect":   true,
	"dblink_connect_u": true,
}

// findBlockedFunction returns the first blocked function the query calls, or "".
func findBlockedFunction(tokens []sqlToken) string {
	for i := 0; i+1 < len(tokens); i++ {
		if tokens[i].isIdent() && tokens[i+1].isPunct("(") {
			if name := strings.ToLower(tokens[i].text); blockedFunctions[name] {
				return name
			}
		}
	}
	return ""
}

// selectTailKeywords may directly follow the table (and its alias) in a single-table SELECT.
var selectTailKeywords = map[string]bool{
	"WHERE": true, "GROUP": true, "HAVING": true, "ORDER": true, "LIMIT": true,
	"OFFSET": true, "FETCH": true, "WINDOW": true, "FOR": true,
}

// SingleTableSource reports the table a query reads when it is a plain SELECT from exactly
// one table: no joins, subqueries, CTEs, set operations or table functions. schema is ""
// when the name is not schema-qualified (the caller resolves it through search_path).
// Anything it is not sure about returns ok=false, so the viewer shows results only.
func SingleTableSource(query string) (schema, table string, ok bool) {
	tokens := lexSQL(query)
	for len(tokens) > 0 && tokens[len(tokens)-1].isPunct(";") {
		tokens = tokens[:len(tokens)-1]
	}
	if len(tokens) == 0 || !tokens[0].isKeyword("SELECT") {
		return "", "", false
	}

	from, depth, selects := -1, 0, 0
	for i, t := range tokens {
		switch {
		case t.isPunct("("):
			depth++
		case t.isPunct(")"):
			depth--
		case t.isKeyword("SELECT"):
			selects++
		case t.isKeyword("JOIN"), t.isKeyword("UNION"), t.isKeyword("INTERSECT"),
			t.isKeyword("EXCEPT"), t.isKeyword("LATERAL"), t.isPunct(";"):
			return "", "", false
		case t.isKeyword("FROM") && depth == 0:
			if from >= 0 {
				return "", "", false
			}
			from = i
		}
	}
	if selects != 1 || from < 0 {
		return "", "", false
	}

	at := func(i int) sqlToken {
		if i < len(tokens) {
			return tokens[i]
		}
		return sqlToken{kind: tokPunct}
	}
	i := from + 1
	if at(i).isKeyword("ONLY") {
		i++
	}
	if !at(i).isIdent() {
		return "", "", false
	}
	first := at(i)
	i++
	if at(i).isPunct(".") {
		if !at(i + 1).isIdent() {
			return "", "", false
		}
		schema, table = first.name(), at(i+1).name()
		i += 2
	} else {
		table = first.name()
	}
	if at(i).isPunct(".") || at(i).isPunct("(") {
		return "", "", false // db.schema.table or a function call
	}

	// optional alias
	switch {
	case at(i).isKeyword("AS"):
		if !at(i + 1).isIdent() {
			return "", "", false
		}
		i += 2
	case at(i).kind == tokQuoted,
		at(i).kind == tokWord && !selectTailKeywords[strings.ToUpper(at(i).text)]:
		i++
	}

	if i == len(tokens) || (at(i).kind == tokWord && selectTailKeywords[strings.ToUpper(at(i).text)]) {
		return schema, table, true
	}
	return "", "", false
}
