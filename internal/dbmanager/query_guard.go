package dbmanager

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"dbplatform/domain"
)

var (
	ErrEmptyQuery       = errors.New("kueri SQL tidak boleh kosong")
	ErrMultiStatement   = errors.New("multi-statement (;) tidak diizinkan pada Data Viewer")
	ErrNotSelectQuery   = errors.New("hanya kueri pembacaan data (SELECT / SHOW) yang diizinkan pada Data Viewer")
	ErrMutationDetected = errors.New("kueri mengandung perintah perubahan data/skema yang dilarang pada mode Read-Only")
	ErrBlockedFunction  = errors.New("fungsi server ini tidak diizinkan pada Data Viewer")
)

var (
	lineCommentRe  = regexp.MustCompile(`(?m)--.*$`)
	blockCommentRe = regexp.MustCompile(`(?s)/\*.*?\*/`)
	singleQuoteRe  = regexp.MustCompile(`'([^']|'')*'`)
	wordTokenRe    = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*`)
)

var forbiddenKeywords = map[string]bool{
	"INSERT":   true,
	"UPDATE":   true,
	"DELETE":   true,
	"DROP":     true,
	"ALTER":    true,
	"TRUNCATE": true,
	"CREATE":   true,
	"REPLACE":  true,
	"GRANT":    true,
	"REVOKE":   true,
	"MERGE":    true,
	"CALL":     true,
	"EXEC":     true,
	"EXECUTE":  true,
	"COPY":     true,
	"VACUUM":   true,
}

var allowedStartKeywords = map[string]bool{
	"SELECT": true,
	"WITH":   true,
	"SHOW":   true,
}

// ValidateReadOnlySQL memeriksa apakah kueri SQL aman dan murni bersifat Read-Only (Lapis 2: Backend Statement Guard).
// Mengembalikan kueri bersih, kata kunci terlarang (jika ditemukan), dan error.
func ValidateReadOnlySQL(rawSQL string) (string, string, error) {
	// 1. Bersihkan komentar blok dan komentar baris
	noBlock := blockCommentRe.ReplaceAllString(rawSQL, " ")
	noComments := strings.TrimSpace(lineCommentRe.ReplaceAllString(noBlock, " "))
	if noComments == "" {
		return "", "", ErrEmptyQuery
	}

	// 2. Hilangkan string literal sementara agar pengecekan ';' dan keyword tidak salah mendeteksi isi teks kutip
	maskedLiterals := singleQuoteRe.ReplaceAllString(noComments, "''")

	// Hapus trailing semicolon tunggal di ujung akhir kueri
	trimmedMasked := strings.TrimSpace(maskedLiterals)
	trimmedMasked = strings.TrimSuffix(trimmedMasked, ";")
	if strings.Contains(trimmedMasked, ";") {
		return "", ";", ErrMultiStatement
	}

	// 3. Ekstrak seluruh token kata dan periksa keyword mutasi
	tokens := wordTokenRe.FindAllString(trimmedMasked, -1)
	if len(tokens) == 0 {
		return "", "", ErrEmptyQuery
	}

	for _, tok := range tokens {
		upper := strings.ToUpper(tok)
		if forbiddenKeywords[upper] {
			return "", upper, fmt.Errorf("%w: keyword '%s' tidak diizinkan", ErrMutationDetected, upper)
		}
	}

	// Fungsi server berbahaya tetap ditolak walaupun berada di dalam SELECT (lihat blockedFunctions)
	if fn := findBlockedFunction(lexSQL(noComments)); fn != "" {
		return "", fn, fmt.Errorf("%w: %s()", ErrBlockedFunction, fn)
	}

	firstUpper := strings.ToUpper(tokens[0])
	if !allowedStartKeywords[firstUpper] {
		return "", firstUpper, fmt.Errorf("%w (ditemukan '%s')", ErrNotSelectQuery, firstUpper)
	}

	cleaned := strings.TrimSpace(strings.TrimSuffix(noComments, ";"))
	return cleaned, "", nil
}

// QueryLimits membatasi biaya satu kueri Read-Only pada database target.
type QueryLimits struct {
	Timeout time.Duration // dikirim sebagai SET LOCAL statement_timeout
	MaxRows int           // baris setelah batas ini tidak dibaca, hasil ditandai Truncated
}

const (
	defaultQueryTimeout = 5 * time.Second
	defaultQueryMaxRows = 500
)

// DefaultQueryLimits returns the built-in limits. config.Load overrides them from QUERY_* env vars.
func DefaultQueryLimits() QueryLimits {
	return QueryLimits{Timeout: defaultQueryTimeout, MaxRows: defaultQueryMaxRows}
}

func (l QueryLimits) normalized() QueryLimits {
	if l.Timeout <= 0 {
		l.Timeout = defaultQueryTimeout
	}
	if l.MaxRows <= 0 {
		l.MaxRows = defaultQueryMaxRows
	}
	return l
}

// ExecuteReadOnlyQuery mengeksekusi kueri di dalam Read-Only Transaction pada database target (Lapis 3: Database Read-Only Transaction).
// Kueri dibatasi statement_timeout dan jumlah baris maksimum sesuai limits.
func ExecuteReadOnlyQuery(ctx context.Context, db *sql.DB, rawSQL string, limits QueryLimits) (*domain.TableDataResult, error) {
	cleanedSQL, _, err := ValidateReadOnlySQL(rawSQL)
	if err != nil {
		return nil, err
	}
	if db == nil {
		return nil, errors.New("koneksi database belum tersedia")
	}

	limits = limits.normalized()
	var res *domain.TableDataResult
	err = withReadOnlyTx(ctx, db, limits, 1, func(ctx context.Context, tx *sql.Tx) error {
		cols, rows, truncated, err := queryRows(ctx, tx, cleanedSQL, limits.MaxRows)
		if err != nil {
			return err
		}
		res = &domain.TableDataResult{
			TableName:   "Hasil Query Read-Only",
			Columns:     cols,
			Rows:        rows,
			TotalRows:   int64(len(rows)),
			CurrentPage: 1,
			TotalPages:  1,
			Truncated:   truncated,
		}
		return nil
	})
	return res, err
}

// withReadOnlyTx runs fn in a READ ONLY transaction that is always rolled back.
// Every statement gets SET LOCAL statement_timeout; the context gets a client-side deadline
// of statements x timeout (+1s) as a backup in case the server-side timeout does not apply.
func withReadOnlyTx(ctx context.Context, db *sql.DB, limits QueryLimits, statements int, fn func(context.Context, *sql.Tx) error) error {
	limits = limits.normalized()
	ctx, cancel := context.WithTimeout(ctx, time.Duration(max(statements, 1))*limits.Timeout+time.Second)
	defer cancel()

	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	// SET tidak menerima parameter bind; nilai berupa integer milidetik sehingga aman disisipkan
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("SET LOCAL statement_timeout = %d", limits.Timeout.Milliseconds())); err != nil {
		return err
	}
	return fn(ctx, tx)
}

// queryRows reads at most maxRows rows as display strings; truncated reports that more were available.
func queryRows(ctx context.Context, tx *sql.Tx, query string, maxRows int, args ...any) ([]string, [][]string, bool, error) {
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, false, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, nil, false, err
	}

	var resultRows [][]string
	truncated := false
	for rows.Next() {
		if len(resultRows) >= maxRows {
			truncated = true
			break
		}
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, nil, false, err
		}
		rowStr := make([]string, len(cols))
		for i, v := range vals {
			if v == nil {
				rowStr[i] = "NULL"
			} else if b, ok := v.([]byte); ok {
				rowStr[i] = string(b)
			} else {
				rowStr[i] = fmt.Sprintf("%v", v)
			}
		}
		resultRows = append(resultRows, rowStr)
	}
	if err := rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return cols, resultRows, truncated, nil
}
