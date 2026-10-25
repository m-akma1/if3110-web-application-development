package dbmanager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"dbplatform/domain"
)

var (
	// ErrNoPool means the target has no live connection (never connected, or the last test failed).
	ErrNoPool = errors.New("database belum terhubung ke platform")
	// ErrTableNotFound means the table does not exist, is not a table, or lives in a system schema.
	ErrTableNotFound = errors.New("tabel tidak ditemukan")
)

// ConnectionRegistry mengelola pool koneksi (*sql.DB) untuk setiap database target berdasarkan ID database
type ConnectionRegistry struct {
	mu     sync.RWMutex
	pools  map[string]*sql.DB
	limits QueryLimits
}

// NewConnectionRegistry uses DefaultQueryLimits.
func NewConnectionRegistry() *ConnectionRegistry {
	return NewConnectionRegistryWithLimits(DefaultQueryLimits())
}

// NewConnectionRegistryWithLimits applies limits to every read-only query run through the registry.
func NewConnectionRegistryWithLimits(limits QueryLimits) *ConnectionRegistry {
	return &ConnectionRegistry{
		pools:  make(map[string]*sql.DB),
		limits: limits.normalized(),
	}
}

func (r *ConnectionRegistry) RegisterPool(dbID string, db *sql.DB) {
	if dbID == "" || db == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pools[dbID] = db
}

// Replace registers db under dbID and closes the pool it replaces, if any.
func (r *ConnectionRegistry) Replace(dbID string, db *sql.DB) {
	if dbID == "" || db == nil {
		return
	}
	r.mu.Lock()
	old := r.pools[dbID]
	r.pools[dbID] = db
	r.mu.Unlock()
	if old != nil && old != db {
		_ = old.Close()
	}
}

// Remove unregisters and closes the pool for dbID.
func (r *ConnectionRegistry) Remove(dbID string) {
	r.mu.Lock()
	old := r.pools[dbID]
	delete(r.pools, dbID)
	r.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// Close closes every pool (on shutdown).
func (r *ConnectionRegistry) Close() {
	r.mu.Lock()
	pools := r.pools
	r.pools = make(map[string]*sql.DB)
	r.mu.Unlock()
	for _, p := range pools {
		_ = p.Close()
	}
}

func (r *ConnectionRegistry) GetPool(dbID string) (*sql.DB, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	db, ok := r.pools[dbID]
	return db, ok && db != nil
}

// ConnectAndRegister membuka koneksi baru ke PostgreSQL target, melakukan Ping, dan menyimpannya ke registry
func (r *ConnectionRegistry) ConnectAndRegister(ctx context.Context, dbID, dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return err
	}
	pingCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	if err := db.PingContext(pingCtx); err != nil {
		_ = db.Close()
		return err
	}
	r.RegisterPool(dbID, db)
	return nil
}

// ExecuteQueryOnDB menjalankan kueri Read-Only terhadap database yang dipilih (dbID)
func (r *ConnectionRegistry) ExecuteQueryOnDB(ctx context.Context, dbID, rawSQL string) (*domain.TableDataResult, error) {
	cleanedSQL, _, err := ValidateReadOnlySQL(rawSQL)
	if err != nil {
		return nil, err
	}

	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, ErrNoPool
	}

	res, err := ExecuteReadOnlyQuery(ctx, db, cleanedSQL, r.limits)
	if err != nil {
		return nil, err
	}
	res.DatabaseID = dbID
	res.ExecutedQuery = cleanedSQL
	res.TableName = "Hasil Query"
	res.IsQueryResult = true
	return res, nil
}

// userSchemas filters pg_namespace n down to schemas a user created: no pg_catalog,
// information_schema, pg_toast or temporary schemas (names starting with pg_ are reserved).
const userSchemas = `n.nspname <> 'information_schema' AND n.nspname NOT LIKE 'pg\_%'`

// ListTables returns the tables (plain and partitioned, not partitions) of every user schema,
// ordered by schema then name.
func (r *ConnectionRegistry) ListTables(ctx context.Context, dbID string) ([]domain.TableSummary, error) {
	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, ErrNoPool
	}

	var tables []domain.TableSummary
	err := withReadOnlyTx(ctx, db, r.limits, 1, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
			SELECT n.nspname, c.relname
			FROM pg_class c
			JOIN pg_namespace n ON n.oid = c.relnamespace
			WHERE c.relkind IN ('r', 'p') AND NOT c.relispartition AND `+userSchemas+`
			ORDER BY n.nspname, c.relname`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var t domain.TableSummary
			if err := rows.Scan(&t.Schema, &t.Name); err != nil {
				return err
			}
			tables = append(tables, t)
		}
		return rows.Err()
	})
	if err != nil {
		return nil, err
	}
	return tables, nil
}

// InspectTableDataAndSchema returns one page of rows plus the columns, constraints and indexes
// of schema.table (an empty schema resolves through search_path). Rows are ordered by the primary key when there is one, so pages are stable.
// Every query runs in one read-only transaction under the viewer's statement timeout.
func (r *ConnectionRegistry) InspectTableDataAndSchema(ctx context.Context, dbID, schema, table string, page, pageSize int) (*domain.TableDataResult, error) {
	if table == "" {
		return nil, ErrTableNotFound
	}
	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, ErrNoPool
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	pageSize = min(pageSize, r.limits.MaxRows)

	var res *domain.TableDataResult
	// count + page of rows + catalog lookups: allow two full timeouts in total
	err := withReadOnlyTx(ctx, db, r.limits, 2, func(ctx context.Context, tx *sql.Tx) error {
		meta, err := describeTable(ctx, tx, schema, table)
		if err != nil {
			return err
		}
		res = meta
		qualified := quoteIdent(meta.SchemaName) + "." + quoteIdent(meta.TableName)

		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+qualified).Scan(&res.TotalRows); err != nil {
			return fmt.Errorf("menghitung baris: %w", err)
		}
		res.TotalPages = max(1, int((res.TotalRows+int64(pageSize)-1)/int64(pageSize)))
		res.CurrentPage = page

		var orderBy []string
		for _, c := range meta.ColumnsMeta {
			if c.IsPK {
				orderBy = append(orderBy, quoteIdent(c.Name))
			}
		}
		dataSQL := `SELECT * FROM ` + qualified
		if len(orderBy) > 0 {
			dataSQL += ` ORDER BY ` + strings.Join(orderBy, ", ")
		}
		dataSQL += fmt.Sprintf(` LIMIT %d OFFSET %d`, pageSize, (page-1)*pageSize)

		res.Columns, res.Rows, _, err = queryRows(ctx, tx, dataSQL, pageSize)
		if err != nil {
			return fmt.Errorf("membaca data: %w", err)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.DatabaseID = dbID
	maskSecretColumns(res)
	return res, nil
}

// DescribeTable returns the columns, constraints and indexes of a table without reading rows.
// An empty schema resolves the name through the connection's search_path, as a query would.
func (r *ConnectionRegistry) DescribeTable(ctx context.Context, dbID, schema, table string) (*domain.TableDataResult, error) {
	if table == "" {
		return nil, ErrTableNotFound
	}
	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, ErrNoPool
	}
	var res *domain.TableDataResult
	err := withReadOnlyTx(ctx, db, r.limits, 1, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		res, err = describeTable(ctx, tx, schema, table)
		return err
	})
	if err != nil {
		return nil, err
	}
	res.DatabaseID = dbID
	return res, nil
}

// describeTable resolves the table through the catalog (never by formatting the user's input
// into SQL) and loads its metadata.
func describeTable(ctx context.Context, tx *sql.Tx, schema, table string) (*domain.TableDataResult, error) {
	name := quoteIdent(table)
	if schema != "" {
		name = quoteIdent(schema) + "." + name
	}
	res := &domain.TableDataResult{HasSchema: true}
	err := tx.QueryRowContext(ctx, `
		SELECT n.nspname, c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE c.oid = to_regclass($1) AND c.relkind IN ('r', 'p') AND `+userSchemas,
		name).Scan(&res.SchemaName, &res.TableName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrTableNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("mencari tabel: %w", err)
	}

	// From here on the table is addressed by its catalog name, cast to regclass by the server.
	rel := quoteIdent(res.SchemaName) + "." + quoteIdent(res.TableName)
	if res.ColumnsMeta, err = loadColumns(ctx, tx, rel); err != nil {
		return nil, fmt.Errorf("membaca kolom: %w", err)
	}
	if res.Constraints, err = loadConstraints(ctx, tx, rel); err != nil {
		return nil, fmt.Errorf("membaca constraint: %w", err)
	}
	if res.Indexes, err = loadIndexes(ctx, tx, rel); err != nil {
		return nil, fmt.Errorf("membaca index: %w", err)
	}
	return res, nil
}

func loadColumns(ctx context.Context, tx *sql.Tx, rel string) ([]domain.ColumnInfo, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			a.attname,
			format_type(a.atttypid, a.atttypmod),
			NOT a.attnotnull,
			COALESCE(pg_get_expr(d.adbin, d.adrelid), ''),
			EXISTS (
				SELECT 1 FROM pg_index i
				WHERE i.indrelid = a.attrelid AND i.indisprimary AND a.attnum = ANY (i.indkey)
			)
		FROM pg_attribute a
		LEFT JOIN pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE a.attrelid = $1::text::regclass AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum`, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ColumnInfo
	for rows.Next() {
		var c domain.ColumnInfo
		if err := rows.Scan(&c.Name, &c.DataType, &c.IsNullable, &c.DefaultValue, &c.IsPK); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadConstraints(ctx context.Context, tx *sql.Tx, rel string) ([]domain.ConstraintInfo, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			con.conname,
			CASE con.contype
				WHEN 'p' THEN 'PRIMARY KEY'
				WHEN 'f' THEN 'FOREIGN KEY'
				WHEN 'u' THEN 'UNIQUE'
				WHEN 'c' THEN 'CHECK'
				WHEN 'x' THEN 'EXCLUDE'
				WHEN 'n' THEN 'NOT NULL'
				WHEN 't' THEN 'TRIGGER'
				ELSE con.contype::text
			END,
			pg_get_constraintdef(con.oid)
		FROM pg_constraint con
		WHERE con.conrelid = $1::text::regclass
		ORDER BY con.conname`, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.ConstraintInfo
	for rows.Next() {
		var c domain.ConstraintInfo
		if err := rows.Scan(&c.Name, &c.Type, &c.Definition); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func loadIndexes(ctx context.Context, tx *sql.Tx, rel string) ([]domain.IndexInfo, error) {
	rows, err := tx.QueryContext(ctx, `
		SELECT
			i.relname,
			array_to_string(ARRAY(
				SELECT pg_get_indexdef(x.indexrelid, k, true)
				FROM generate_series(1, x.indnkeyatts) AS k
				ORDER BY k
			), ', '),
			x.indisunique,
			upper(am.amname),
			pg_get_indexdef(x.indexrelid)
		FROM pg_index x
		JOIN pg_class i ON i.oid = x.indexrelid
		JOIN pg_am am ON am.oid = i.relam
		WHERE x.indrelid = $1::text::regclass
		ORDER BY i.relname`, rel)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []domain.IndexInfo
	for rows.Next() {
		var ix domain.IndexInfo
		if err := rows.Scan(&ix.Name, &ix.Columns, &ix.IsUnique, &ix.IndexType, &ix.Definition); err != nil {
			return nil, err
		}
		out = append(out, ix)
	}
	return out, rows.Err()
}

// quoteIdent quotes a PostgreSQL identifier, like quote_ident() but always quoted.
func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

// maskSecretColumns shortens credential-looking values in the table view. It is a display
// courtesy for targets that happen to store such columns, not a security boundary.
func maskSecretColumns(res *domain.TableDataResult) {
	for colIdx, colName := range res.Columns {
		if colName == "password_hash" || colName == "token_hash" || colName == "previous_token_hash" {
			for rowIdx := range res.Rows {
				if len(res.Rows[rowIdx][colIdx]) > 14 {
					res.Rows[rowIdx][colIdx] = res.Rows[rowIdx][colIdx][:14] + "..."
				}
			}
		}
	}
}
