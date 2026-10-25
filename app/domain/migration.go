package domain

import (
	"fmt"
	"time"
)

// MigrationView is one draft or executed migration as the pages show it.
type MigrationView struct {
	ID             string
	DatabaseID     string
	DatabaseName   string
	Version        int // 0 unless Status is SUCCESS
	Name           string
	SQL            string
	Status         string // DRAFT, SUCCESS or FAILED
	CreatedByName  string
	CreatedAt      time.Time
	UpdatedAt      time.Time
	ExecutedByName string
	ExecutedAt     time.Time // zero for drafts
	DurationMs     int64
	Error          string
}

// VersionLabel is "v3" for executed migrations and "-" otherwise.
func (m MigrationView) VersionLabel() string {
	if m.Version > 0 {
		return fmt.Sprintf("v%d", m.Version)
	}
	return "-"
}

// MigrationPageView is the migration page of one database.
type MigrationPageView struct {
	DatabaseID   string // empty: the user has no database (empty state)
	DatabaseName string
	AccessLevel  string // VIEW_ONLY or VIEW_EDIT
	CanEdit      bool   // VIEW_EDIT: drafts, dry runs and execution
	Drafts       []MigrationView
	History      []MigrationView
	Editing      *MigrationView // the draft open in the editor; nil shows the "new draft" form
}

// MigrationDetailView is the detail page of one migration.
type MigrationDetailView struct {
	Migration MigrationView
	CanEdit   bool
}

// DryRunView is the result of a dry run, with a query plan per DML statement.
type DryRunView struct {
	OK         bool
	Statements int
	DurationMs float64
	Error      string // formatted error when OK is false
	Steps      []DryRunStepView
}

// DryRunStepView is what the dry run did with one statement.
type DryRunStepView struct {
	Index       int
	SQL         string
	Kind        string // EXPLAINED, EXECUTED, FAILED or SKIPPED
	DurationMs  float64
	PlanningMs  float64
	ExecutionMs float64
	Nodes       []PlanNodeView // depth-first; empty unless Kind is EXPLAINED
}

// PlanNodeView is one node of an EXPLAIN ANALYZE plan.
type PlanNodeView struct {
	Depth         int
	Title         string // e.g. "Index Scan using orders_pkey on orders o"
	NodeType      string
	JoinType      string
	EstRows       float64 // planner estimate, per loop
	ActualRows    float64 // per loop
	Loops         float64
	StartupCost   float64
	TotalCost     float64
	ActualMs      float64 // actual total time per loop
	Conditions    []string
	RowsRemoved   float64 // by filter
	SharedHit     int64
	SharedRead    int64
	NeverExecuted bool
	Misestimated  bool // estimate off by 10x or more
}
