// Package migration runs user-written migrations on target databases: drafts, dry runs,
// transactional execution and the migration history. It is separate from internal/platformdb,
// which migrates the platform's own schema.
//
// Access control is not decided here. Callers check rbac before calling the engine.
package migration

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// Status is the lifecycle state of a migration: DRAFT (editable) -> SUCCESS or FAILED (immutable).
type Status string

const (
	StatusDraft   Status = "DRAFT"
	StatusSuccess Status = "SUCCESS"
	StatusFailed  Status = "FAILED"
)

const (
	MaxNameLength   = 200
	MaxScriptLength = 1 << 20 // 1 MiB
)

var (
	ErrNotFound          = errors.New("migration tidak ditemukan")
	ErrNotDraft          = errors.New("migration sudah dieksekusi dan tidak dapat diubah")
	ErrInvalidName       = fmt.Errorf("nama migration wajib diisi (maksimal %d karakter)", MaxNameLength)
	ErrEmptyScript       = errors.New("script SQL tidak boleh kosong")
	ErrScriptTooLarge    = fmt.Errorf("script SQL melebihi batas %d KiB", MaxScriptLength/1024)
	ErrScriptRejected    = errors.New("script ditolak")
	ErrTargetUnavailable = errors.New("koneksi ke database target tidak tersedia")
	ErrTargetBusy        = errors.New("migration lain sedang berjalan pada database ini, coba lagi nanti")
)

// Migration is one draft or executed run on a target database.
type Migration struct {
	ID           string
	DatabaseID   string
	DatabaseName string
	Version      int // 0 unless Status is SUCCESS
	Name         string
	SQL          string
	Status       Status

	CreatedBy     string // user id; empty if the user was deleted
	CreatedByName string
	CreatedAt     time.Time
	UpdatedAt     time.Time

	ExecutedBy     string
	ExecutedByName string
	ExecutedAt     time.Time // zero for drafts
	DurationMs     int64
	Error          string // formatted error for FAILED runs
}

// IsDraft reports whether the migration can still be edited and executed.
func (m *Migration) IsDraft() bool { return m != nil && m.Status == StatusDraft }

// NormalizeDraft trims and validates a draft's name and script.
func NormalizeDraft(name, script string) (string, string, error) {
	name = strings.TrimSpace(name)
	if name == "" || utf8.RuneCountInString(name) > MaxNameLength {
		return "", "", ErrInvalidName
	}
	if len(script) > MaxScriptLength {
		return "", "", ErrScriptTooLarge
	}
	if strings.TrimSpace(script) == "" {
		return "", "", ErrEmptyScript
	}
	return name, script, nil
}

// RunResult is the outcome of a dry run or an execution on the target.
type RunResult struct {
	OK         bool
	Statements int // statements in the script, as split by SplitStatements
	Duration   time.Duration
	Err        *ScriptError // set when OK is false
	Steps      []Step       // per-statement report; only dry runs fill it (see explain.go)
}

// StepKind says what a dry run did with one statement.
type StepKind string

const (
	StepExplained StepKind = "EXPLAINED" // DML run through EXPLAIN ANALYZE; Plan is set
	StepExecuted  StepKind = "EXECUTED"  // not explainable (DDL and others); run as is
	StepFailed    StepKind = "FAILED"    // this statement raised the error in RunResult.Err
	StepSkipped   StepKind = "SKIPPED"   // not run because an earlier statement failed
)

// Step is the dry-run report for one statement.
type Step struct {
	Index    int // 1-based
	SQL      string
	Kind     StepKind
	Duration time.Duration
	Plan     *Plan // for StepExplained
}

// ScriptError describes why a script failed on the target.
// Line and Column are 1-based and 0 when PostgreSQL did not report a position.
// Statement is 1-based and 0 when the failing statement is unknown.
type ScriptError struct {
	Message   string
	Code      string // SQLSTATE, e.g. 42P01
	Detail    string
	Hint      string
	Where     string // PL/pgSQL call stack, for errors inside DO blocks and functions
	Line      int
	Column    int
	Statement int
}

func (e *ScriptError) Error() string { return e.String() }

// String formats the error for the history table and the UI.
func (e *ScriptError) String() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	if e.Code != "" {
		fmt.Fprintf(&b, "ERROR %s: %s", e.Code, e.Message)
	} else {
		b.WriteString(e.Message)
	}
	switch {
	case e.Line > 0 && e.Statement > 0:
		fmt.Fprintf(&b, " (statement %d, baris %d, kolom %d)", e.Statement, e.Line, e.Column)
	case e.Line > 0:
		fmt.Fprintf(&b, " (baris %d, kolom %d)", e.Line, e.Column)
	case e.Statement > 0:
		fmt.Fprintf(&b, " (statement %d)", e.Statement)
	}
	if e.Detail != "" {
		b.WriteString("\nDETAIL: " + e.Detail)
	}
	if e.Hint != "" {
		b.WriteString("\nHINT: " + e.Hint)
	}
	if e.Where != "" {
		b.WriteString("\nCONTEXT: " + e.Where)
	}
	return b.String()
}
