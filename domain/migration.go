package domain

import (
	"fmt"
	"time"
)

// MigrationApproval is one group leader's approval record on a migration.
type MigrationApproval struct {
	ID              string     `json:"id"`
	MigrationID     string     `json:"migration_id"`
	GroupID         string     `json:"group_id"`
	GroupName       string     `json:"group_name"`
	LeaderID        string     `json:"leader_id,omitempty"`
	LeaderName      string     `json:"leader_name,omitempty"`
	Status          string     `json:"status"` // PENDING, APPROVED, REJECTED
	ReviewedAt      *time.Time `json:"reviewed_at,omitempty"`
	Note            string     `json:"note,omitempty"`
	RejectionReason string     `json:"rejection_reason,omitempty"`
}

// MigrationView is one draft or executed migration as the pages show it.
type MigrationView struct {
	ID              string              `json:"id"`
	DatabaseID      string              `json:"database_id"`
	DatabaseName    string              `json:"database_name"`
	Version         int                 `json:"version"` // 0 unless Status is SUCCESS
	Name            string              `json:"name"`
	SQL             string              `json:"sql"`
	Status          string              `json:"status"` // DRAFT, PENDING_REVIEW, APPROVED, REJECTED, SUCCESS, FAILED
	ReviewRequired  bool                `json:"review_required,omitempty"`
	CanApprove      bool                `json:"can_approve,omitempty"`
	RejectionReason string              `json:"rejection_reason,omitempty"`
	Approvals       []MigrationApproval `json:"approvals,omitempty"`
	CreatedByName   string              `json:"created_by_name"`
	CreatedAt       time.Time           `json:"created_at"`
	UpdatedAt       time.Time           `json:"updated_at"`
	ExecutedByName  string              `json:"executed_by_name"`
	ExecutedAt      time.Time           `json:"executed_at"` // zero for drafts
	DurationMs      int64               `json:"duration_ms"`
	Error           string              `json:"error"`
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
	DatabaseID   string          `json:"database_id"` // empty: the user has no database (empty state)
	DatabaseName string          `json:"database_name"`
	AccessLevel  string          `json:"access_level"` // VIEW_ONLY or VIEW_EDIT
	CanEdit      bool            `json:"can_edit"`     // VIEW_EDIT: drafts, dry runs and execution
	Drafts       []MigrationView `json:"drafts"`
	History      []MigrationView `json:"history"`
	Editing      *MigrationView  `json:"editing"` // the draft open in the editor; nil shows the "new draft" form
}

// MigrationDetailView is the detail page of one migration.
type MigrationDetailView struct {
	Migration MigrationView `json:"migration"`
	CanEdit   bool          `json:"can_edit"`
}

// DryRunView is the result of a dry run, with a query plan per DML statement.
type DryRunView struct {
	OK         bool             `json:"ok"`
	Statements int              `json:"statements"`
	DurationMs float64          `json:"duration_ms"`
	Error      string           `json:"error"` // formatted error when OK is false
	Steps      []DryRunStepView `json:"steps"`
}

// DryRunStepView is what the dry run did with one statement.
type DryRunStepView struct {
	Index       int            `json:"index"`
	SQL         string         `json:"sql"`
	Kind        string         `json:"kind"` // EXPLAINED, EXECUTED, FAILED or SKIPPED
	DurationMs  float64        `json:"duration_ms"`
	PlanningMs  float64        `json:"planning_ms"`
	ExecutionMs float64        `json:"execution_ms"`
	Nodes       []PlanNodeView `json:"nodes"` // depth-first; empty unless Kind is EXPLAINED
}

// PlanNodeView is one node of an EXPLAIN ANALYZE plan.
type PlanNodeView struct {
	Depth         int      `json:"depth"`
	Title         string   `json:"title"` // e.g. "Index Scan using orders_pkey on orders o"
	NodeType      string   `json:"node_type"`
	JoinType      string   `json:"join_type"`
	EstRows       float64  `json:"est_rows"` // planner estimate, per loop
	ActualRows    float64  `json:"actual_rows"` // per loop
	Loops         float64  `json:"loops"`
	StartupCost   float64  `json:"startup_cost"`
	TotalCost     float64  `json:"total_cost"`
	ActualMs      float64  `json:"actual_ms"` // actual total time per loop
	Conditions    []string `json:"conditions"`
	RowsRemoved   float64  `json:"rows_removed"` // by filter
	SharedHit     int64    `json:"shared_hit"`
	SharedRead    int64    `json:"shared_read"`
	NeverExecuted bool     `json:"never_executed"`
	Misestimated  bool     `json:"misestimated"` // estimate off by 10x or more
}
