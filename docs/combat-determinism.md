# Combat Determinism Notes (M1)

This document defines deterministic invariants for the Terminal Wrestling League combat core.

## 1. Turn resolution

- Turn model is simultaneous input collection and deterministic resolution.
- Inputs are canonicalized by fixed lexicographic `player_id` order.
- Exactly one input per fighter is resolved each turn; missing input becomes `ActionNone`.
- Same initial state + same ordered inputs + same seed must produce the same `TurnResult`.

## 2. RNG

- RNG is a deterministic `xorshift64*` implementation.
- RNG state is fully seed-driven and snapshotable.
- Every random roll used by combat contributes to a `RollHash` checksum.

## 3. Integer math and rounding

All combat formulas use integer math only (no floating-point logic).

- Stamina cost:
  - `RealCost = floor(BaseCost * (100 - Endurance*3) / 100)`
  - minimum cost is `1` for non-zero base actions.
- Regen:
  - `Regen = floor((50 + Endurance*8) / 10)`
- Success chance:
  - `ChanceFinalBPS = clamp(1000, 9000, 5000 + atk*300 - def*200 + momentum*15 - fatiguePenalty)`
- Damage:
  - `DamageFinal = floor(BaseDamage * (100 + Momentum) / 100)`
  - combo bonus is applied as `floor(damage * 110 / 100)`.
- Stun chance:
  - `StunChanceBPS = clamp(0, 9500, 1000 + headBonus + lowStaminaBonus + momentumDiff*50)`

## 4. Effect timing invariants

- Stun duration is one full future turn:
  - if applied in turn `T`, fighter skips actions in turn `T+1`.
- Torso and legs debuffs last exactly two future turns.
- Effect counters are consumed from turn-start snapshots so newly applied effects are not consumed in the same turn.

## 5. Combo invariants

- Combo window is exactly one turn.
- Combo activates only when:
  - previous turn had a successful offensive hit,
  - current action is offensive,
  - current stamina is greater than `30`.
- Combo grants `+5` extra momentum and `+10%` extra damage.

## 6. Checksums

Every `TurnResult` contains:

- `StateHash`: FNV-1a hash of deterministic match state fields.
- `RollHash`: FNV-1a hash of random rolls consumed during the turn.

These checksums are used by tests to detect divergence across runs and replay paths.
