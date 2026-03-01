# M5 Telemetry + Anti-bot + Tutorial + Spectator

## Scope

M5 adds persistent telemetry, anti-bot flagging, first-session tutorial gate, and read-only spectator streaming for active PvP matches.

Included in this milestone:

- PostgreSQL schema for telemetry, anti-bot config/flags, tutorial/profile tracking, and spectator events.
- SQL repositories and telemetry writer integration for M5 entities.
- Matchmaking persistence of per-turn telemetry, match summaries, and anti-bot evaluations.
- Mandatory first-session tutorial (one-time gate) plus optional replay via `tutorial retry`.
- Spectator mode via `watch <handle>` with wait timeout and per-match capacity limit.

Excluded from this milestone:

- Automatic punitive action on anti-bot flags.
- TTL/retention jobs for telemetry data.

## Database model

Migration: `db/migrations/0003_m5_telemetry_antibot_tutorial_spectator.sql`

Main tables:

- `player_profiles`
- `tutorial_runs`
- `telemetry_match_turns`
- `telemetry_match_summaries`
- `anti_bot_config`
- `anti_bot_flags`
- `telemetry_session_events`
- `telemetry_queue_events`
- `telemetry_spectator_events`

`anti_bot_config` is seeded with default thresholds:

- `min_decisions=12`
- `max_mean_decision_ms=180`
- `min_decision_variance_ms2=2500`
- `min_optimal_pick_rate=0.82`
- `suspicion_score_threshold=0.75`
- `optimality_epsilon=0.05`

## Tutorial first experience

Server flow:

1. Login resolves/creates `players` and `player_profiles`.
2. If `tutorial_completed=false`, tutorial starts automatically before regular shell loop.
3. Tutorial has two phases:
   - guided textual commands with explicit validation
   - deterministic short fight versus training NPC
4. Completion is marked after the first tutorial fight, independent of win/loss/draw.
5. `tutorial retry` reruns tutorial without re-locking PvP queue access.

## PvP telemetry and anti-bot

During authoritative PvP `runMatch`:

- A turn row is persisted for each player turn in `telemetry_match_turns`.
- A match summary row is persisted in `telemetry_match_summaries`.
- Human decision observations are evaluated against `anti_bot_config`.
- A row is always persisted in `anti_bot_flags` for analyzed players.

Anti-bot score:

- `score = 0.40*I(mean_ms <= max_mean) + 0.30*I(var_ms2 <= min_variance) + 0.30*I(opt_rate >= min_opt_rate)`
- `flagged = (decision_count >= min_decisions) AND (score >= suspicion_score_threshold)`

This milestone only flags and records telemetry; it does not auto-block players.

## Spectator mode

Command: `watch <handle>`

Behavior:

- Only attaches to active PvP matches (tutorial fights are not published to spectator hub).
- Waits up to configured timeout (default `120s`) for target handle to enter active PvP.
- Shell is blocked while waiting and while attached.
- Streams the same read-only frame payload broadcast by matchmaking.
- Enforces max spectators per match (default `20`).
- Persists events: `watch_requested`, `watch_attached`, `watch_timeout`, `watch_rejected`, `watch_ended`.

## Determinism considerations

- Combat resolution remains deterministic and independent from wall-clock randomness.
- Telemetry collection is side-effect persistence and does not alter canonical turn input resolution.
- Replay persistence remains canonical by `player_id`, preserving deterministic re-execution invariants.

