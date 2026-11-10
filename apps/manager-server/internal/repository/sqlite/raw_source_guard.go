package sqlite

import (
	"database/sql"
	"fmt"
	"strings"
)

type RowQueryer interface {
	QueryRow(query string, args ...any) *sql.Row
}

func HistoricalRawDeletionExists(queryer RowQueryer) (bool, error) {
	var tableExists int
	if err := queryer.QueryRow(`select count(*) from sqlite_master where type = 'table' and name = 'usage_archive_event_refs'`).Scan(&tableExists); err != nil {
		// PostgreSQL 后端没有 sqlite_master。separated 架构下用量归档/删除不在 PG 路径上，
		// 直接视为“没有历史删除”，避免 /status 等只读接口 500。
		if looksLikeNonSQLite(err) {
			return false, nil
		}
		return false, fmt.Errorf("inspect usage archive event refs table for raw deletion: %w", err)
	}
	if tableExists == 0 {
		return false, nil
	}
	var hasDeletedRaw int
	err := queryer.QueryRow(`select exists (
		select 1 from usage_archive_event_refs where raw_deleted_at_ms is not null
	)`).Scan(&hasDeletedRaw)
	if err != nil {
		return false, fmt.Errorf("inspect deleted raw events: %w", err)
	}
	return hasDeletedRaw != 0, nil
}

func ensureCompleteRawSourceForDerivedRebuild(queryer RowQueryer, reason string) error {
	hasDeletedRaw, err := HistoricalRawDeletionExists(queryer)
	if err != nil {
		return fmt.Errorf("inspect raw source for derived rebuild: %w", err)
	}
	if hasDeletedRaw {
		return fmt.Errorf(
			"cannot rebuild %s: historical raw usage events have been archived and deleted; derived rebuild requires complete raw usage history",
			reason,
		)
	}
	return nil
}

// looksLikeNonSQLite 判断错误是否来自非 SQLite 后端（如 PostgreSQL 无 sqlite_master）。
func looksLikeNonSQLite(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "sqlite_master") ||
		strings.Contains(msg, "SQLSTATE 42P01") ||
		strings.Contains(msg, "does not exist")
}
