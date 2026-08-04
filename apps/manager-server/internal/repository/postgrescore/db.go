package postgrescore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const DefaultSchema = "cpamp_core"

// Open creates a database/sql PostgreSQL handle whose default search path is
// the durable CPA Manager core schema, then applies all startup migrations.
// The optional schema is intended for isolated integration tests.
func Open(ctx context.Context, dsn string, schemaOverride ...string) (*sql.DB, error) {
	if strings.TrimSpace(dsn) == "" {
		return nil, errors.New("postgres core DSN is required")
	}
	schema := DefaultSchema
	if len(schemaOverride) > 0 && strings.TrimSpace(schemaOverride[0]) != "" {
		schema = strings.TrimSpace(schemaOverride[0])
	}
	if err := validateSchemaName(schema); err != nil {
		return nil, err
	}

	config, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse postgres core DSN: %w", err)
	}
	if config.RuntimeParams == nil {
		config.RuntimeParams = make(map[string]string)
	}
	config.RuntimeParams["search_path"] = schema + ",public"
	if strings.TrimSpace(config.RuntimeParams["application_name"]) == "" {
		config.RuntimeParams["application_name"] = "cpa-manager-plus-core"
	}

	baseConnector := stdlib.GetConnector(*config)
	db := sql.OpenDB(&rebindConnector{base: baseConnector})
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(2)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ping postgres core: %w", err)
	}

	var schemaExists bool
	if err := db.QueryRowContext(ctx, `select exists(select 1 from pg_namespace where nspname = $1)`, schema).Scan(&schemaExists); err != nil {
		db.Close()
		return nil, fmt.Errorf("check postgres core schema %s: %w", schema, err)
	}
	if !schemaExists {
		if _, err := db.ExecContext(ctx, `create schema `+quoteIdentifier(schema)); err != nil {
			db.Close()
			return nil, fmt.Errorf("create postgres core schema %s: %w", schema, err)
		}
	}
	if err := Migrate(ctx, db, schema); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func validateSchemaName(schema string) error {
	if schema == "" || len(schema) > 63 || !isIdentifierStart(schema[0]) {
		return fmt.Errorf("invalid postgres core schema name %q", schema)
	}
	for index := 1; index < len(schema); index++ {
		if !isIdentifierPart(schema[index]) {
			return fmt.Errorf("invalid postgres core schema name %q", schema)
		}
	}
	return nil
}

func quoteIdentifier(identifier string) string {
	return `"` + strings.ReplaceAll(identifier, `"`, `""`) + `"`
}
