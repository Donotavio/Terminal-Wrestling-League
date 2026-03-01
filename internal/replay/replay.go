package replay

import (
	"fmt"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
)

// InputEvent stores player inputs and timestamp metadata for one turn.
type InputEvent struct {
	Turn       int
	RelativeMS int64
	Inputs     []combat.TurnInput
}

// Record is the deterministic replay payload.
type Record struct {
	Seed       uint64
	Archetypes map[string]combat.Archetype
	Initial    combat.MatchState
	Inputs     []InputEvent
}

// Player replays a deterministic combat record.
type Player interface {
	Replay(record Record) ([]combat.TurnResult, error)
}

// Runner executes deterministic replays with a combat resolver.
type Runner struct {
	resolver combat.Resolver
}

func NewRunner(resolver combat.Resolver) *Runner {
	return &Runner{resolver: resolver}
}

func (r *Runner) Replay(record Record) ([]combat.TurnResult, error) {
	if r.resolver == nil {
		return nil, fmt.Errorf("nil resolver")
	}
	if record.Initial.P1.PlayerID == "" || record.Initial.P2.PlayerID == "" {
		return nil, fmt.Errorf("record initial state missing player ids")
	}

	sim := engine.NewCombatSimulator(record.Initial, r.resolver, record.Seed)
	results := make([]combat.TurnResult, 0, len(record.Inputs))
	for i, ev := range record.Inputs {
		expectedTurn := sim.State().Turn + 1
		if ev.Turn != 0 && ev.Turn != expectedTurn {
			return nil, fmt.Errorf("input event %d has turn %d, expected %d", i, ev.Turn, expectedTurn)
		}

		res, err := sim.Step(ev.Inputs)
		if err != nil {
			return nil, fmt.Errorf("resolve turn %d: %w", expectedTurn, err)
		}
		results = append(results, res)
	}
	return results, nil
}
