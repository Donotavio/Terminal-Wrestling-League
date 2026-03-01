-- +twl Up
CREATE TABLE IF NOT EXISTS schema_migrations (
    version BIGINT PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    checksum TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS players (
    id UUID PRIMARY KEY,
    handle TEXT NOT NULL UNIQUE,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE TABLE IF NOT EXISTS seasons (
    id UUID PRIMARY KEY,
    number INTEGER NOT NULL UNIQUE,
    starts_at TIMESTAMPTZ NOT NULL,
    ends_at TIMESTAMPTZ NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('active', 'closed')),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    CHECK (starts_at < ends_at)
);

CREATE UNIQUE INDEX IF NOT EXISTS seasons_single_active_idx ON seasons ((status)) WHERE status = 'active';

CREATE TABLE IF NOT EXISTS matches (
    id UUID PRIMARY KEY,
    season_id UUID NOT NULL REFERENCES seasons(id),
    player1_id UUID NOT NULL REFERENCES players(id),
    player2_id UUID NOT NULL REFERENCES players(id),
    winner_id UUID NULL REFERENCES players(id),
    result_type TEXT NOT NULL CHECK (result_type IN ('ko', 'submission', 'abandon', 'draw')),
    started_at TIMESTAMPTZ NOT NULL,
    ended_at TIMESTAMPTZ NOT NULL,
    duration_ms INTEGER NOT NULL CHECK (duration_ms >= 0),
    created_at TIMESTAMPTZ NOT NULL,
    CHECK (player1_id <> player2_id),
    CHECK (ended_at >= started_at)
);

CREATE INDEX IF NOT EXISTS matches_season_started_idx ON matches (season_id, started_at DESC);
CREATE INDEX IF NOT EXISTS matches_player1_idx ON matches (player1_id);
CREATE INDEX IF NOT EXISTS matches_player2_idx ON matches (player2_id);

CREATE TABLE IF NOT EXISTS match_results (
    match_id UUID NOT NULL REFERENCES matches(id) ON DELETE CASCADE,
    player_id UUID NOT NULL REFERENCES players(id),
    opponent_id UUID NOT NULL REFERENCES players(id),
    score NUMERIC(2,1) NOT NULL CHECK (score IN (0.0, 0.5, 1.0)),
    rating_before DOUBLE PRECISION NOT NULL,
    rd_before DOUBLE PRECISION NOT NULL,
    sigma_before DOUBLE PRECISION NOT NULL,
    rating_after DOUBLE PRECISION NOT NULL,
    rd_after DOUBLE PRECISION NOT NULL,
    sigma_after DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (match_id, player_id),
    CHECK (player_id <> opponent_id)
);

CREATE INDEX IF NOT EXISTS match_results_player_idx ON match_results (player_id, created_at DESC);

CREATE TABLE IF NOT EXISTS player_ratings (
    player_id UUID NOT NULL REFERENCES players(id),
    season_id UUID NOT NULL REFERENCES seasons(id),
    rating DOUBLE PRECISION NOT NULL,
    rd DOUBLE PRECISION NOT NULL,
    sigma DOUBLE PRECISION NOT NULL,
    last_match_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (player_id, season_id)
);

CREATE INDEX IF NOT EXISTS player_ratings_season_rating_idx ON player_ratings (season_id, rating DESC);

-- +twl Down
DROP INDEX IF EXISTS player_ratings_season_rating_idx;
DROP TABLE IF EXISTS player_ratings;

DROP INDEX IF EXISTS match_results_player_idx;
DROP TABLE IF EXISTS match_results;

DROP INDEX IF EXISTS matches_player2_idx;
DROP INDEX IF EXISTS matches_player1_idx;
DROP INDEX IF EXISTS matches_season_started_idx;
DROP TABLE IF EXISTS matches;

DROP INDEX IF EXISTS seasons_single_active_idx;
DROP TABLE IF EXISTS seasons;

DROP TABLE IF EXISTS players;
DROP TABLE IF EXISTS schema_migrations;
