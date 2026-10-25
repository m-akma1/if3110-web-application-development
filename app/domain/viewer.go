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
	Name      string `json:"name"`
	Columns   string `json:"columns"`
	IsUnique  bool   `json:"is_unique"`
	IndexType string `json:"index_type"` // "BTREE", "HASH", dll.
}

type TableDataResult struct {
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
}
