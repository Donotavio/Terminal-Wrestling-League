# M4 Replay + Animation + NPC (Deterministic)

## Scope

M4 finalizes deterministic replay integration and adds animation/NPC runtime behavior.

- Replay is written in the same transaction as match/rating finalization.
- Replay includes seed, initial match state, and per-turn inputs/checksums.
- Match frame rendering now uses an ASCII framebuffer renderer with delta output.
- NPC takeover is now deterministic for inactivity/disconnect recovery during a live match.
- No new SSH commands or HTTP endpoints are included in M4.

## Data model

M4 migration `0002_match_replays.sql` adds:

- `match_replays`
  - `match_id` (PK, FK -> `matches.id`)
  - `seed_text` (decimal `uint64` as text)
  - `initial_state_json` (`combat.MatchState`)
  - `turn_count`
  - `created_at`
- `match_replay_turns`
  - (`match_id`, `turn`) PK
  - `relative_ms`
  - `inputs_json` (`[]combat.TurnInput`)
  - `state_hash_text` and `roll_hash_text` (decimal `uint64` as text)
  - `created_at`

## Write path

`FinalizeMatch` now accepts optional `Replay *MatchReplayWrite`.

When present, the flow is:

1. Insert `matches`.
2. Insert replay header (`match_replays`) and turns (`match_replay_turns`).
3. Insert `match_results`.
4. Upsert `player_ratings`.
5. Commit transaction.

If any replay insert fails, the full transaction is rolled back.

## Matchmaking integration

During `runMatch`:

- Seed and initial state are captured before the turn loop.
- Turn inputs are canonicalized by `player_id` each turn.
- The match loop now supports per-player control mode (human or NPC).
- Renderer output is generated from `internal/animation` and sent as line-delta updates.
- Each turn appends replay row data:
  - turn number
  - relative milliseconds since match start
  - canonicalized inputs
  - turn checksums
- Replay payload is sent to `FinalizeMatch`.

## Animation renderer

`internal/animation` now provides:

- Full frame output (HUD + events + outcome).
- Delta frame output in format `"[Δ L<index>] <line>"`.
- Deterministic effect tags:
  - `hitstop`: at least one `damage_applied` event.
  - `shake`: high damage (>= 15) or match finish.
  - `knockback`: successful `grapple`/`counter` damage event.
  - `slowmo`: `match_finished` event.

## NPC takeover policy

`internal/npc` provides deterministic weighted decision logic tied to match seed.

- Two consecutive timeouts from a human player trigger NPC takeover in the same turn.
- Disconnect/quit during an active turn triggers immediate takeover in the same turn.
- After takeover, control does not return to the human player for that match.
- If NPC decision fails, fallback input is `ActionNone` (torso) and the match continues.

## Invariants

- Replay input order is stable (canonicalized by `player_id`).
- Stored checksums come from authoritative combat resolution (`TurnResult.Checksums`).
- Seed and hashes are text-encoded decimal `uint64` to avoid `BIGINT` overflow risk.
- NPC RNG streams are deterministic and derived from the match seed.
- Retention is currently indefinite (no TTL/cleanup in M4).
