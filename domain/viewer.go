package domain

type TableSummary struct {
	Name       string
	Schema     string
	IsSelected bool
}

type ColumnInfo struct {
	Name         string
	DataType     string
	IsNullable   bool
	IsPK         bool
	DefaultValue string
}

type ConstraintInfo struct {
	Name       string
	Type       string // "PRIMARY KEY", "FOREIGN KEY", "UNIQUE", "CHECK"
	Definition string
}

type IndexInfo struct {
	Name       string
	Columns    string // key columns or expressions, comma separated
	IsUnique   bool
	IndexType  string // access method from pg_am: "BTREE", "HASH", "GIN", ...
	Definition string // full CREATE INDEX statement
}

type TableDataResult struct {
	DatabaseID    string // target the data came from; pagination links keep it
	SchemaName    string
	TableName     string
	Columns       []string
	Rows          [][]string
	TotalRows     int64
	CurrentPage   int
	TotalPages    int
	ColumnsMeta   []ColumnInfo
	Constraints   []ConstraintInfo
	Indexes       []IndexInfo
	ExecutedQuery string
	QueryError    string
	Truncated     bool // true jika hasil kueri dipotong oleh batas jumlah baris
	// IsQueryResult marks a Query Explorer result: no pagination, and the schema panel only
	// when HasSchema (the query reads exactly one table).
	IsQueryResult bool
	HasSchema     bool // ColumnsMeta/Constraints/Indexes describe SchemaName.TableName
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
