package combat

import (
	"encoding/binary"
	"fmt"
	"hash"
	"hash/fnv"
)

// StandardResolver applies deterministic combat rules.
type StandardResolver struct{}

func NewStandardResolver() *StandardResolver {
	return &StandardResolver{}
}

type fighterTurnContext struct {
	stunnedAtTurnStart      bool
	torsoPenaltyAtTurnStart bool
	legsPenaltyAtTurnStart  bool
	opponentAction          Action
}

func (r *StandardResolver) ResolveTurn(state MatchState, inputs []TurnInput, rng RandomSource) (TurnResult, error) {
	if state.Outcome.Finished {
		return TurnResult{}, fmt.Errorf("match already finished")
	}

	ordered, err := CanonicalizeInputs(state, inputs)
	if err != nil {
		return TurnResult{}, err
	}

	next := state
	next.Turn = state.Turn + 1

	contexts := map[string]fighterTurnContext{}
	actions := map[string]TurnInput{}
	for _, in := range ordered {
		actions[in.PlayerID] = in
	}
	ctx1 := prepareFighterTurn(&next.P1)
	ctx1.opponentAction = actions[next.P2.PlayerID].Action
	contexts[next.P1.PlayerID] = ctx1
	ctx2 := prepareFighterTurn(&next.P2)
	ctx2.opponentAction = actions[next.P1.PlayerID].Action
	contexts[next.P2.PlayerID] = ctx2

	events := make([]Event, 0, 12)
	events = append(events, Event{Turn: next.Turn, Type: EventTurnStarted, Detail: "turn_begin"})

	rollHash := fnv.New64a()

	for _, in := range ordered {
		if next.Outcome.Finished {
			break
		}

		actor, defender, err := fightersByID(&next, in.PlayerID)
		if err != nil {
			return TurnResult{}, err
		}

		ctx := contexts[actor.PlayerID]
		target := normalizeZone(in.Target)
		action := normalizeAction(in.Action)
		actor.LastAction = action

		if ctx.stunnedAtTurnStart {
			actor.LastAction = ActionNone
			actor.LastActionSuccess = false
			events = append(events, Event{
				Turn:     next.Turn,
				Type:     EventStatusApplied,
				PlayerID: actor.PlayerID,
				Action:   ActionNone,
				Success:  false,
				Detail:   "stunned_skip",
			})
			continue
		}

		cost := ActionStaminaCost(action, actor.Stats.Endurance)
		if actor.Stamina < cost {
			actor.LastActionSuccess = false
			events = append(events, Event{
				Turn:     next.Turn,
				Type:     EventActionResolved,
				PlayerID: actor.PlayerID,
				TargetID: defender.PlayerID,
				Action:   action,
				Zone:     target,
				Success:  false,
				Detail:   "insufficient_stamina",
			})
			continue
		}
		actor.Stamina -= cost

		if action == ActionNone {
			actor.LastActionSuccess = false
			continue
		}

		chance := resolveSuccessChance(*actor, *defender, action, ctx)
		roll := rng.NextInt(10000)
		appendHashInt(rollHash, roll)
		success := roll < chance
		detail := "chance_roll"

		if success && RequiresHitbox(action) {
			move := MoveForAction(action)
			if !move.IsActive(1) || next.Distance > move.Range {
				success = false
				detail = "miss_out_of_range"
			}
		}

		actor.LastActionSuccess = success
		events = append(events, Event{
			Turn:     next.Turn,
			Type:     EventActionResolved,
			PlayerID: actor.PlayerID,
			TargetID: defender.PlayerID,
			Action:   action,
			Zone:     target,
			Value:    chance,
			Success:  success,
			Detail:   detail,
		})
		if !success {
			continue
		}

		comboActive := IsOffensive(action) && actor.LastOffensiveHitTurn == state.Turn && actor.Stamina > 30
		momentumBefore := actor.Momentum
		actor.Momentum = ClampInt(actor.Momentum+8, 0, 50)
		if comboActive {
			actor.Momentum = ClampInt(actor.Momentum+5, 0, 50)
			events = append(events, Event{
				Turn:     next.Turn,
				Type:     EventStatusApplied,
				PlayerID: actor.PlayerID,
				Action:   action,
				Success:  true,
				Detail:   "combo_active",
			})
		}

		switch action {
		case ActionStrike, ActionGrapple, ActionCounter:
			damage := DamageFinal(BaseDamage(action, actor.Stats), momentumBefore, comboActive)
			if damage > 0 {
				defender.HP = ClampInt(defender.HP-damage, 0, HPMax)
				defender.Momentum = ClampInt(defender.Momentum-6, 0, 50)
				actor.LastOffensiveHitTurn = next.Turn

				events = append(events, Event{
					Turn:     next.Turn,
					Type:     EventDamageApplied,
					PlayerID: actor.PlayerID,
					TargetID: defender.PlayerID,
					Action:   action,
					Zone:     target,
					Value:    damage,
					Success:  true,
					Detail:   "damage",
				})
			}

			applyZoneEffects(defender, target, &events, next.Turn)
			stunChance := StunChanceBPS(target, defender.Stamina, actor.Momentum-defender.Momentum)
			stunRoll := rng.NextInt(10000)
			appendHashInt(rollHash, stunRoll)
			if stunRoll < stunChance {
				defender.Effects.StunnedFor = MaxInt(defender.Effects.StunnedFor, 1)
				defender.State = StateStunned
				events = append(events, Event{
					Turn:     next.Turn,
					Type:     EventStatusApplied,
					PlayerID: defender.PlayerID,
					TargetID: actor.PlayerID,
					Zone:     target,
					Value:    stunChance,
					Success:  true,
					Detail:   "stunned",
				})
			}

			if action == ActionGrapple {
				applyGrappleTransition(&next, actor, defender, rng, rollHash, &events)
			}

			if defender.HP == 0 {
				next.Outcome = MatchOutcome{Finished: true, WinnerID: actor.PlayerID, Method: "KO"}
				events = append(events, Event{
					Turn:     next.Turn,
					Type:     EventMatchFinished,
					PlayerID: actor.PlayerID,
					TargetID: defender.PlayerID,
					Success:  true,
					Detail:   "ko",
				})
			}
		case ActionFeint:
			actor.LastOffensiveHitTurn = next.Turn
		case ActionBreak:
			next.CombatState = StateNeutral
			if actor.State != StateStunned {
				actor.State = StateNeutral
			}
			if defender.State != StateStunned {
				defender.State = StateNeutral
			}
			events = append(events, Event{
				Turn:     next.Turn,
				Type:     EventStateChanged,
				PlayerID: actor.PlayerID,
				Success:  true,
				Detail:   "break_to_neutral",
			})
		}
	}

	applyRegen(&next.P1, contexts[next.P1.PlayerID], next.Turn, &events)
	applyRegen(&next.P2, contexts[next.P2.PlayerID], next.Turn, &events)

	if next.P1.State == StateStunned && next.P1.Effects.StunnedFor == 0 {
		next.P1.State = next.CombatState
	}
	if next.P2.State == StateStunned && next.P2.Effects.StunnedFor == 0 {
		next.P2.State = next.CombatState
	}

	next.RNGState = rng.Snapshot()

	return TurnResult{
		Events: events,
		Next:   next,
		Checksums: TurnChecksum{
			StateHash: hashMatchState(next),
			RollHash:  rollHash.Sum64(),
		},
	}, nil
}

func prepareFighterTurn(f *FighterState) fighterTurnContext {
	ctx := fighterTurnContext{
		stunnedAtTurnStart:      f.Effects.StunnedFor > 0,
		torsoPenaltyAtTurnStart: f.Effects.TorsoRegenPenaltyFor > 0,
		legsPenaltyAtTurnStart:  f.Effects.LegsDodgePenaltyFor > 0,
	}
	if ctx.stunnedAtTurnStart {
		f.Effects.StunnedFor--
	}
	if ctx.torsoPenaltyAtTurnStart {
		f.Effects.TorsoRegenPenaltyFor--
	}
	if ctx.legsPenaltyAtTurnStart {
		f.Effects.LegsDodgePenaltyFor--
	}
	return ctx
}

func resolveSuccessChance(actor, defender FighterState, action Action, ctx fighterTurnContext) int {
	attackerAttr := ActionRelevantAttribute(action, actor.Stats)
	defenderAttr := defenseAttribute(action, defender, ctx.opponentAction, ctx.legsPenaltyAtTurnStart)
	chance := SuccessChanceBPS(attackerAttr, defenderAttr, actor.Momentum, actor.Stamina)
	if action == ActionDodge && ctx.legsPenaltyAtTurnStart {
		chance = (chance * 80) / 100
	}
	return ClampInt(chance, chanceMinBPS, chanceMaxBPS)
}

func defenseAttribute(action Action, defender FighterState, defenderAction Action, defenderLegsPenalty bool) int {
	if defenderAction == ActionBlock {
		return ClampInt(defender.Stats.Technique+1, 1, 10)
	}
	if defenderAction == ActionDodge {
		ag := defender.Stats.Agility
		if defenderLegsPenalty {
			ag = MaxInt(1, (ag*80)/100)
		}
		return ClampInt(ag, 1, 10)
	}

	switch action {
	case ActionStrike:
		return ClampInt(defender.Stats.Agility, 1, 10)
	case ActionGrapple:
		return ClampInt(defender.Stats.Technique, 1, 10)
	case ActionCounter:
		return ClampInt(defender.Stats.Technique, 1, 10)
	case ActionFeint:
		return ClampInt(defender.Stats.Technique, 1, 10)
	case ActionBreak:
		return ClampInt(defender.Stats.Power, 1, 10)
	default:
		return 1
	}
}

func applyZoneEffects(defender *FighterState, target Zone, events *[]Event, turn int) {
	switch target {
	case ZoneTorso:
		defender.Effects.TorsoRegenPenaltyFor = MaxInt(defender.Effects.TorsoRegenPenaltyFor, 2)
		*events = append(*events, Event{
			Turn:     turn,
			Type:     EventStatusApplied,
			PlayerID: defender.PlayerID,
			Zone:     ZoneTorso,
			Success:  true,
			Detail:   "torso_regen_penalty",
		})
	case ZoneLegs:
		defender.Effects.LegsDodgePenaltyFor = MaxInt(defender.Effects.LegsDodgePenaltyFor, 2)
		*events = append(*events, Event{
			Turn:     turn,
			Type:     EventStatusApplied,
			PlayerID: defender.PlayerID,
			Zone:     ZoneLegs,
			Success:  true,
			Detail:   "legs_dodge_penalty",
		})
	}
}

func applyGrappleTransition(next *MatchState, actor, defender *FighterState, rng RandomSource, rollHash hash.Hash64, events *[]Event) {
	prev := next.CombatState
	switch next.CombatState {
	case StateNeutral:
		next.CombatState = StateClinch
	case StateClinch:
		next.CombatState = StateGround
	case StateGround:
		next.CombatState = StateDominant
	case StateDominant:
		next.CombatState = StateSubmissionAttempt
	case StateSubmissionAttempt:
		// keep submission attempt until resolved or escaped by break
	}

	if next.CombatState != prev {
		if actor.State != StateStunned {
			actor.State = next.CombatState
		}
		if defender.State != StateStunned {
			defender.State = next.CombatState
		}
		*events = append(*events, Event{
			Turn:     next.Turn,
			Type:     EventStateChanged,
			PlayerID: actor.PlayerID,
			TargetID: defender.PlayerID,
			Success:  true,
			Detail:   fmt.Sprintf("%s_to_%s", prev, next.CombatState),
		})
	}

	if next.CombatState == StateSubmissionAttempt && !next.Outcome.Finished {
		subChance := SubmissionChanceBPS(*actor, *defender)
		roll := rng.NextInt(10000)
		appendHashInt(rollHash, roll)
		if roll < subChance {
			next.Outcome = MatchOutcome{Finished: true, WinnerID: actor.PlayerID, Method: "Submission"}
			*events = append(*events, Event{
				Turn:     next.Turn,
				Type:     EventMatchFinished,
				PlayerID: actor.PlayerID,
				TargetID: defender.PlayerID,
				Value:    subChance,
				Success:  true,
				Detail:   "submission",
			})
		}
	}
}

func applyRegen(f *FighterState, ctx fighterTurnContext, turn int, events *[]Event) {
	regen := RegenForEndurance(f.Stats.Endurance)
	if ctx.torsoPenaltyAtTurnStart {
		regen = (regen * 80) / 100
	}
	if regen < 0 {
		regen = 0
	}
	before := f.Stamina
	f.Stamina = ClampInt(f.Stamina+regen, 0, StaminaMax)
	delta := f.Stamina - before
	*events = append(*events, Event{
		Turn:     turn,
		Type:     EventStaminaRestored,
		PlayerID: f.PlayerID,
		Value:    delta,
		Success:  true,
		Detail:   "turn_regen",
	})
}

func fightersByID(state *MatchState, actorID string) (actor, defender *FighterState, err error) {
	switch actorID {
	case state.P1.PlayerID:
		return &state.P1, &state.P2, nil
	case state.P2.PlayerID:
		return &state.P2, &state.P1, nil
	default:
		return nil, nil, fmt.Errorf("unknown player id %q", actorID)
	}
}

func normalizeAction(a Action) Action {
	switch a {
	case ActionStrike, ActionGrapple, ActionBlock, ActionDodge, ActionCounter, ActionFeint, ActionBreak, ActionNone:
		return a
	default:
		return ActionNone
	}
}

func normalizeZone(z Zone) Zone {
	switch z {
	case ZoneHead, ZoneTorso, ZoneLegs:
		return z
	default:
		return ZoneTorso
	}
}

func appendHashInt(h hash.Hash64, v int) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	_, _ = h.Write(buf)
}

func hashMatchState(state MatchState) uint64 {
	h := fnv.New64a()
	writeHashString(h, state.P1.PlayerID)
	writeHashString(h, state.P2.PlayerID)
	writeHashInt(h, state.Turn)
	writeHashInt(h, int(state.CombatState))
	writeHashInt(h, state.Distance)
	writeHashInt(h, int(state.RNGState&0xffffffff))
	writeHashFighter(h, state.P1)
	writeHashFighter(h, state.P2)
	writeHashBool(h, state.Outcome.Finished)
	writeHashString(h, state.Outcome.WinnerID)
	writeHashString(h, state.Outcome.Method)
	return h.Sum64()
}

func writeHashFighter(h hash.Hash64, f FighterState) {
	writeHashString(h, f.PlayerID)
	writeHashInt(h, int(f.Archetype))
	writeHashInt(h, f.Stats.Power)
	writeHashInt(h, f.Stats.Technique)
	writeHashInt(h, f.Stats.Agility)
	writeHashInt(h, f.Stats.Endurance)
	writeHashInt(h, f.HP)
	writeHashInt(h, f.Stamina)
	writeHashInt(h, f.Momentum)
	writeHashInt(h, int(f.State))
	writeHashInt(h, f.Effects.StunnedFor)
	writeHashInt(h, f.Effects.TorsoRegenPenaltyFor)
	writeHashInt(h, f.Effects.LegsDodgePenaltyFor)
	writeHashInt(h, int(f.LastAction))
	writeHashBool(h, f.LastActionSuccess)
	writeHashInt(h, f.LastOffensiveHitTurn)
}

func writeHashInt(h hash.Hash64, v int) {
	buf := make([]byte, 8)
	binary.LittleEndian.PutUint64(buf, uint64(v))
	_, _ = h.Write(buf)
}

func writeHashBool(h hash.Hash64, v bool) {
	if v {
		writeHashInt(h, 1)
		return
	}
	writeHashInt(h, 0)
}

func writeHashString(h hash.Hash64, v string) {
	_, _ = h.Write([]byte(v))
	_, _ = h.Write([]byte{0})
}
