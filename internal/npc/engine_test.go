package npc

import (
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
)

func TestProbabilisticEngineDeterministicForSameSeed(t *testing.T) {
	e := NewProbabilisticEngine()
	state := buildState()

	rngA := engine.NewDeterministicRNG(777)
	rngB := engine.NewDeterministicRNG(777)

	decA, err := e.Decide(state, "p1", rngA)
	if err != nil {
		t.Fatalf("decide A: %v", err)
	}
	decB, err := e.Decide(state, "p1", rngB)
	if err != nil {
		t.Fatalf("decide B: %v", err)
	}

	if decA != decB {
		t.Fatalf("decisions differ for same seed/state: A=%+v B=%+v", decA, decB)
	}
}

func TestProbabilisticEngineStunnedReturnsNone(t *testing.T) {
	e := NewProbabilisticEngine()
	state := buildState()
	state.P1.State = combat.StateStunned
	state.P1.Effects.StunnedFor = 1

	dec, err := e.Decide(state, "p1", engine.NewDeterministicRNG(42))
	if err != nil {
		t.Fatalf("decide: %v", err)
	}
	if dec.Action != combat.ActionNone {
		t.Fatalf("action = %s, want None", dec.Action)
	}
	if dec.Target != combat.ZoneTorso {
		t.Fatalf("target = %s, want Torso", dec.Target)
	}
}

func TestProbabilisticEngineAlwaysReturnsValidActionAndZone(t *testing.T) {
	e := NewProbabilisticEngine()
	state := buildState()

	for i := uint64(1); i <= 200; i++ {
		dec, err := e.Decide(state, "p1", engine.NewDeterministicRNG(i))
		if err != nil {
			t.Fatalf("seed %d decide: %v", i, err)
		}
		if !isValidAction(dec.Action) {
			t.Fatalf("seed %d invalid action %s", i, dec.Action)
		}
		if !isValidZone(dec.Target) {
			t.Fatalf("seed %d invalid zone %s", i, dec.Target)
		}
	}
}

func buildState() combat.MatchState {
	p1, _ := combat.NewFighter("p1", combat.ArchetypeBalanced)
	p2, _ := combat.NewFighter("p2", combat.ArchetypeTechnician)
	p1.Stamina = 70
	p1.Momentum = 12
	p2.Stamina = 48
	p2.Momentum = 8

	return combat.MatchState{
		Turn:        5,
		P1:          p1,
		P2:          p2,
		CombatState: combat.StateNeutral,
	}
}

func isValidAction(a combat.Action) bool {
	switch a {
	case combat.ActionStrike,
		combat.ActionGrapple,
		combat.ActionBlock,
		combat.ActionDodge,
		combat.ActionCounter,
		combat.ActionFeint,
		combat.ActionBreak:
		return true
	default:
		return false
	}
}

func isValidZone(z combat.Zone) bool {
	switch z {
	case combat.ZoneHead, combat.ZoneTorso, combat.ZoneLegs:
		return true
	default:
		return false
	}
}
