package npc

import (
	"fmt"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
)

// RandomSource is the deterministic RNG contract used by NPC decisions.
type RandomSource interface {
	NextInt(n int) int
}

// Engine decides deterministic NPC actions.
type Engine interface {
	Decide(state combat.MatchState, selfID string, rng RandomSource) (combat.TurnInput, error)
}

// ProbabilisticEngine computes weighted action and target choices.
type ProbabilisticEngine struct{}

func NewProbabilisticEngine() *ProbabilisticEngine {
	return &ProbabilisticEngine{}
}

func (e *ProbabilisticEngine) Decide(state combat.MatchState, selfID string, rng RandomSource) (combat.TurnInput, error) {
	if selfID == "" {
		return combat.TurnInput{}, fmt.Errorf("selfID is required")
	}
	if rng == nil {
		return combat.TurnInput{}, fmt.Errorf("rng is required")
	}

	self, opp, err := fightersByID(state, selfID)
	if err != nil {
		return combat.TurnInput{}, err
	}

	if self.State == combat.StateStunned || self.Effects.StunnedFor > 0 {
		return combat.TurnInput{
			PlayerID: self.PlayerID,
			Action:   combat.ActionNone,
			Target:   combat.ZoneTorso,
		}, nil
	}

	actionWeights := baseActionWeights(state.CombatState)
	adjustActionWeights(actionWeights, *self, *opp)
	action := chooseAction(actionWeights, rng)
	if action == combat.ActionNone {
		action = combat.ActionBlock
	}

	zoneWeights := baseZoneWeights(action)
	adjustZoneWeights(zoneWeights, action, *self, *opp)
	zone := chooseZone(zoneWeights, rng)

	return combat.TurnInput{
		PlayerID: self.PlayerID,
		Action:   action,
		Target:   zone,
	}, nil
}

type actionWeight struct {
	action combat.Action
	weight int
}

type zoneWeight struct {
	zone   combat.Zone
	weight int
}

func fightersByID(state combat.MatchState, selfID string) (*combat.FighterState, *combat.FighterState, error) {
	switch selfID {
	case state.P1.PlayerID:
		return &state.P1, &state.P2, nil
	case state.P2.PlayerID:
		return &state.P2, &state.P1, nil
	default:
		return nil, nil, fmt.Errorf("selfID %q not present in match", selfID)
	}
}

func baseActionWeights(s combat.State) []actionWeight {
	switch s {
	case combat.StateClinch:
		return []actionWeight{
			{action: combat.ActionStrike, weight: 20},
			{action: combat.ActionGrapple, weight: 30},
			{action: combat.ActionBlock, weight: 10},
			{action: combat.ActionDodge, weight: 8},
			{action: combat.ActionCounter, weight: 8},
			{action: combat.ActionFeint, weight: 4},
			{action: combat.ActionBreak, weight: 20},
		}
	case combat.StateGround:
		return []actionWeight{
			{action: combat.ActionStrike, weight: 12},
			{action: combat.ActionGrapple, weight: 32},
			{action: combat.ActionBlock, weight: 10},
			{action: combat.ActionDodge, weight: 8},
			{action: combat.ActionCounter, weight: 8},
			{action: combat.ActionFeint, weight: 6},
			{action: combat.ActionBreak, weight: 24},
		}
	case combat.StateDominant:
		return []actionWeight{
			{action: combat.ActionStrike, weight: 22},
			{action: combat.ActionGrapple, weight: 30},
			{action: combat.ActionBlock, weight: 10},
			{action: combat.ActionDodge, weight: 8},
			{action: combat.ActionCounter, weight: 10},
			{action: combat.ActionFeint, weight: 14},
			{action: combat.ActionBreak, weight: 6},
		}
	case combat.StateSubmissionAttempt:
		return []actionWeight{
			{action: combat.ActionStrike, weight: 12},
			{action: combat.ActionGrapple, weight: 20},
			{action: combat.ActionBlock, weight: 12},
			{action: combat.ActionDodge, weight: 8},
			{action: combat.ActionCounter, weight: 8},
			{action: combat.ActionFeint, weight: 10},
			{action: combat.ActionBreak, weight: 30},
		}
	default:
		return []actionWeight{
			{action: combat.ActionStrike, weight: 34},
			{action: combat.ActionGrapple, weight: 20},
			{action: combat.ActionBlock, weight: 14},
			{action: combat.ActionDodge, weight: 14},
			{action: combat.ActionCounter, weight: 8},
			{action: combat.ActionFeint, weight: 8},
			{action: combat.ActionBreak, weight: 2},
		}
	}
}

func adjustActionWeights(weights []actionWeight, self, opp combat.FighterState) {
	momentumDelta := self.Momentum - opp.Momentum

	for i := range weights {
		switch weights[i].action {
		case combat.ActionStrike, combat.ActionGrapple:
			if self.Stamina < 30 {
				weights[i].weight -= 8
			}
			if momentumDelta >= 20 {
				weights[i].weight += 8
			}
			if opp.Stamina < 25 {
				weights[i].weight += 6
			}
		case combat.ActionBlock, combat.ActionDodge:
			if self.Stamina < 30 {
				weights[i].weight += 10
			}
			if momentumDelta <= -15 {
				weights[i].weight += 8
			}
		case combat.ActionCounter:
			if momentumDelta <= -10 {
				weights[i].weight += 6
			}
		case combat.ActionBreak:
			if self.Stamina < 30 {
				weights[i].weight += 6
			}
			if self.State == combat.StateSubmissionAttempt || self.State == combat.StateGround || self.State == combat.StateClinch {
				weights[i].weight += 6
			}
		}

		if weights[i].weight < 1 {
			weights[i].weight = 1
		}
	}
}

func chooseAction(weights []actionWeight, rng RandomSource) combat.Action {
	total := 0
	for _, w := range weights {
		total += w.weight
	}
	if total <= 0 {
		return combat.ActionNone
	}
	roll := rng.NextInt(total)
	if roll < 0 {
		roll = 0
	}
	for _, w := range weights {
		if roll < w.weight {
			return w.action
		}
		roll -= w.weight
	}
	return weights[len(weights)-1].action
}

func baseZoneWeights(action combat.Action) []zoneWeight {
	switch action {
	case combat.ActionGrapple:
		return []zoneWeight{
			{zone: combat.ZoneHead, weight: 15},
			{zone: combat.ZoneTorso, weight: 65},
			{zone: combat.ZoneLegs, weight: 20},
		}
	case combat.ActionCounter:
		return []zoneWeight{
			{zone: combat.ZoneHead, weight: 35},
			{zone: combat.ZoneTorso, weight: 45},
			{zone: combat.ZoneLegs, weight: 20},
		}
	default:
		return []zoneWeight{
			{zone: combat.ZoneHead, weight: 30},
			{zone: combat.ZoneTorso, weight: 45},
			{zone: combat.ZoneLegs, weight: 25},
		}
	}
}

func adjustZoneWeights(weights []zoneWeight, action combat.Action, self, opp combat.FighterState) {
	momentumDelta := self.Momentum - opp.Momentum
	for i := range weights {
		switch weights[i].zone {
		case combat.ZoneHead:
			if momentumDelta >= 15 {
				weights[i].weight += 8
			}
			if opp.Stamina < 25 {
				weights[i].weight += 8
			}
		case combat.ZoneTorso:
			if action == combat.ActionGrapple {
				weights[i].weight += 10
			}
		case combat.ZoneLegs:
			if opp.Effects.LegsDodgePenaltyFor > 0 {
				weights[i].weight += 10
			}
			if opp.Stats.Agility >= 8 {
				weights[i].weight += 6
			}
		}
		if weights[i].weight < 1 {
			weights[i].weight = 1
		}
	}
}

func chooseZone(weights []zoneWeight, rng RandomSource) combat.Zone {
	total := 0
	for _, w := range weights {
		total += w.weight
	}
	if total <= 0 {
		return combat.ZoneTorso
	}
	roll := rng.NextInt(total)
	if roll < 0 {
		roll = 0
	}
	for _, w := range weights {
		if roll < w.weight {
			return w.zone
		}
		roll -= w.weight
	}
	return weights[len(weights)-1].zone
}
