package combat

const (
	chanceMinBPS = 1000
	chanceMaxBPS = 9000
	stunMaxBPS   = 9500
)

const (
	baseStunBPS            = 1000
	headStunBonusBPS       = 1000
	lowStaminaStunBonusBPS = 500
)

const (
	fatigueThreshold = 20
	fatiguePenalty   = 1500
)

// MoveSpec describes deterministic logical hitbox behavior for an action.
type MoveSpec struct {
	Range             int
	FrameActiveStart  int
	FrameActiveEnd    int
	RequiresCollision bool
}

func (m MoveSpec) IsActive(frame int) bool {
	return frame >= m.FrameActiveStart && frame <= m.FrameActiveEnd
}

var actionBaseCost = map[Action]int{
	ActionNone:    0,
	ActionStrike:  10,
	ActionGrapple: 14,
	ActionDodge:   8,
	ActionBlock:   6,
	ActionCounter: 12,
	ActionFeint:   9,
	ActionBreak:   10,
}

var actionMoveSpec = map[Action]MoveSpec{
	ActionStrike:  {Range: 2, FrameActiveStart: 1, FrameActiveEnd: 2, RequiresCollision: true},
	ActionGrapple: {Range: 1, FrameActiveStart: 1, FrameActiveEnd: 2, RequiresCollision: true},
	ActionCounter: {Range: 2, FrameActiveStart: 1, FrameActiveEnd: 1, RequiresCollision: true},
	ActionFeint:   {Range: 2, FrameActiveStart: 1, FrameActiveEnd: 1, RequiresCollision: false},
}

func ClampInt(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func MaxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// ActionStaminaCost applies endurance discount on top of base action cost.
func ActionStaminaCost(action Action, endurance int) int {
	base := actionBaseCost[action]
	if base <= 0 {
		return 0
	}
	endurance = ClampInt(endurance, 1, 10)
	cost := (base * (100 - endurance*3)) / 100
	if cost < 1 {
		cost = 1
	}
	return cost
}

// RegenForEndurance returns per-turn stamina regen.
func RegenForEndurance(endurance int) int {
	endurance = ClampInt(endurance, 1, 10)
	return (50 + endurance*8) / 10
}

func FatiguePenaltyBPS(stamina int) int {
	if stamina < fatigueThreshold {
		return fatiguePenalty
	}
	return 0
}

// SuccessChanceBPS computes final clamped success chance.
func SuccessChanceBPS(attackerAttr, defenderAttr, attackerMomentum, attackerStamina int) int {
	chance := 5000 + attackerAttr*300 - defenderAttr*200 + attackerMomentum*15 - FatiguePenaltyBPS(attackerStamina)
	return ClampInt(chance, chanceMinBPS, chanceMaxBPS)
}

func ActionRelevantAttribute(action Action, stats FighterStats) int {
	switch action {
	case ActionStrike:
		return stats.Power
	case ActionGrapple:
		return stats.Technique
	case ActionBlock:
		return stats.Technique
	case ActionDodge:
		return stats.Agility
	case ActionCounter:
		return stats.Technique
	case ActionFeint:
		return stats.Agility
	case ActionBreak:
		return stats.Power
	default:
		return 1
	}
}

func BaseDamage(action Action, stats FighterStats) int {
	switch action {
	case ActionStrike:
		return 12 + stats.Power
	case ActionGrapple:
		return 10 + stats.Technique
	case ActionCounter:
		return 8 + stats.Technique
	default:
		return 0
	}
}

// DamageFinal applies momentum and combo modifiers.
func DamageFinal(baseDamage, momentum int, comboActive bool) int {
	damage := (baseDamage * (100 + ClampInt(momentum, 0, 50))) / 100
	if comboActive {
		damage = (damage * 110) / 100
	}
	if damage < 0 {
		return 0
	}
	return damage
}

func StunChanceBPS(zone Zone, defenderStamina, momentumDiff int) int {
	chance := baseStunBPS
	if zone == ZoneHead {
		chance += headStunBonusBPS
	}
	if defenderStamina < fatigueThreshold {
		chance += lowStaminaStunBonusBPS
	}
	chance += momentumDiff * 50
	return ClampInt(chance, 0, stunMaxBPS)
}

func SubmissionChanceBPS(attacker, defender FighterState) int {
	chance := 3000 + (attacker.Stats.Technique-defender.Stats.Technique)*300 + (attacker.Momentum-defender.Momentum)*20
	if defender.Stamina < fatigueThreshold {
		chance += 500
	}
	return ClampInt(chance, chanceMinBPS, chanceMaxBPS)
}

func IsOffensive(action Action) bool {
	switch action {
	case ActionStrike, ActionGrapple, ActionCounter, ActionFeint:
		return true
	default:
		return false
	}
}

func RequiresHitbox(action Action) bool {
	spec, ok := actionMoveSpec[action]
	return ok && spec.RequiresCollision
}

func MoveForAction(action Action) MoveSpec {
	spec, ok := actionMoveSpec[action]
	if !ok {
		return MoveSpec{}
	}
	return spec
}
