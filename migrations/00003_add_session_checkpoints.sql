-- +goose Up
ALTER TABLE game_sessions
    ADD COLUMN checkpoint_at timestamptz,
    ADD COLUMN checkpoint_score bigint NOT NULL DEFAULT 0;

UPDATE game_sessions SET checkpoint_at = started_at;

ALTER TABLE game_sessions
    ALTER COLUMN checkpoint_at SET NOT NULL,
    ADD CONSTRAINT valid_session_checkpoint CHECK (
        checkpoint_score >= 0 AND checkpoint_at >= started_at AND checkpoint_at <= expires_at
    );

-- +goose Down
ALTER TABLE game_sessions
    DROP CONSTRAINT valid_session_checkpoint,
    DROP COLUMN checkpoint_score,
    DROP COLUMN checkpoint_at;
