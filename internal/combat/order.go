package combat

import (
	"fmt"
	"sort"
)

// CanonicalizeInputs returns exactly one input per fighter in fixed player_id order.
func CanonicalizeInputs(state MatchState, inputs []TurnInput) ([]TurnInput, error) {
	byPlayer := make(map[string]TurnInput, len(inputs))
	for _, in := range inputs {
		if in.PlayerID == "" {
			return nil, fmt.Errorf("empty player id in input")
		}
		if _, exists := byPlayer[in.PlayerID]; exists {
			return nil, fmt.Errorf("duplicate input for player %q", in.PlayerID)
		}
		byPlayer[in.PlayerID] = in
	}

	ids := []string{state.P1.PlayerID, state.P2.PlayerID}
	if ids[0] == "" || ids[1] == "" {
		return nil, fmt.Errorf("match state has empty player id")
	}
	if ids[0] == ids[1] {
		return nil, fmt.Errorf("match state has duplicate player id: %q", ids[0])
	}
	sort.Strings(ids)

	ordered := make([]TurnInput, 0, 2)
	for _, id := range ids {
		in, ok := byPlayer[id]
		if !ok {
			in = TurnInput{PlayerID: id, Action: ActionNone, Target: ZoneTorso}
		}
		if in.Target != ZoneHead && in.Target != ZoneTorso && in.Target != ZoneLegs {
			in.Target = ZoneTorso
		}
		ordered = append(ordered, in)
	}
	return ordered, nil
}
