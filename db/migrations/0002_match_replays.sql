-- +twl Up
CREATE TABLE IF NOT EXISTS match_replays (
    match_id UUID PRIMARY KEY REFERENCES matches(id) ON DELETE CASCADE,
    seed_text TEXT NOT NULL CHECK (seed_text ~ '^[0-9]+$'),
    initial_state_json JSONB NOT NULL,
    turn_count INTEGER NOT NULL CHECK (turn_count >= 0),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS match_replay_turns (
    match_id UUID NOT NULL REFERENCES match_replays(match_id) ON DELETE CASCADE,
    turn INTEGER NOT NULL CHECK (turn > 0),
    relative_ms BIGINT NOT NULL CHECK (relative_ms >= 0),
    inputs_json JSONB NOT NULL,
    state_hash_text TEXT NOT NULL CHECK (state_hash_text ~ '^[0-9]+$'),
    roll_hash_text TEXT NOT NULL CHECK (roll_hash_text ~ '^[0-9]+$'),
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (match_id, turn)
);

CREATE INDEX IF NOT EXISTS match_replay_turns_match_idx ON match_replay_turns (match_id, turn);

-- +twl Down
DROP INDEX IF EXISTS match_replay_turns_match_idx;
DROP TABLE IF EXISTS match_replay_turns;
DROP TABLE IF EXISTS match_replays;
