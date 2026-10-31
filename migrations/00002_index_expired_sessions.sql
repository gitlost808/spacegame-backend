-- +goose Up
CREATE INDEX game_sessions_expiry_idx
    ON game_sessions (expires_at, id)
    WHERE finished_at IS NULL;

-- +goose Down
DROP INDEX game_sessions_expiry_idx;
