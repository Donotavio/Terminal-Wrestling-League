# M2 Ranking + Storage Notes

## Scope

M2 introduces:

- Glicko-2 ranking service (`internal/ranking`)
- PostgreSQL storage layer with `pgxpool` (`internal/storage`)
- SQL-first migrations with version tracking (`db/migrations`)

Replay payload persistence is intentionally out of scope for M2.

## Glicko-2 defaults

- `Tau = 0.5`
- `Epsilon = 1e-6`
- Default rating: `R=1500`, `RD=350`, `Sigma=0.06`
- Update cadence: one update per finished match.
- Scores: win `1.0`, draw `0.5`, loss `0.0`.

## Season lifecycle (UTC)

- Active season duration: 30 days.
- On finalize-match write path:
  - If no active season exists, create season #1.
  - If active season expired (`now >= ends_at`), close it and create next season.
- Rating bootstrap for a new season is lazy:
  - If previous season rating exists:
    - `new_rating = 0.75*old + 0.25*1500`
    - `new_rd = min(350, old_rd + 30)`
    - `new_sigma = old_sigma`
  - Otherwise use default rating.

## Transactional finalize-match

`FinalizeMatch` runs in one database transaction:

1. Ensure active season.
2. Get/create player ratings for active season.
3. Update both ratings via Glicko-2.
4. Insert `matches` row.
5. Insert two `match_results` rows.
6. Upsert both `player_ratings` rows.

If any step fails, all changes are rolled back.

## Migrations format

Files are in `db/migrations/000X_name.sql` with markers:

- `-- +twl Up`
- `-- +twl Down`

M2 applies only `Up` blocks automatically and tracks applied versions in `schema_migrations`.
