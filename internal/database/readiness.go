package database

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Readiness checks connectivity and the schema required by this application.
// It only reads the database; it never creates migration metadata or tables.
type Readiness struct {
	Pool     *pgxpool.Pool
	Versions []int64
}

func (r Readiness) CheckReady(ctx context.Context) error {
	if err := r.Pool.Ping(ctx); err != nil {
		return fmt.Errorf("database connectivity: %w", err)
	}
	var applied int
	err := r.Pool.QueryRow(ctx, `
SELECT count(*) FROM (
    SELECT DISTINCT ON (version_id) version_id, is_applied
    FROM goose_db_version
    WHERE version_id = ANY($1::bigint[])
    ORDER BY version_id, id DESC
) AS latest WHERE is_applied`, r.Versions).Scan(&applied)
	if err != nil {
		return fmt.Errorf("read migration metadata: %w", err)
	}
	if applied != len(r.Versions) {
		return fmt.Errorf("required migrations are missing: %d of %d applied", applied, len(r.Versions))
	}
	rows, err := r.Pool.Query(ctx, `SELECT id, player_name, token_hash, started_at,
expires_at, finished_at, score, checkpoint_at, checkpoint_score FROM game_sessions LIMIT 0`)
	if err != nil {
		return fmt.Errorf("required game schema: %w", err)
	}
	rows.Close()
	return rows.Err()
}
