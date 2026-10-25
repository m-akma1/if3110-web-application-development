package domain

type TableSummary struct {
	Name       string `json:"name"`
	Schema     string `json:"schema"`
	RowCount   int64  `json:"row_count"`
	IsSelected bool   `json:"is_selected"`
}

type ColumnInfo struct {
	Name         string `json:"name"`
	DataType     string `json:"data_type"`
	IsNullable   bool   `json:"is_nullable"`
	IsPK         bool   `json:"is_pk"`
	DefaultValue string `json:"default_value"`
}

type ConstraintInfo struct {
	Name       string `json:"name"`
	Type       string `json:"type"` // "PRIMARY KEY", "FOREIGN KEY", "UNIQUE", "CHECK"
	Definition string `json:"definition"`
}

type IndexInfo struct {
	Name       string `json:"name"`
	Columns    string `json:"columns"` // key columns or expressions, comma separated
	IsUnique   bool   `json:"is_unique"`
	IndexType  string `json:"index_type"` // access method from pg_am: "BTREE", "HASH", "GIN", ...
	Definition string `json:"definition"` // full CREATE INDEX statement
}

type TableDataResult struct {
	DatabaseID    string           `json:"database_id"` // target the data came from; pagination links keep it
	SchemaName    string           `json:"schema_name"`
	TableName     string           `json:"table_name"`
	Columns       []string         `json:"columns"`
	Rows          [][]string       `json:"rows"`
	TotalRows     int64            `json:"total_rows"`
	CurrentPage   int              `json:"current_page"`
	TotalPages    int              `json:"total_pages"`
	ColumnsMeta   []ColumnInfo     `json:"columns_meta,omitempty"`
	Constraints   []ConstraintInfo `json:"constraints,omitempty"`
	Indexes       []IndexInfo      `json:"indexes,omitempty"`
	ExecutedQuery string           `json:"executed_query,omitempty"`
	QueryError    string           `json:"query_error,omitempty"`
	Truncated     bool             `json:"truncated,omitempty"` // true jika hasil kueri dipotong oleh batas jumlah baris
	// IsQueryResult marks a Query Explorer result: no pagination, and the schema panel only
	// when HasSchema (the query reads exactly one table).
	IsQueryResult bool `json:"is_query_result,omitempty"`
	HasSchema     bool `json:"has_schema,omitempty"` // ColumnsMeta/Constraints/Indexes describe SchemaName.TableName
}

// ViewerPageView is the Database Viewer page of one database.
type ViewerPageView struct {
	DatabaseID   string // empty: the user has no database (empty state)
	DatabaseName string
	Tables       []TableSummary
	Selected     *TableDataResult // the first table, opened on load; nil when there is none
	// Error is set when the database could not be read at all (no live connection or the
	// table list failed): the page shows it with a retry link instead of the viewer.
	Error string
	// TableError is set when the table list loaded but the first table did not.
	TableError string
}
