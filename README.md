# Terminal Wrestling League

Terminal Wrestling League is an authoritative PvP wrestling game that runs over SSH.
The server owns the match state, resolves combat deterministically, and streams textual
frames to connected players.

Current implemented milestones:

- M1: deterministic combat core
- M2: ranking and PostgreSQL storage
- M3: lobby/matchmaking and SSH runtime
- M4: replay persistence, animation renderer, deterministic NPC takeover
- M5: persistent telemetry, anti-bot scoring, tutorial gate, spectator mode

## Core Architecture

Primary package layout:

- `/cmd/server`: executable entrypoint, env/config loading, SSH server wiring
- `/internal/engine`: deterministic RNG and combat simulator orchestration
- `/internal/combat`: combat types, rules, turn ordering, deterministic resolver
- `/internal/animation`: ASCII framebuffer renderer and delta frame generation
- `/internal/lobby`: in-memory sessions and FIFO queue management
- `/internal/matchmaking`: queue pairing, authoritative match loop, tutorial and spectator orchestration
- `/internal/npc`: deterministic NPC input strategy
- `/internal/player`: session and command/frame contracts
- `/internal/ranking`: Glicko-2 rating service
- `/internal/replay`: replay loading and deterministic re-execution support
- `/internal/storage`: PostgreSQL pool, migrations, SQL repositories
- `/internal/telemetry`: in-memory metrics, SQL telemetry writer, anti-bot metrics
- `/internal/spectator`: watcher hub for read-only match frame fanout

## Determinism Guarantees

The project enforces deterministic combat behavior:

- RNG is seeded per match and deterministic.
- Turn resolution order is fixed by `player_id` canonicalization.
- Combat formulas use integer arithmetic for authoritative logic.
- Replay data stores canonical inputs plus checksums.
- The same seed, initial state, and ordered inputs must produce identical outcomes.

## Feature Set

- Authoritative queue and matchmaking over SSH
- PvP combat with server-side turn collection and resolution
- Deterministic NPC takeover after timeout streak or disconnect
- Replay persistence (`match_replays`, `match_replay_turns`)
- Persistent telemetry (session, queue, turn, match summary, spectator)
- Anti-bot scoring and flag persistence with configurable thresholds
- Mandatory first-session tutorial gate before queue unlock
- Optional tutorial replay command (`tutorial retry`)
- Spectator attach by handle (`watch <handle>`) for active PvP matches

## Requirements

- Go `1.26+`
- PostgreSQL (accessible by server runtime)
- SSH client for gameplay testing

## Configuration

Environment variables:

| Variable | Required | Default | Description |
|---|---|---|---|
| `TWL_DATABASE_URL` | Yes | - | PostgreSQL DSN used by pgx pool |
| `TWL_SSH_USERS` | Yes | - | Comma-separated credentials (`user:pass,user2:pass2`) |
| `TWL_SSH_ADDR` | No | `:2222` | SSH server bind address |
| `TWL_QUEUE_TIMEOUT_SEC` | No | `45` | Queue timeout (seconds) |
| `TWL_TURN_TIMEOUT_SEC` | No | `5` | Per-turn input timeout (seconds) |
| `TWL_MAX_TURNS` | No | `120` | Max turns before draw |
| `TWL_WATCH_WAIT_TIMEOUT_SEC` | No | `120` | Max wait to attach spectator stream |
| `TWL_SPECTATOR_MAX_PER_MATCH` | No | `20` | Spectator capacity per active match |

## Local Setup and Run

1. Download dependencies:

```bash
go mod download
```

2. Configure environment (example):

```bash
export TWL_DATABASE_URL='postgres://postgres:postgres@localhost:5432/twl?sslmode=disable'
export TWL_SSH_USERS='alice:secret,bob:secret'
export TWL_SSH_ADDR=':2222'
```

3. Run server:

```bash
go run ./cmd/server
```

4. Connect with SSH:

```bash
ssh alice@127.0.0.1 -p 2222
```

## Gameplay Command Reference

- `q`: join queue
- `l`: leave queue
- `s`: lobby snapshot
- `a <action> <zone>`: send turn action
- `watch <handle>`: attach as spectator to target active PvP match
- `tutorial retry`: rerun tutorial flow
- `help`: show command summary
- `quit` or `exit`: close session

Action values:

- `strike`, `grapple`, `block`, `dodge`, `counter`, `feint`, `break`

Target zones:

- `head`, `torso`, `legs`

## Database and Migrations

- SQL-first migrations are stored in `db/migrations/`.
- Migration files use `-- +twl Up` / `-- +twl Down` sections.
- Applied migration versions are tracked in `schema_migrations`.
- Startup applies pending migrations automatically in order.

## Testing and Quality Checks

Run before shipping changes:

```bash
go test ./...
go vet ./...
```

## Security Reporting

For vulnerability reporting and coordinated disclosure process, read
[SECURITY.md](SECURITY.md).

## License

This project is licensed under the GNU General Public License v3.0.
See [LICENSE](LICENSE).

## Additional Documentation

- [Combat determinism notes](docs/combat-determinism.md)
- [M2 ranking and storage notes](docs/m2-ranking-storage.md)
- [M3 SSH protocol and runtime configuration](docs/m3-ssh-protocol.md)
- [M4 replay persistence and animation/NPC notes](docs/m4-replay-persistence.md)
- [M5 telemetry, anti-bot, tutorial and spectator notes](docs/m5-telemetry-antibot-tutorial-spectator.md)
