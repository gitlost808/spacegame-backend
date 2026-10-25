-- name: CreateSession :one
INSERT INTO game_sessions (player_name, token_hash, started_at, expires_at)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: GetSession :one
SELECT * FROM game_sessions WHERE id = $1;

-- name: FinishSession :one
UPDATE game_sessions
SET score = sqlc.arg(score), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id)
  AND token_hash = sqlc.arg(token_hash)
  AND finished_at IS NULL
  AND expires_at >= sqlc.arg(finished_at)
RETURNING *;

-- name: ListLeaderboard :many
SELECT id, player_name, score, started_at, finished_at
FROM game_sessions
WHERE finished_at IS NOT NULL
ORDER BY score DESC, finished_at ASC, id ASC
LIMIT $1;
