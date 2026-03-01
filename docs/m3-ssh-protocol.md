# M3 SSH Protocol and Runtime Configuration

## Environment variables

Required:

- `TWL_DATABASE_URL`: PostgreSQL DSN used by `pgxpool`.
- `TWL_SSH_USERS`: comma-separated credentials in the format `user:pass,user2:pass2`.

Optional:

- `TWL_SSH_ADDR` (default `:2222`)
- `TWL_QUEUE_TIMEOUT_SEC` (default `45`)
- `TWL_TURN_TIMEOUT_SEC` (default `5`)
- `TWL_MAX_TURNS` (default `120`)
- `TWL_WATCH_WAIT_TIMEOUT_SEC` (default `120`)
- `TWL_SPECTATOR_MAX_PER_MATCH` (default `20`)

## Startup flow

1. Load config from environment.
2. Create PostgreSQL pool.
3. Apply SQL migrations from `db/migrations`.
4. Build ranking service and SQL repositories.
5. Initialize lobby and matchmaking services.
6. Start SSH server with username/password auth.

If `TWL_DATABASE_URL` is missing or invalid, startup fails.

## SSH commands

After login, users can run:

- `q`: join matchmaking queue
- `l`: leave queue
- `s`: show lobby snapshot
- `npc`: start an immediate practice match versus Coach NPC
- `a <action> <zone>`: send combat action
  - actions: `strike`, `grapple`, `block`, `dodge`, `counter`, `feint`, `break`
  - zones: `head`, `torso`, `legs`
- `watch <handle>`: wait and attach as spectator to target active PvP match (read-only)
- `tutorial retry`: rerun tutorial flow (optional after first completion)
- `help`: show command help
- `quit` / `exit`: close session

First-session gate:

- On first login (`tutorial_completed=false`), tutorial is started automatically before normal shell.
- Queue command `q` is blocked until tutorial completion.
- Tutorial completion is marked after first tutorial fight, regardless of match result.

## Match lifecycle

1. Two queued players are paired FIFO.
2. Server runs an authoritative combat loop:
   - each turn waits for player input until `TWL_TURN_TIMEOUT_SEC`
   - missing input becomes `ActionNone`
   - after 2 consecutive timeouts for one player, NPC takeover is activated for that slot
   - disconnect/quit during match activates immediate NPC takeover for that slot
   - once takeover happens, control stays with NPC until match end
3. Match ends by combat outcome or draw at `TWL_MAX_TURNS`.
4. Result is persisted through `FinalizeMatch` transaction.

## Frame streaming

- Combat frames are emitted as delta lines derived from an ASCII framebuffer.
- Delta line format: `"[Δ L<index>] <line>"`.
- Optional effect line can be emitted: `effects: hitstop,shake,knockback,slowmo`.
- Replay persistence still stores canonical combat inputs and checksums, independent of frame format.

## Rate limits

- Login by IP: token-bucket equivalent to 5/minute with burst 3.
- Queue join by player: 10/minute.
- Action submit by player: 30/minute.

When exceeded, command is ignored and a message is returned to client.

## Telemetry counters (in-memory)

Counters:

- `login_attempts`
- `login_rate_limited`
- `login_denied`
- `queue_join`
- `queue_timeout`
- `matches_started`
- `matches_finished`

Durations:

- `match_duration`

## Spectator runtime

- Spectator stream is read-only and mirrors authoritative match frame payloads.
- Only active PvP matches are exposed (tutorial matches are excluded).
- `watch <handle>` waits up to `TWL_WATCH_WAIT_TIMEOUT_SEC`.
- Per-match spectator cap is `TWL_SPECTATOR_MAX_PER_MATCH`.
