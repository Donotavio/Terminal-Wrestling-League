package combat

import "fmt"

// State represents the global combat state machine.
type State uint8

const (
	StateNeutral State = iota
	StateClinch
	StateGround
	StateDominant
	StateStunned
	StateSubmissionAttempt
)

func (s State) String() string {
	switch s {
	case StateNeutral:
		return "Neutral"
	case StateClinch:
		return "Clinch"
	case StateGround:
		return "Ground"
	case StateDominant:
		return "Dominant"
	case StateStunned:
		return "Stunned"
	case StateSubmissionAttempt:
		return "SubmissionAttempt"
	default:
		return fmt.Sprintf("State(%d)", s)
	}
}

// Action represents one command chosen for a turn.
type Action uint8

const (
	ActionNone Action = iota
	ActionStrike
	ActionGrapple
	ActionBlock
	ActionDodge
	ActionCounter
	ActionFeint
	ActionBreak
)

func (a Action) String() string {
	switch a {
	case ActionNone:
		return "None"
	case ActionStrike:
		return "Strike"
	case ActionGrapple:
		return "Grapple"
	case ActionBlock:
		return "Block"
	case ActionDodge:
		return "Dodge"
	case ActionCounter:
		return "Counter"
	case ActionFeint:
		return "Feint"
	case ActionBreak:
		return "Break"
	default:
		return fmt.Sprintf("Action(%d)", a)
	}
}

// Zone is the target hitbox zone.
type Zone uint8

const (
	ZoneHead Zone = iota
	ZoneTorso
	ZoneLegs
)

func (z Zone) String() string {
	switch z {
	case ZoneHead:
		return "Head"
	case ZoneTorso:
		return "Torso"
	case ZoneLegs:
		return "Legs"
	default:
		return fmt.Sprintf("Zone(%d)", z)
	}
}

// Archetype defines static base attributes.
type Archetype uint8

const (
	ArchetypeBalanced Archetype = iota
	ArchetypePowerhouse
	ArchetypeTechnician
	ArchetypeHighFlyer
)

func (a Archetype) String() string {
	switch a {
	case ArchetypeBalanced:
		return "Balanced"
	case ArchetypePowerhouse:
		return "Powerhouse"
	case ArchetypeTechnician:
		return "Technician"
	case ArchetypeHighFlyer:
		return "HighFlyer"
	default:
		return fmt.Sprintf("Archetype(%d)", a)
	}
}

// FighterStats are fixed (1..10) by archetype.
type FighterStats struct {
	Power     int
	Technique int
	Agility   int
	Endurance int
}

var ArchetypeStats = map[Archetype]FighterStats{
	ArchetypeBalanced:   {Power: 6, Technique: 6, Agility: 6, Endurance: 6},
	ArchetypePowerhouse: {Power: 9, Technique: 4, Agility: 4, Endurance: 8},
	ArchetypeTechnician: {Power: 5, Technique: 9, Agility: 5, Endurance: 6},
	ArchetypeHighFlyer:  {Power: 4, Technique: 6, Agility: 9, Endurance: 5},
}

const (
	HPMax      = 100
	StaminaMax = 100
)

// StatusEffects stores deterministic turn-based effect counters.
type StatusEffects struct {
	StunnedFor           int
	TorsoRegenPenaltyFor int
	LegsDodgePenaltyFor  int
}

// FighterState holds mutable per-fighter battle state.
type FighterState struct {
	PlayerID             string
	Archetype            Archetype
	Stats                FighterStats
	HP                   int
	Stamina              int
	Momentum             int
	State                State
	Effects              StatusEffects
	LastAction           Action
	LastActionSuccess    bool
	LastOffensiveHitTurn int
}

// MatchOutcome marks end state when a winner exists.
type MatchOutcome struct {
	Finished bool
	WinnerID string
	Method   string
}

// MatchState is the complete authoritative state for one fight.
type MatchState struct {
	Turn        int
	P1          FighterState
	P2          FighterState
	CombatState State
	Distance    int
	RNGState    uint64
	Outcome     MatchOutcome
}

// TurnInput is one player's command for a turn.
type TurnInput struct {
	PlayerID   string
	Action     Action
	Target     Zone
	DecisionMS int
}

// EventType identifies result log categories.
type EventType string

const (
	EventTurnStarted     EventType = "turn_started"
	EventActionResolved  EventType = "action_resolved"
	EventDamageApplied   EventType = "damage_applied"
	EventStatusApplied   EventType = "status_applied"
	EventStateChanged    EventType = "state_changed"
	EventMatchFinished   EventType = "match_finished"
	EventStaminaRestored EventType = "stamina_restored"
)

// Event is replay-friendly structured output.
type Event struct {
	Turn     int
	Type     EventType
	PlayerID string
	TargetID string
	Action   Action
	Zone     Zone
	Value    int
	Success  bool
	Detail   string
}

// TurnChecksum captures deterministic turn identity.
type TurnChecksum struct {
	StateHash uint64
	RollHash  uint64
}

// TurnResult returns state transitions and event logs for one turn.
type TurnResult struct {
	Events    []Event
	Next      MatchState
	Checksums TurnChecksum
}

// Resolver deterministically resolves a turn.
type Resolver interface {
	ResolveTurn(state MatchState, inputs []TurnInput, rng RandomSource) (TurnResult, error)
}

// RandomSource is the minimum deterministic RNG contract used by combat.
type RandomSource interface {
	NextInt(n int) int
	Snapshot() uint64
}

func NewFighter(playerID string, archetype Archetype) (FighterState, error) {
	stats, ok := ArchetypeStats[archetype]
	if !ok {
		return FighterState{}, fmt.Errorf("unknown archetype: %v", archetype)
	}
	return FighterState{
		PlayerID:  playerID,
		Archetype: archetype,
		Stats:     stats,
		HP:        HPMax,
		Stamina:   StaminaMax,
		Momentum:  0,
		State:     StateNeutral,
		Effects:   StatusEffects{},
	}, nil
}

func NewMatchState(p1, p2 FighterState) MatchState {
	return MatchState{
		Turn:        0,
		P1:          p1,
		P2:          p2,
		CombatState: StateNeutral,
		Distance:    1,
	}
}
