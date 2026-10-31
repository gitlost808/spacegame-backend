-- name: CreateSession :one
INSERT INTO game_sessions (player_name, token_hash, started_at, expires_at, checkpoint_at)
VALUES ($1, $2, $3, $4, $3)
RETURNING *;

-- name: GetSession :one
SELECT * FROM game_sessions WHERE id = $1;

-- name: DeleteUnfinishedSession :execrows
DELETE FROM game_sessions
WHERE id = sqlc.arg(id)
  AND token_hash = sqlc.arg(token_hash)
  AND finished_at IS NULL;

-- name: FinishSession :one
UPDATE game_sessions
SET score = sqlc.arg(score), finished_at = sqlc.arg(finished_at)
WHERE id = sqlc.arg(id)
  AND token_hash = sqlc.arg(token_hash)
  AND finished_at IS NULL
  AND expires_at >= sqlc.arg(finished_at)
RETURNING *;

-- name: RecordCheckpoint :execrows
UPDATE game_sessions
SET checkpoint_score = sqlc.arg(checkpoint_score), checkpoint_at = sqlc.arg(checkpoint_at)
WHERE id = sqlc.arg(id)
  AND token_hash = sqlc.arg(token_hash)
  AND finished_at IS NULL
  AND checkpoint_at < sqlc.arg(checkpoint_at)
  AND expires_at >= sqlc.arg(checkpoint_at);

-- name: ListLeaderboard :many
SELECT id, player_name, score, started_at, finished_at
FROM game_sessions
WHERE finished_at IS NOT NULL
ORDER BY score DESC, finished_at ASC, id ASC
LIMIT $1;

-- name: DeleteExpiredSessions :execrows
WITH expired AS (
    SELECT candidate.id FROM game_sessions AS candidate
    WHERE candidate.finished_at IS NULL AND candidate.expires_at < sqlc.arg(cutoff)
    ORDER BY candidate.expires_at, candidate.id
    LIMIT sqlc.arg(batch_size)
    FOR UPDATE OF candidate SKIP LOCKED
)
DELETE FROM game_sessions
USING expired
WHERE game_sessions.id = expired.id AND game_sessions.finished_at IS NULL;
