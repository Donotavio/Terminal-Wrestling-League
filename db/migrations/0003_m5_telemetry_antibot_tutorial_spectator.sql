-- +twl Up
CREATE TABLE IF NOT EXISTS player_profiles (
    player_id UUID PRIMARY KEY REFERENCES players(id) ON DELETE CASCADE,
    tutorial_completed BOOLEAN NOT NULL DEFAULT FALSE,
    tutorial_completed_at TIMESTAMPTZ NULL,
    tutorial_runs INTEGER NOT NULL DEFAULT 0 CHECK (tutorial_runs >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS tutorial_runs (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    result TEXT NOT NULL CHECK (result IN ('win', 'loss', 'draw')),
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (ended_at >= started_at)
);

CREATE INDEX IF NOT EXISTS tutorial_runs_player_created_idx ON tutorial_runs (player_id, created_at DESC);

CREATE TABLE IF NOT EXISTS telemetry_match_turns (
    match_id UUID NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    turn INTEGER NOT NULL CHECK (turn > 0),
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    is_human BOOLEAN NOT NULL,
    action TEXT NOT NULL,
    target_zone TEXT NOT NULL,
    decision_ms INTEGER NOT NULL CHECK (decision_ms >= 0),
    success BOOLEAN NOT NULL,
    hp_before INTEGER NOT NULL,
    hp_after INTEGER NOT NULL,
    stamina_before INTEGER NOT NULL,
    stamina_after INTEGER NOT NULL,
    momentum_before INTEGER NOT NULL,
    momentum_after INTEGER NOT NULL,
    is_optimal_choice BOOLEAN NOT NULL,
    optimal_action TEXT NOT NULL,
    optimal_target_zone TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (match_id, turn, player_id)
);

CREATE INDEX IF NOT EXISTS telemetry_match_turns_player_idx ON telemetry_match_turns (player_id, created_at DESC);

CREATE TABLE IF NOT EXISTS telemetry_match_summaries (
    match_id UUID PRIMARY KEY REFERENCES matches(id) ON DELETE CASCADE,
    season_id UUID NOT NULL REFERENCES seasons(id) ON DELETE CASCADE,
    player1_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    player2_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    winner_id UUID NULL REFERENCES players(id) ON DELETE SET NULL,
    result_type TEXT NOT NULL,
    turn_count INTEGER NOT NULL CHECK (turn_count >= 0),
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    avg_decision_ms_p1 DOUBLE PRECISION NOT NULL,
    avg_decision_ms_p2 DOUBLE PRECISION NOT NULL,
    avg_stamina_p1 DOUBLE PRECISION NOT NULL,
    avg_stamina_p2 DOUBLE PRECISION NOT NULL,
    avg_momentum_p1 DOUBLE PRECISION NOT NULL,
    avg_momentum_p2 DOUBLE PRECISION NOT NULL,
    max_combo_p1 INTEGER NOT NULL CHECK (max_combo_p1 >= 0),
    max_combo_p2 INTEGER NOT NULL CHECK (max_combo_p2 >= 0),
    stun_events INTEGER NOT NULL CHECK (stun_events >= 0),
    submission_attempts INTEGER NOT NULL CHECK (submission_attempts >= 0),
    queue_wait_ms_p1 BIGINT NOT NULL CHECK (queue_wait_ms_p1 >= 0),
    queue_wait_ms_p2 BIGINT NOT NULL CHECK (queue_wait_ms_p2 >= 0),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_match_summaries_season_idx ON telemetry_match_summaries (season_id, created_at DESC);

CREATE TABLE IF NOT EXISTS anti_bot_config (
    key TEXT PRIMARY KEY,
    value_numeric DOUBLE PRECISION NULL,
    value_text TEXT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (value_numeric IS NOT NULL OR value_text IS NOT NULL)
);

INSERT INTO anti_bot_config (key, value_numeric, value_text, updated_at) VALUES
    ('min_decisions', 12, NULL, now()),
    ('max_mean_decision_ms', 180, NULL, now()),
    ('min_decision_variance_ms2', 2500, NULL, now()),
    ('min_optimal_pick_rate', 0.82, NULL, now()),
    ('suspicion_score_threshold', 0.75, NULL, now()),
    ('optimality_epsilon', 0.05, NULL, now())
ON CONFLICT (key) DO NOTHING;

CREATE TABLE IF NOT EXISTS anti_bot_flags (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    season_id UUID NOT NULL REFERENCES seasons(id) ON DELETE CASCADE,
    match_id UUID NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    decision_count INTEGER NOT NULL CHECK (decision_count >= 0),
    mean_decision_ms DOUBLE PRECISION NOT NULL,
    decision_variance_ms2 DOUBLE PRECISION NOT NULL,
    optimal_pick_rate DOUBLE PRECISION NOT NULL,
    suspicion_score DOUBLE PRECISION NOT NULL,
    flagged BOOLEAN NOT NULL,
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS anti_bot_flags_player_created_idx ON anti_bot_flags (player_id, created_at DESC);

CREATE TABLE IF NOT EXISTS telemetry_session_events (
    id UUID PRIMARY KEY,
    player_id UUID NULL REFERENCES players(id) ON DELETE SET NULL,
    handle TEXT NOT NULL,
    remote_addr_hash TEXT NOT NULL,
    event_type TEXT NOT NULL,
    detail_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_session_events_created_idx ON telemetry_session_events (created_at DESC);

CREATE TABLE IF NOT EXISTS telemetry_queue_events (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    event_type TEXT NOT NULL CHECK (event_type IN ('join', 'leave', 'timeout', 'matched')),
    queue_wait_ms BIGINT NOT NULL CHECK (queue_wait_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_queue_events_player_created_idx ON telemetry_queue_events (player_id, created_at DESC);

CREATE TABLE IF NOT EXISTS telemetry_spectator_events (
    id UUID PRIMARY KEY,
    spectator_player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    target_player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    match_id UUID NULL REFERENCES matches(id) ON DELETE SET NULL,
    event_type TEXT NOT NULL CHECK (event_type IN ('watch_requested', 'watch_attached', 'watch_timeout', 'watch_ended', 'watch_rejected')),
    wait_ms BIGINT NOT NULL CHECK (wait_ms >= 0),
    detail_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_spectator_events_spectator_created_idx
    ON telemetry_spectator_events (spectator_player_id, created_at DESC);

-- +twl Down
DROP INDEX IF EXISTS telemetry_spectator_events_spectator_created_idx;
DROP TABLE IF EXISTS telemetry_spectator_events;

DROP INDEX IF EXISTS telemetry_queue_events_player_created_idx;
DROP TABLE IF EXISTS telemetry_queue_events;

DROP INDEX IF EXISTS telemetry_session_events_created_idx;
DROP TABLE IF EXISTS telemetry_session_events;

DROP INDEX IF EXISTS anti_bot_flags_player_created_idx;
DROP TABLE IF EXISTS anti_bot_flags;

DROP TABLE IF EXISTS anti_bot_config;

DROP INDEX IF EXISTS telemetry_match_summaries_season_idx;
DROP TABLE IF EXISTS telemetry_match_summaries;

DROP INDEX IF EXISTS telemetry_match_turns_player_idx;
DROP TABLE IF EXISTS telemetry_match_turns;

DROP INDEX IF EXISTS tutorial_runs_player_created_idx;
DROP TABLE IF EXISTS tutorial_runs;

DROP TABLE IF EXISTS player_profiles;
