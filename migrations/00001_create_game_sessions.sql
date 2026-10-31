-- +goose Up
CREATE TABLE game_sessions (
    id uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    player_name text NOT NULL CHECK (char_length(player_name) BETWEEN 1 AND 32),
    token_hash bytea NOT NULL CHECK (octet_length(token_hash) = 32),
    started_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL CHECK (expires_at > started_at),
    finished_at timestamptz,
    score bigint,
    CONSTRAINT valid_session_result CHECK (
        (finished_at IS NULL AND score IS NULL)
        OR (
            finished_at IS NOT NULL AND score IS NOT NULL AND score >= 0
            AND finished_at >= started_at AND finished_at <= expires_at
        )
    )
);

CREATE INDEX game_sessions_leaderboard_idx
    ON game_sessions (score DESC, finished_at ASC, id ASC)
    WHERE finished_at IS NOT NULL;

-- +goose Down
DROP TABLE game_sessions;
