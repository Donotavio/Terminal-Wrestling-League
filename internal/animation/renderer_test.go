package animation

import (
	"reflect"
	"strings"
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
)

func TestASCIIRendererProducesCinematicKeyframes(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(1, 100, 100, combat.ActionStrike, combat.ActionDodge, nil, ""))

	if len(frame.Keyframes) < 4 {
		t.Fatalf("keyframes len = %d, want >= 4", len(frame.Keyframes))
	}
	if len(frame.Delta) != 0 {
		t.Fatalf("delta should be empty in cinematic mode, got %v", frame.Delta)
	}
	if len(frame.Full) == 0 {
		t.Fatalf("full frame should contain final keyframe")
	}
	for i, keyframe := range frame.Keyframes {
		if len(keyframe) == 0 {
			t.Fatalf("keyframe %d is empty", i)
		}
		if !strings.Contains(keyframe[0], "Turn 1") {
			t.Fatalf("keyframe %d header = %q, want Turn 1", i, keyframe[0])
		}
	}
}

func TestASCIIRendererDeterministicForSameInput(t *testing.T) {
	r := NewASCIIRenderer()
	turn := buildTurnResult(2, 83, 64, combat.ActionFeint, combat.ActionBlock, []combat.Event{
		{
			Type:    combat.EventDamageApplied,
			Detail:  "damage",
			Success: true,
			Action:  combat.ActionFeint,
			Value:   9,
		},
	}, "")

	first := r.Render("alice", "bob", turn)
	second := r.Render("alice", "bob", turn)

	if !reflect.DeepEqual(first.Keyframes, second.Keyframes) {
		t.Fatalf("keyframes are not deterministic")
	}
	if first.Summary != second.Summary {
		t.Fatalf("summary mismatch: %q vs %q", first.Summary, second.Summary)
	}
	if !reflect.DeepEqual(first.Effects, second.Effects) {
		t.Fatalf("effects mismatch: %v vs %v", first.Effects, second.Effects)
	}
}

func TestASCIIRendererOmitsLogLinesAndKeepsSummary(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(3, 88, 71, combat.ActionCounter, combat.ActionGrapple, []combat.Event{
		{
			Type:    combat.EventDamageApplied,
			Detail:  "damage",
			Success: true,
			Action:  combat.ActionCounter,
			Value:   18,
		},
	}, ""))

	joined := strings.Join(flattenKeyframes(frame.Keyframes), "\n")
	if strings.Contains(joined, "event:") {
		t.Fatalf("combat logs leaked into cinematic output:\n%s", joined)
	}
	if strings.Contains(joined, "[Δ") {
		t.Fatalf("delta markers leaked into cinematic output:\n%s", joined)
	}
	if !strings.Contains(frame.Summary, "Exchange:") {
		t.Fatalf("unexpected summary: %q", frame.Summary)
	}
}

func TestASCIIRendererAppendsSlowmoOnFinish(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(4, 0, 29, combat.ActionBlock, combat.ActionGrapple, []combat.Event{
		{
			Type:    combat.EventDamageApplied,
			Detail:  "damage",
			Success: true,
			Action:  combat.ActionGrapple,
			Value:   21,
		},
		{
			Type:    combat.EventMatchFinished,
			Detail:  "ko",
			Success: true,
		},
	}, "KO"))

	if len(frame.Keyframes) < 5 {
		t.Fatalf("keyframes len = %d, want >= 5 for slowmo", len(frame.Keyframes))
	}
	last := frame.Keyframes[len(frame.Keyframes)-1]
	if len(last) == 0 || !strings.Contains(last[0], "SLOWMO") {
		t.Fatalf("last keyframe missing slowmo marker: %v", last)
	}
}

func TestASCIIRendererAddsImpactOverlays(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(6, 70, 40, combat.ActionGrapple, combat.ActionStrike, []combat.Event{
		{
			Type:     combat.EventDamageApplied,
			Detail:   "damage",
			Success:  true,
			Action:   combat.ActionGrapple,
			Value:    24,
			TargetID: "p2",
		},
		{
			Type:    combat.EventStatusApplied,
			Detail:  "stunned",
			Success: true,
		},
	}, ""))

	joined := strings.Join(flattenKeyframes(frame.Keyframes), "\n")
	if !strings.Contains(joined, "HITSTOP") {
		t.Fatalf("expected hitstop overlay, got:\n%s", joined)
	}
	if !strings.Contains(joined, "KRAK") && !strings.Contains(joined, "BOOM") {
		t.Fatalf("expected impact onomatopoeia, got:\n%s", joined)
	}
}

func TestASCIIRendererInfersEffectsDeterministically(t *testing.T) {
	r := NewASCIIRenderer()
	frame := r.Render("alice", "bob", buildTurnResult(5, 70, 40, combat.ActionGrapple, combat.ActionStrike, []combat.Event{
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

func buildTurnResult(
	turn int,
	p1HP int,
	p2HP int,
	p1Action combat.Action,
	p2Action combat.Action,
	events []combat.Event,
	outcome string,
) combat.TurnResult {
	state := combat.MatchState{
		Turn: turn,
		P1: combat.FighterState{
			PlayerID:   "p1",
			HP:         p1HP,
			Stamina:    100,
			Momentum:   0,
			Archetype:  combat.ArchetypeBalanced,
			LastAction: p1Action,
		},
		P2: combat.FighterState{
			PlayerID:   "p2",
			HP:         p2HP,
			Stamina:    100,
			Momentum:   0,
			Archetype:  combat.ArchetypeBalanced,
			LastAction: p2Action,
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

func flattenKeyframes(keyframes [][]string) []string {
	lines := make([]string, 0, len(keyframes)*8)
	for _, keyframe := range keyframes {
		lines = append(lines, keyframe...)
	}
	return lines
}
