# M6 Navigation + Onboarding UX

## Scope

M6 adds a hybrid CLI/menu layer on top of existing SSH commands to reduce time from login to first PvP match.

Included:

- New navigation commands: `menu`, `status`, `play` (alias for queue join).
- Numeric menu shortcuts by state (`tutorial`, `lobby`, `queue`, `match`).
- Automatic `status + menu` refresh after mutable commands (`play/q`, `l`, `npc`, `watch`, `tutorial retry`).
- Persistent navigation telemetry table `telemetry_navigation_events`.
- Matchmaking navigation events for `queue_matched`, `practice_started`, and `pvp_started`.

Excluded:

- Social discovery UX (`watch list`, active match browser).
- Ranking/history commands.
- Combat balance or determinism changes.

## Commands and states

State model:

- `tutorial`
- `lobby`
- `queue`
- `match`
- `spectator` (reserved for read-only stream flows)

Shortcuts:

- Tutorial: `1=tutorial retry`, `2=help`, `3=quit`
- Lobby: `1=play`, `2=npc`, `3=status`, `4=help`, `5=quit`
- Queue: `1=l`, `2=status`, `3=help`, `4=quit`
- Match: `1=action hint`

## Database

Migration: `db/migrations/0004_m6_navigation_ux.sql`

Table:

- `telemetry_navigation_events`

Key fields:

- `player_id`
- `session_id` (nullable UUID)
- `state`
- `event_type`
- `source` (`system`, `command`, `menu`)
- `option_key` (normalized shortcut key only)
- `detail_json`
- `created_at`

## Success metric

Primary metric: median time from `session_started` to `pvp_started`.

Example query:

```sql
WITH starts AS (
  SELECT player_id, session_id, MIN(created_at) AS started_at
  FROM telemetry_navigation_events
  WHERE event_type = 'session_started'
  GROUP BY player_id, session_id
),
pvp AS (
  SELECT player_id, MIN(created_at) AS pvp_at
  FROM telemetry_navigation_events
  WHERE event_type = 'pvp_started'
  GROUP BY player_id
)
SELECT
  percentile_cont(0.5) WITHIN GROUP (ORDER BY EXTRACT(EPOCH FROM (pvp.pvp_at - starts.started_at))) AS median_seconds
FROM starts
JOIN pvp ON pvp.player_id = starts.player_id
WHERE pvp.pvp_at >= starts.started_at;
```

## Determinism and safety

- No changes to combat resolver formulas, RNG semantics, or replay canonicalization.
- Navigation telemetry is best-effort and does not alter authoritative match resolution.
- No raw input payload is persisted in navigation telemetry.
