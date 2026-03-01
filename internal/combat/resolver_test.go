package combat

import "testing"

type scriptedRNG struct {
	rolls []int
	idx   int
}

func (r *scriptedRNG) NextInt(n int) int {
	if n <= 0 {
		return 0
	}
	if len(r.rolls) == 0 {
		return 0
	}
	v := r.rolls[r.idx%len(r.rolls)] % n
	r.idx++
	if v < 0 {
		v += n
	}
	return v
}

func (r *scriptedRNG) Snapshot() uint64 {
	return uint64(r.idx)
}

func newTestMatch(t *testing.T) MatchState {
	t.Helper()
	p1, err := NewFighter("alice", ArchetypePowerhouse)
	if err != nil {
		t.Fatalf("NewFighter p1: %v", err)
	}
	p2, err := NewFighter("bob", ArchetypeTechnician)
	if err != nil {
		t.Fatalf("NewFighter p2: %v", err)
	}
	return NewMatchState(p1, p2)
}

func TestActionStaminaCost(t *testing.T) {
	if got := ActionStaminaCost(ActionStrike, 10); got != 7 {
		t.Fatalf("strike cost with endurance 10 = %d, want 7", got)
	}
	if got := ActionStaminaCost(ActionGrapple, 1); got != 13 {
		t.Fatalf("grapple cost with endurance 1 = %d, want 13", got)
	}
}

func TestRegenForEndurance(t *testing.T) {
	if got := RegenForEndurance(1); got != 5 {
		t.Fatalf("regen endurance 1 = %d, want 5", got)
	}
	if got := RegenForEndurance(10); got != 13 {
		t.Fatalf("regen endurance 10 = %d, want 13", got)
	}
}

func TestSuccessChanceClamp(t *testing.T) {
	high := SuccessChanceBPS(20, 0, 50, 100)
	if high != 9000 {
		t.Fatalf("high chance = %d, want 9000", high)
	}
	low := SuccessChanceBPS(1, 20, 0, 1)
	if low != 1000 {
		t.Fatalf("low chance = %d, want 1000", low)
	}
}

func TestGrappleTransitionsCombatState(t *testing.T) {
	resolver := NewStandardResolver()
	state := newTestMatch(t)
	rng := &scriptedRNG{rolls: []int{0, 9999, 0, 9999}}

	res1, err := resolver.ResolveTurn(state, []TurnInput{
		{PlayerID: "alice", Action: ActionGrapple, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 1: %v", err)
	}
	if res1.Next.CombatState != StateClinch {
		t.Fatalf("combat state after 1st grapple = %s, want %s", res1.Next.CombatState, StateClinch)
	}

	res2, err := resolver.ResolveTurn(res1.Next, []TurnInput{
		{PlayerID: "alice", Action: ActionGrapple, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 2: %v", err)
	}
	if res2.Next.CombatState != StateGround {
		t.Fatalf("combat state after 2nd grapple = %s, want %s", res2.Next.CombatState, StateGround)
	}
}

func TestStunDurationOneTurn(t *testing.T) {
	resolver := NewStandardResolver()
	state := newTestMatch(t)
	state.P1.Momentum = 50
	state.P2.Stamina = 10
	rng := &scriptedRNG{rolls: []int{0, 0, 0, 0, 0, 0}}

	res1, err := resolver.ResolveTurn(state, []TurnInput{
		{PlayerID: "alice", Action: ActionStrike, Target: ZoneHead},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 1: %v", err)
	}
	if got := res1.Next.P2.Effects.StunnedFor; got != 1 {
		t.Fatalf("stunned turns after hit = %d, want 1", got)
	}

	res2, err := resolver.ResolveTurn(res1.Next, []TurnInput{
		{PlayerID: "alice", Action: ActionNone, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionStrike, Target: ZoneHead},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 2: %v", err)
	}
	if got := res2.Next.P2.Effects.StunnedFor; got != 0 {
		t.Fatalf("stunned turns after skip = %d, want 0", got)
	}
	if got := res2.Next.P2.LastAction; got != ActionNone {
		t.Fatalf("bob action while stunned = %s, want %s", got, ActionNone)
	}
}

func TestDebuffExpirationTwoTurns(t *testing.T) {
	resolver := NewStandardResolver()
	state := newTestMatch(t)
	state.P2.Effects.TorsoRegenPenaltyFor = 2
	state.P2.Effects.LegsDodgePenaltyFor = 2
	rng := &scriptedRNG{rolls: []int{0, 0, 0, 0}}

	res1, err := resolver.ResolveTurn(state, []TurnInput{
		{PlayerID: "alice", Action: ActionNone, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 1: %v", err)
	}
	if got := res1.Next.P2.Effects.TorsoRegenPenaltyFor; got != 1 {
		t.Fatalf("torso penalty after turn 1 = %d, want 1", got)
	}
	if got := res1.Next.P2.Effects.LegsDodgePenaltyFor; got != 1 {
		t.Fatalf("legs penalty after turn 1 = %d, want 1", got)
	}

	res2, err := resolver.ResolveTurn(res1.Next, []TurnInput{
		{PlayerID: "alice", Action: ActionNone, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 2: %v", err)
	}
	if got := res2.Next.P2.Effects.TorsoRegenPenaltyFor; got != 0 {
		t.Fatalf("torso penalty after turn 2 = %d, want 0", got)
	}
	if got := res2.Next.P2.Effects.LegsDodgePenaltyFor; got != 0 {
		t.Fatalf("legs penalty after turn 2 = %d, want 0", got)
	}
}

func TestComboWindowAppliesBonus(t *testing.T) {
	resolver := NewStandardResolver()
	state := newTestMatch(t)
	rng := &scriptedRNG{rolls: []int{0, 9999, 0, 9999, 0, 9999, 0, 9999}}

	res1, err := resolver.ResolveTurn(state, []TurnInput{
		{PlayerID: "alice", Action: ActionStrike, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 1: %v", err)
	}

	res2, err := resolver.ResolveTurn(res1.Next, []TurnInput{
		{PlayerID: "alice", Action: ActionStrike, Target: ZoneTorso},
		{PlayerID: "bob", Action: ActionNone, Target: ZoneTorso},
	}, rng)
	if err != nil {
		t.Fatalf("resolve turn 2: %v", err)
	}

	firstDamage := 0
	for _, e := range res1.Events {
		if e.Type == EventDamageApplied && e.PlayerID == "alice" {
			firstDamage = e.Value
			break
		}
	}
	if firstDamage == 0 {
		t.Fatalf("expected damage event in turn 1")
	}

	secondDamage := 0
	comboSeen := false
	for _, e := range res2.Events {
		if e.Type == EventDamageApplied && e.PlayerID == "alice" {
			secondDamage = e.Value
		}
		if e.Type == EventStatusApplied && e.PlayerID == "alice" && e.Detail == "combo_active" {
			comboSeen = true
		}
	}
	if !comboSeen {
		t.Fatalf("expected combo_active event in turn 2")
	}
	if secondDamage <= firstDamage {
		t.Fatalf("combo damage = %d, expected > first damage %d", secondDamage, firstDamage)
	}
}
