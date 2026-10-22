package domain

import "time"

type MigrationHistory struct {
	ID          string    `json:"id"`
	Filename    string    `json:"filename"`
	ExecutedAt  time.Time `json:"executed_at"`
	ExecutedBy  string    `json:"executed_by"`
	Status      string    `json:"status"`
	ExecutionMs int64     `json:"execution_ms"`
}