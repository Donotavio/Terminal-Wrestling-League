package replay

import (
	"reflect"
	"testing"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
)

func mustFighter(t *testing.T, id string, a combat.Archetype) combat.FighterState {
	t.Helper()
	f, err := combat.NewFighter(id, a)
	if err != nil {
		t.Fatalf("NewFighter(%s): %v", id, err)
	}
	return f
}

func buildRecord(t *testing.T) Record {
	t.Helper()
	p1 := mustFighter(t, "alice", combat.ArchetypePowerhouse)
	p2 := mustFighter(t, "bob", combat.ArchetypeTechnician)

	return Record{
		Seed: 42,
		Archetypes: map[string]combat.Archetype{
			"alice": combat.ArchetypePowerhouse,
			"bob":   combat.ArchetypeTechnician,
		},
		Initial: combat.NewMatchState(p1, p2),
		Inputs: []InputEvent{
			{
				Turn:       1,
				RelativeMS: 0,
				Inputs: []combat.TurnInput{
					{PlayerID: "alice", Action: combat.ActionStrike, Target: combat.ZoneHead, DecisionMS: 120},
					{PlayerID: "bob", Action: combat.ActionDodge, Target: combat.ZoneTorso, DecisionMS: 180},
				},
			},
			{
				Turn:       2,
				RelativeMS: 130,
				Inputs: []combat.TurnInput{
					{PlayerID: "alice", Action: combat.ActionGrapple, Target: combat.ZoneTorso, DecisionMS: 160},
					{PlayerID: "bob", Action: combat.ActionCounter, Target: combat.ZoneHead, DecisionMS: 145},
				},
			},
			{
				Turn:       3,
				RelativeMS: 280,
				Inputs: []combat.TurnInput{
					{PlayerID: "alice", Action: combat.ActionStrike, Target: combat.ZoneLegs, DecisionMS: 110},
					{PlayerID: "bob", Action: combat.ActionBlock, Target: combat.ZoneTorso, DecisionMS: 220},
				},
			},
			{
				Turn:       4,
				RelativeMS: 410,
				Inputs: []combat.TurnInput{
					{PlayerID: "alice", Action: combat.ActionFeint, Target: combat.ZoneTorso, DecisionMS: 100},
					{PlayerID: "bob", Action: combat.ActionBreak, Target: combat.ZoneTorso, DecisionMS: 205},
				},
			},
		},
	}
}

func TestReplayDeterministicEquivalence(t *testing.T) {
	record := buildRecord(t)
	runner := NewRunner(combat.NewStandardResolver())

	resA, err := runner.Replay(record)
	if err != nil {
		t.Fatalf("replay run A: %v", err)
	}
	resB, err := runner.Replay(record)
	if err != nil {
		t.Fatalf("replay run B: %v", err)
	}

	if !reflect.DeepEqual(resA, resB) {
		t.Fatalf("replay outputs diverged for the same seed/inputs")
	}
}

func TestReplayMatchesDirectSimulation(t *testing.T) {
	record := buildRecord(t)
	resolver := combat.NewStandardResolver()
	sim := engine.NewCombatSimulator(record.Initial, resolver, record.Seed)

	direct := make([]combat.TurnResult, 0, len(record.Inputs))
	for _, ev := range record.Inputs {
		res, err := sim.Step(ev.Inputs)
		if err != nil {
			t.Fatalf("direct step turn %d: %v", ev.Turn, err)
		}
		direct = append(direct, res)
	}

	runner := NewRunner(resolver)
	replayed, err := runner.Replay(record)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}

	if len(direct) != len(replayed) {
		t.Fatalf("result length mismatch: direct=%d replayed=%d", len(direct), len(replayed))
	}

	for i := range direct {
		if direct[i].Checksums != replayed[i].Checksums {
			t.Fatalf("turn %d checksum mismatch: direct=%+v replayed=%+v", i+1, direct[i].Checksums, replayed[i].Checksums)
		}
		if !reflect.DeepEqual(direct[i].Next, replayed[i].Next) {
			t.Fatalf("turn %d state mismatch", i+1)
		}
	}
}
