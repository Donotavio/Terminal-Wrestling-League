-- +twl Up
CREATE TABLE IF NOT EXISTS telemetry_navigation_events (
    id UUID PRIMARY KEY,
    player_id UUID NOT NULL REFERENCES players(id) ON DELETE CASCADE,
    session_id UUID NULL,
    state TEXT NOT NULL CHECK (state IN ('tutorial', 'lobby', 'queue', 'match', 'spectator')),
    event_type TEXT NOT NULL CHECK (
        event_type IN (
            'session_started',
            'menu_shown',
            'menu_selected',
            'status_shown',
            'help_shown',
            'command_invalid',
            'queue_joined',
            'queue_left',
            'queue_matched',
            'tutorial_started',
            'tutorial_completed',
            'practice_started',
            'pvp_started'
        )
    ),
    source TEXT NOT NULL CHECK (source IN ('system', 'command', 'menu')),
    option_key TEXT NULL,
    detail_json JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS telemetry_navigation_events_player_created_idx
    ON telemetry_navigation_events (player_id, created_at DESC);

CREATE INDEX IF NOT EXISTS telemetry_navigation_events_session_created_idx
    ON telemetry_navigation_events (session_id, created_at ASC);

CREATE INDEX IF NOT EXISTS telemetry_navigation_events_event_created_idx
    ON telemetry_navigation_events (event_type, created_at DESC);

-- +twl Down
DROP INDEX IF EXISTS telemetry_navigation_events_event_created_idx;
DROP INDEX IF EXISTS telemetry_navigation_events_session_created_idx;
DROP INDEX IF EXISTS telemetry_navigation_events_player_created_idx;
DROP TABLE IF EXISTS telemetry_navigation_events;
