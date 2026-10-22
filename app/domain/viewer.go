package domain

type TableSummary struct {
	Name       string `json:"name"`
	RowCount   int64  `json:"row_count"`
	IsSelected bool   `json:"is_selected"`
}

type ColumnInfo struct {
	Name       string `json:"name"`
	DataType   string `json:"data_type"`
	IsNullable bool   `json:"is_nullable"`
	IsPK       bool   `json:"is_pk"`
}

type TableDataResult struct {
	TableName   string     `json:"table_name"`
	Columns     []string   `json:"columns"`
	Rows        [][]string `json:"rows"`
	TotalRows   int64      `json:"total_rows"`
	CurrentPage int        `json:"current_page"`
	TotalPages  int        `json:"total_pages"`
}
