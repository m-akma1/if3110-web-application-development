package migration

import (
	"fmt"
	"strings"
)

// RejectedError means a statement cannot run inside the single transaction the engine wraps
// around every script. It unwraps to ErrScriptRejected.
type RejectedError struct {
	Statement int    // 1-based
	Keyword   string // e.g. "COMMIT", "CREATE INDEX CONCURRENTLY"
	Reason    string
}

func (e *RejectedError) Error() string {
	return fmt.Sprintf("statement %d (%s) ditolak: %s", e.Statement, e.Keyword, e.Reason)
}

func (e *RejectedError) Unwrap() error { return ErrScriptRejected }

const (
	reasonTxControl = "platform sudah menjalankan seluruh script dalam satu transaksi (COMMIT jika berhasil, ROLLBACK jika gagal); hapus perintah kontrol transaksi"
	reasonNoTx      = "perintah ini tidak dapat dijalankan di dalam transaksi, sehingga tidak bisa di-rollback"
	reasonCopyStdio = "COPY FROM STDIN / TO STDOUT tidak didukung; gunakan INSERT"
)

// CheckScript splits script into statements and rejects the ones that cannot run inside one
// transaction: transaction control (BEGIN, COMMIT, ROLLBACK, ...), VACUUM, CREATE/DROP DATABASE
// or TABLESPACE, ALTER SYSTEM, the CONCURRENTLY forms of index commands, DISCARD, and COPY over
// STDIN/STDOUT. It returns the statements so callers do not need to split again.
func CheckScript(script string) ([]Statement, error) {
	if len(script) > MaxScriptLength {
		return nil, ErrScriptTooLarge
	}
	stmts := SplitStatements(script)
	if len(stmts) == 0 {
		return nil, ErrEmptyScript
	}
	for i, st := range stmts {
		if kw, reason := rejectReason(st.Words); reason != "" {
			return stmts, &RejectedError{Statement: i + 1, Keyword: kw, Reason: reason}
		}
	}
	return stmts, nil
}

func rejectReason(w []string) (keyword, reason string) {
	if len(w) == 0 {
		return "", ""
	}
	at := func(i int) string {
		if i < len(w) {
			return w[i]
		}
		return ""
	}
	switch w[0] {
	case "BEGIN", "START", "COMMIT", "END", "ABORT":
		return w[0], reasonTxControl
	case "ROLLBACK":
		if at(1) == "TO" { // ROLLBACK TO SAVEPOINT stays inside the transaction
			return "", ""
		}
		return "ROLLBACK", reasonTxControl
	case "SAVEPOINT", "RELEASE":
		return "", ""
	case "PREPARE":
		if at(1) == "TRANSACTION" {
			return "PREPARE TRANSACTION", reasonTxControl
		}
	case "VACUUM":
		return "VACUUM", reasonNoTx
	case "DISCARD":
		return "DISCARD", reasonNoTx
	case "CREATE", "DROP":
		switch at(1) {
		case "DATABASE", "TABLESPACE":
			return w[0] + " " + at(1), reasonNoTx
		}
		if has(w, "INDEX") && has(w, "CONCURRENTLY") {
			return w[0] + " INDEX CONCURRENTLY", reasonNoTx
		}
	case "REINDEX":
		if has(w, "CONCURRENTLY") || at(1) == "SYSTEM" || at(1) == "DATABASE" {
			return strings.Join(w[:min(len(w), 2)], " "), reasonNoTx
		}
	case "ALTER":
		if at(1) == "SYSTEM" {
			return "ALTER SYSTEM", reasonNoTx
		}
		if at(1) == "DATABASE" && has(w, "TABLESPACE") {
			return "ALTER DATABASE SET TABLESPACE", reasonNoTx
		}
		if has(w, "DETACH") && has(w, "CONCURRENTLY") {
			return "DETACH PARTITION CONCURRENTLY", reasonNoTx
		}
	case "COPY":
		if has(w, "STDIN") || has(w, "STDOUT") {
			return "COPY", reasonCopyStdio
		}
	}
	return "", ""
}

func has(words []string, w string) bool {
	for _, x := range words {
		if x == w {
			return true
		}
	}
	return false
}
