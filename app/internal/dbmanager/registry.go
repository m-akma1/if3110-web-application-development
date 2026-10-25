package dbmanager

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"sync"
	"time"

	"dbplatform/app/domain"
)

var validIdentifierRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ConnectionRegistry mengelola pool koneksi (*sql.DB) untuk setiap database target berdasarkan ID database
type ConnectionRegistry struct {
	mu    sync.RWMutex
	pools map[string]*sql.DB
}

func NewConnectionRegistry() *ConnectionRegistry {
	return &ConnectionRegistry{
		pools: make(map[string]*sql.DB),
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
		return nil, fmt.Errorf("pool koneksi aktif untuk database '%s' tidak tersedia", dbID)
	}

	res, err := ExecuteReadOnlyQuery(ctx, db, cleanedSQL)
	if err != nil {
		return nil, err
	}
	res.ExecutedQuery = cleanedSQL
	res.TableName = fmt.Sprintf("Query Result (%s)", dbID)
	return res, nil
}

// ListTables mengambil daftar tabel pada skema public dari database PostgreSQL target
func (r *ConnectionRegistry) ListTables(ctx context.Context, dbID string) ([]domain.TableSummary, error) {
	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, fmt.Errorf("pool database '%s' belum terdaftar", dbID)
	}

	query := `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public' AND table_type = 'BASE TABLE'
		ORDER BY table_name ASC
	`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []domain.TableSummary
	first := true
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, domain.TableSummary{
			Name:       name,
			Schema:     "public",
			IsSelected: first,
		})
		first = false
	}
	return tables, rows.Err()
}

// InspectTableDataAndSchema mengambil baris data, kolom, constraint, dan index langsung dari PostgreSQL target
func (r *ConnectionRegistry) InspectTableDataAndSchema(ctx context.Context, dbID, tableName string, page, pageSize int) (*domain.TableDataResult, error) {
	if !validIdentifierRe.MatchString(tableName) {
		return nil, fmt.Errorf("nama tabel tidak valid: %s", tableName)
	}
	db, ok := r.GetPool(dbID)
	if !ok {
		return nil, fmt.Errorf("pool database '%s' belum terdaftar", dbID)
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 25
	}
	offset := (page - 1) * pageSize

	// 1. Hitung total baris
	var totalRows int64
	countSQL := fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, tableName)
	_ = db.QueryRowContext(ctx, countSQL).Scan(&totalRows)

	totalPages := int((totalRows + int64(pageSize) - 1) / int64(pageSize))
	if totalPages < 1 {
		totalPages = 1
	}

	// 2. Ambil baris data dengan Read-Only Query
	dataSQL := fmt.Sprintf(`SELECT * FROM "%s" LIMIT %d OFFSET %d`, tableName, pageSize, offset)
	dataRes, err := ExecuteReadOnlyQuery(ctx, db, dataSQL)
	if err != nil {
		return nil, err
	}

	// Masking kolom password_hash jika sedang melihat tabel users agar aman di UI
	for colIdx, colName := range dataRes.Columns {
		if colName == "password_hash" || colName == "token_hash" || colName == "previous_token_hash" {
			for rowIdx := range dataRes.Rows {
				if len(dataRes.Rows[rowIdx][colIdx]) > 14 {
					dataRes.Rows[rowIdx][colIdx] = dataRes.Rows[rowIdx][colIdx][:14] + "..."
				}
			}
		}
	}

	// 3. Ambil metadata kolom (information_schema.columns + PK check)
	colQuery := `
		SELECT
			c.column_name,
			UPPER(c.data_type) || CASE WHEN c.character_maximum_length IS NOT NULL THEN '(' || c.character_maximum_length || ')' ELSE '' END AS data_type,
			c.is_nullable = 'YES' AS is_nullable,
			COALESCE(c.column_default, '') AS column_default,
			EXISTS (
				SELECT 1
				FROM information_schema.table_constraints tc
				JOIN information_schema.key_column_usage kcu
				  ON tc.constraint_name = kcu.constraint_name AND tc.table_schema = kcu.table_schema
				WHERE tc.constraint_type = 'PRIMARY KEY'
				  AND tc.table_schema = 'public'
				  AND tc.table_name = $1
				  AND kcu.column_name = c.column_name
			) AS is_pk
		FROM information_schema.columns c
		WHERE c.table_schema = 'public' AND c.table_name = $1
		ORDER BY c.ordinal_position ASC
	`
	colRows, err := db.QueryContext(ctx, colQuery, tableName)
	var colsMeta []domain.ColumnInfo
	if err == nil {
		defer colRows.Close()
		for colRows.Next() {
			var ci domain.ColumnInfo
			if scanErr := colRows.Scan(&ci.Name, &ci.DataType, &ci.IsNullable, &ci.DefaultValue, &ci.IsPK); scanErr == nil {
				colsMeta = append(colsMeta, ci)
			}
		}
	}

	// 4. Ambil daftar Constraints dari pg_constraint
	cstQuery := `
		SELECT
			con.conname,
			CASE con.contype
				WHEN 'p' THEN 'PRIMARY KEY'
				WHEN 'f' THEN 'FOREIGN KEY'
				WHEN 'u' THEN 'UNIQUE'
				WHEN 'c' THEN 'CHECK'
				ELSE con.contype::text
			END AS constraint_type,
			pg_get_constraintdef(con.oid) AS definition
		FROM pg_constraint con
		JOIN pg_class rel ON rel.oid = con.conrelid
		JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
		WHERE nsp.nspname = 'public' AND rel.relname = $1
		ORDER BY con.conname ASC
	`
	cstRows, err := db.QueryContext(ctx, cstQuery, tableName)
	var constraints []domain.ConstraintInfo
	if err == nil {
		defer cstRows.Close()
		for cstRows.Next() {
			var cst domain.ConstraintInfo
			if scanErr := cstRows.Scan(&cst.Name, &cst.Type, &cst.Definition); scanErr == nil {
				constraints = append(constraints, cst)
			}
		}
	}

	// 5. Ambil daftar Indexes dari pg_indexes
	idxQuery := `
		SELECT
			indexname,
			indexdef
		FROM pg_indexes
		WHERE schemaname = 'public' AND tablename = $1
		ORDER BY indexname ASC
	`
	idxRows, err := db.QueryContext(ctx, idxQuery, tableName)
	var indexes []domain.IndexInfo
	if err == nil {
		defer idxRows.Close()
		for idxRows.Next() {
			var idxName, idxDef string
			if scanErr := idxRows.Scan(&idxName, &idxDef); scanErr == nil {
				indexes = append(indexes, domain.IndexInfo{
					Name:      idxName,
					Columns:   idxDef,
					IsUnique:  regexp.MustCompile(`(?i)\bUNIQUE\b`).MatchString(idxDef),
					IndexType: "BTREE",
				})
			}
		}
	}

	return &domain.TableDataResult{
		SchemaName:  "public",
		TableName:   tableName,
		Columns:     dataRes.Columns,
		Rows:        dataRes.Rows,
		TotalRows:   totalRows,
		CurrentPage: page,
		TotalPages:  totalPages,
		ColumnsMeta: colsMeta,
		Constraints: constraints,
		Indexes:     indexes,
	}, nil
}
