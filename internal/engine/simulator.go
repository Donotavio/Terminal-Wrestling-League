package engine

import "github.com/Donotavio/Terminal-Wrestling-League/internal/combat"

// Simulator steps one deterministic match forward.
type Simulator interface {
	Step(inputs []combat.TurnInput) (combat.TurnResult, error)
	State() combat.MatchState
}

// CombatSimulator wraps combat resolution with local state and RNG.
type CombatSimulator struct {
	state    combat.MatchState
	resolver combat.Resolver
	rng      RNG
}

func NewCombatSimulator(initial combat.MatchState, resolver combat.Resolver, seed uint64) *CombatSimulator {
	rng := NewDeterministicRNG(seed)
	initial.RNGState = rng.Snapshot()
	return &CombatSimulator{
		state:    initial,
		resolver: resolver,
		rng:      rng,
	}
}

func (s *CombatSimulator) Step(inputs []combat.TurnInput) (combat.TurnResult, error) {
	result, err := s.resolver.ResolveTurn(s.state, inputs, s.rng)
	if err != nil {
		return combat.TurnResult{}, err
	}
	result.Next.RNGState = s.rng.Snapshot()
	s.state = result.Next
	return result, nil
}

func (s *CombatSimulator) State() combat.MatchState {
	return s.state
}
