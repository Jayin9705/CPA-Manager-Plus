package coremigration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/seakee/cpa-manager-plus/apps/manager-server/internal/repository/postgrescore"
)

var durableTables = []string{
	"settings",
	"model_prices",
	"api_key_aliases",
	"account_action_candidates",
	"quota_cooldowns",
	"codex_inspection_runs",
	"codex_inspection_leases",
	"codex_inspection_results",
	"codex_inspection_logs",
	"codex_inspection_disable_ownership",
	"dead_letter_events",
}

var identityTables = []string{
	"account_action_candidates",
	"quota_cooldowns",
	"codex_inspection_runs",
	"codex_inspection_results",
	"codex_inspection_logs",
	"dead_letter_events",
}

type TableReport struct {
	Table        string `json:"table"`
	SourceRows   int64  `json:"source_rows"`
	TargetRows   int64  `json:"target_rows"`
	SourceSHA256 string `json:"source_sha256"`
	TargetSHA256 string `json:"target_sha256"`
}

type Report struct {
	Tables []TableReport `json:"tables"`
}

type tableShape struct {
	name    string
	columns []string
}

// Sync replaces the not-yet-live PostgreSQL core schema from one consistent
// SQLite read transaction. The destination transaction is committed only if
// every table has identical row counts and normalized content hashes.
func Sync(ctx context.Context, sqlitePath, postgresDSN string, replace bool) (Report, error) {
	if !replace {
		return Report{}, errors.New("refusing core sync without explicit replace authorization")
	}
	source, err := openSQLiteReadOnly(sqlitePath)
	if err != nil {
		return Report{}, err
	}
	defer source.Close()
	target, err := postgrescore.Open(ctx, postgresDSN)
	if err != nil {
		return Report{}, err
	}
	defer target.Close()

	sourceTx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Report{}, fmt.Errorf("begin SQLite core snapshot: %w", err)
	}
	defer sourceTx.Rollback()
	targetTx, err := target.BeginTx(ctx, nil)
	if err != nil {
		return Report{}, fmt.Errorf("begin PostgreSQL core sync: %w", err)
	}
	defer targetTx.Rollback()
	if _, err := targetTx.ExecContext(ctx, `select pg_advisory_xact_lock(hashtext('cpamp-core-data-sync'))`); err != nil {
		return Report{}, fmt.Errorf("lock PostgreSQL core sync: %w", err)
	}

	shapes := make([]tableShape, 0, len(durableTables))
	for _, table := range durableTables {
		columns, err := sqliteColumns(ctx, sourceTx, table)
		if err != nil {
			return Report{}, err
		}
		if err := verifyTargetColumns(ctx, targetTx, table, columns); err != nil {
			return Report{}, err
		}
		shapes = append(shapes, tableShape{name: table, columns: columns})
	}
	if _, err := targetTx.ExecContext(ctx, `truncate table `+strings.Join(durableTables, ", ")+` restart identity cascade`); err != nil {
		return Report{}, fmt.Errorf("truncate PostgreSQL core tables: %w", err)
	}
	for _, shape := range shapes {
		if err := copyTable(ctx, sourceTx, targetTx, shape); err != nil {
			return Report{}, err
		}
	}
	for _, table := range identityTables {
		query := fmt.Sprintf(
			`select setval(pg_get_serial_sequence('%s.%s', 'id'), greatest(coalesce(max(id), 0), 1), max(id) is not null) from %s`,
			postgrescore.DefaultSchema, table, table,
		)
		if _, err := targetTx.ExecContext(ctx, query); err != nil {
			return Report{}, fmt.Errorf("advance PostgreSQL identity %s: %w", table, err)
		}
	}
	report, err := verifyTransactions(ctx, sourceTx, targetTx, shapes)
	if err != nil {
		return Report{}, err
	}
	if err := targetTx.Commit(); err != nil {
		return Report{}, fmt.Errorf("commit PostgreSQL core sync: %w", err)
	}
	if err := sourceTx.Commit(); err != nil {
		return Report{}, fmt.Errorf("finish SQLite core snapshot: %w", err)
	}
	return report, nil
}

func Verify(ctx context.Context, sqlitePath, postgresDSN string) (Report, error) {
	source, err := openSQLiteReadOnly(sqlitePath)
	if err != nil {
		return Report{}, err
	}
	defer source.Close()
	target, err := postgrescore.Open(ctx, postgresDSN)
	if err != nil {
		return Report{}, err
	}
	defer target.Close()
	sourceTx, err := source.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Report{}, err
	}
	defer sourceTx.Rollback()
	targetTx, err := target.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Report{}, err
	}
	defer targetTx.Rollback()
	shapes := make([]tableShape, 0, len(durableTables))
	for _, table := range durableTables {
		columns, err := sqliteColumns(ctx, sourceTx, table)
		if err != nil {
			return Report{}, err
		}
		if err := verifyTargetColumns(ctx, targetTx, table, columns); err != nil {
			return Report{}, err
		}
		shapes = append(shapes, tableShape{name: table, columns: columns})
	}
	return verifyTransactions(ctx, sourceTx, targetTx, shapes)
}

func openSQLiteReadOnly(path string) (*sql.DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("SQLite core source path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	fileURL := &url.URL{Scheme: "file", Path: filepath.ToSlash(absolute)}
	query := fileURL.Query()
	query.Set("mode", "ro")
	query.Add("_pragma", "query_only(1)")
	query.Add("_pragma", "busy_timeout(5000)")
	fileURL.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", fileURL.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping SQLite core source: %w", err)
	}
	return db, nil
}

func sqliteColumns(ctx context.Context, tx *sql.Tx, table string) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `pragma table_info("`+table+`")`)
	if err != nil {
		return nil, fmt.Errorf("inspect SQLite table %s: %w", table, err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			return nil, err
		}
		columns = append(columns, name)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(columns) == 0 {
		return nil, fmt.Errorf("SQLite core table %s is missing", table)
	}
	return columns, nil
}

func verifyTargetColumns(ctx context.Context, tx *sql.Tx, table string, sourceColumns []string) error {
	rows, err := tx.QueryContext(ctx, `select column_name from information_schema.columns
		where table_schema = ? and table_name = ? order by ordinal_position`, postgrescore.DefaultSchema, table)
	if err != nil {
		return err
	}
	defer rows.Close()
	var targetColumns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return err
		}
		targetColumns = append(targetColumns, column)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if !reflect.DeepEqual(sourceColumns, targetColumns) {
		return fmt.Errorf("core table %s column mismatch: SQLite=%v PostgreSQL=%v", table, sourceColumns, targetColumns)
	}
	return nil
}

func copyTable(ctx context.Context, source, target *sql.Tx, shape tableShape) error {
	columnList := strings.Join(shape.columns, ", ")
	rows, err := source.QueryContext(ctx, `select `+columnList+` from "`+shape.name+`"`)
	if err != nil {
		return fmt.Errorf("read SQLite core table %s: %w", shape.name, err)
	}
	defer rows.Close()
	placeholders := strings.TrimSuffix(strings.Repeat("?,", len(shape.columns)), ",")
	statement, err := target.PrepareContext(ctx, `insert into `+shape.name+` (`+columnList+`) values (`+placeholders+`)`)
	if err != nil {
		return fmt.Errorf("prepare PostgreSQL core table %s: %w", shape.name, err)
	}
	defer statement.Close()
	values := make([]any, len(shape.columns))
	destinations := make([]any, len(shape.columns))
	for index := range values {
		destinations[index] = &values[index]
	}
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			return fmt.Errorf("scan SQLite core table %s: %w", shape.name, err)
		}
		for index, value := range values {
			if bytes, ok := value.([]byte); ok {
				values[index] = string(bytes)
			}
		}
		if _, err := statement.ExecContext(ctx, values...); err != nil {
			return fmt.Errorf("insert PostgreSQL core table %s: %w", shape.name, err)
		}
	}
	return rows.Err()
}

func verifyTransactions(ctx context.Context, source, target *sql.Tx, shapes []tableShape) (Report, error) {
	report := Report{Tables: make([]TableReport, 0, len(shapes))}
	for _, shape := range shapes {
		sourceRows, sourceHash, err := tableDigest(ctx, source, shape)
		if err != nil {
			return Report{}, err
		}
		targetRows, targetHash, err := tableDigest(ctx, target, shape)
		if err != nil {
			return Report{}, err
		}
		item := TableReport{Table: shape.name, SourceRows: sourceRows, TargetRows: targetRows, SourceSHA256: sourceHash, TargetSHA256: targetHash}
		report.Tables = append(report.Tables, item)
		if sourceRows != targetRows || sourceHash != targetHash {
			return Report{}, fmt.Errorf("core table %s verification mismatch: source=%d/%s target=%d/%s", shape.name, sourceRows, sourceHash, targetRows, targetHash)
		}
	}
	return report, nil
}

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func tableDigest(ctx context.Context, queryer queryer, shape tableShape) (int64, string, error) {
	columns := strings.Join(shape.columns, ", ")
	orderColumns := append([]string(nil), shape.columns...)
	sort.Strings(orderColumns)
	rows, err := queryer.QueryContext(ctx, `select `+columns+` from `+shape.name+` order by `+strings.Join(orderColumns, ", "))
	if err != nil {
		return 0, "", fmt.Errorf("hash core table %s: %w", shape.name, err)
	}
	defer rows.Close()
	hash := sha256.New()
	values := make([]any, len(shape.columns))
	destinations := make([]any, len(shape.columns))
	for index := range values {
		destinations[index] = &values[index]
	}
	var count int64
	for rows.Next() {
		if err := rows.Scan(destinations...); err != nil {
			return 0, "", err
		}
		for _, value := range values {
			encoded := normalizeValue(value)
			_, _ = fmt.Fprintf(hash, "%d:%s|", len(encoded), encoded)
		}
		hash.Write([]byte{'\n'})
		count++
	}
	if err := rows.Err(); err != nil {
		return 0, "", err
	}
	return count, hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeValue(value any) string {
	if value == nil {
		return "<NULL>"
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		return string(typed)
	case float64:
		return strconv.FormatFloat(typed, 'g', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'g', -1, 32)
	case bool:
		if typed {
			return "1"
		}
		return "0"
	}
	valueOf := reflect.ValueOf(value)
	switch valueOf.Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.FormatInt(valueOf.Int(), 10)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.FormatUint(valueOf.Uint(), 10)
	}
	return fmt.Sprint(value)
}
