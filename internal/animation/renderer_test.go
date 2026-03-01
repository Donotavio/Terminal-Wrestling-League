package animation

import (
	"strings"
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
)

func TestASCIIRendererFirstFrameProducesFullDelta(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(1, 100, 100, nil, ""))

	if len(frame.Full) == 0 {
		t.Fatalf("full frame is empty")
	}
	if len(frame.Delta) != len(frame.Full) {
		t.Fatalf("delta len = %d, want %d", len(frame.Delta), len(frame.Full))
	}
	for i, line := range frame.Delta {
		wantPrefix := "[Δ L"
		if !strings.HasPrefix(line, wantPrefix) {
			t.Fatalf("delta[%d] = %q, want prefix %q", i, line, wantPrefix)
		}
	}
}

func TestASCIIRendererDeltaOnlyIncludesChangedLines(t *testing.T) {
	r := NewASCIIRenderer()

	_ = r.Render("alice", "bob", buildTurnResult(1, 100, 100, []combat.Event{{
		Type:    combat.EventActionResolved,
		Detail:  "chance_roll",
		Success: true,
	}}, ""))

	frame := r.Render("alice", "bob", buildTurnResult(2, 85, 100, []combat.Event{{
		Type:    combat.EventDamageApplied,
		Detail:  "damage",
		Success: true,
		Action:  combat.ActionStrike,
		Value:   15,
	}}, ""))

	if len(frame.Delta) == 0 {
		t.Fatalf("expected changed lines in delta")
	}
	for _, line := range frame.Delta {
		if strings.Contains(line, "bob HP:100 ST:100 MO:0") {
			t.Fatalf("unchanged line should not appear in delta: %q", line)
		}
	}
}

func TestASCIIRendererInfersEffectsDeterministically(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(3, 70, 40, []combat.Event{
		{
			Type:    combat.EventDamageApplied,
			Detail:  "damage",
			Success: true,
			Action:  combat.ActionGrapple,
			Value:   18,
		},
		{
			Type:    combat.EventMatchFinished,
			Detail:  "ko",
			Success: true,
		},
	}, "KO"))

	want := []Effect{EffectHitstop, EffectShake, EffectKnockback, EffectSlowmo}
	if len(frame.Effects) != len(want) {
		t.Fatalf("effects len = %d, want %d (%v)", len(frame.Effects), len(want), frame.Effects)
	}
	for i := range want {
		if frame.Effects[i] != want[i] {
			t.Fatalf("effects[%d] = %s, want %s", i, frame.Effects[i], want[i])
		}
	}
}

func buildTurnResult(turn int, p1HP int, p2HP int, events []combat.Event, outcome string) combat.TurnResult {
	state := combat.MatchState{
		Turn: turn,
		P1: combat.FighterState{
			PlayerID:  "p1",
			HP:        p1HP,
			Stamina:   100,
			Momentum:  0,
			Archetype: combat.ArchetypeBalanced,
		},
		P2: combat.FighterState{
			PlayerID:  "p2",
			HP:        p2HP,
			Stamina:   100,
			Momentum:  0,
			Archetype: combat.ArchetypeBalanced,
		},
		CombatState: combat.StateNeutral,
	}
	if outcome != "" {
		state.Outcome = combat.MatchOutcome{Finished: true, WinnerID: "p1", Method: outcome}
	}

	return combat.TurnResult{
		Next:   state,
		Events: events,
	}
}
