package adminauth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const activeAdminsView = "cpamp_auth.active_admins"

type postgresAdminRepository struct {
	pool *pgxpool.Pool
}

func newPostgresAdminRepository(ctx context.Context, dsn string) (AdminRepository, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, errors.New("CPAMP_AUTH_PG_DSN is required")
	}
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse administrator PostgreSQL DSN: %w", err)
	}
	if poolConfig.MaxConns > 4 {
		poolConfig.MaxConns = 4
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("open administrator PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping administrator PostgreSQL: %w", err)
	}
	if _, err := pool.Exec(ctx, `SELECT 1 FROM `+activeAdminsView+` LIMIT 1`); err != nil {
		pool.Close()
		return nil, fmt.Errorf("verify administrator PostgreSQL view: %w", err)
	}
	return &postgresAdminRepository{pool: pool}, nil
}

func (r *postgresAdminRepository) FindActiveByEmail(ctx context.Context, normalizedEmail string) (ActiveAdmin, error) {
	return r.find(ctx, `
SELECT id, email, password_hash
FROM `+activeAdminsView+`
WHERE LOWER(BTRIM(email)) = $1
LIMIT 2`, normalizedEmail)
}

func (r *postgresAdminRepository) FindActiveByID(ctx context.Context, id int64) (ActiveAdmin, error) {
	return r.find(ctx, `
SELECT id, email, password_hash
FROM `+activeAdminsView+`
WHERE id = $1
LIMIT 2`, id)
}

func (r *postgresAdminRepository) find(ctx context.Context, query string, arg any) (ActiveAdmin, error) {
	if r == nil || r.pool == nil {
		return ActiveAdmin{}, errors.New("administrator PostgreSQL pool is not configured")
	}
	rows, err := r.pool.Query(ctx, query, arg)
	if err != nil {
		return ActiveAdmin{}, err
	}
	defer rows.Close()
	admins := make([]ActiveAdmin, 0, 2)
	for rows.Next() {
		var admin ActiveAdmin
		if err := rows.Scan(&admin.ID, &admin.Email, &admin.PasswordHash); err != nil {
			return ActiveAdmin{}, err
		}
		admins = append(admins, admin)
	}
	if err := rows.Err(); err != nil {
		return ActiveAdmin{}, err
	}
	switch len(admins) {
	case 0:
		return ActiveAdmin{}, ErrAdminNotFound
	case 1:
		return admins[0], nil
	default:
		return ActiveAdmin{}, ErrAmbiguousAdmin
	}
}

func (r *postgresAdminRepository) Close() error {
	if r != nil && r.pool != nil {
		r.pool.Close()
	}
	return nil
}
