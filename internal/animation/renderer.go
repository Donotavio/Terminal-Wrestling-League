package animation

import (
	"fmt"
	"sync"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
)

// Effect identifies deterministic render side-effects for clients.
type Effect string

const (
	EffectHitstop   Effect = "hitstop"
	EffectShake     Effect = "shake"
	EffectKnockback Effect = "knockback"
	EffectSlowmo    Effect = "slowmo"
)

// Frame is the rendered output for one combat turn.
type Frame struct {
	Full    []string
	Delta   []string
	Effects []Effect
}

// Renderer builds deterministic combat frames.
type Renderer interface {
	Render(handle1, handle2 string, result combat.TurnResult) Frame
}

// ASCIIRenderer renders frames and computes delta against the prior frame.
type ASCIIRenderer struct {
	mu   sync.Mutex
	last []string
}

func NewASCIIRenderer() *ASCIIRenderer {
	return &ASCIIRenderer{}
}

func (r *ASCIIRenderer) Render(handle1, handle2 string, result combat.TurnResult) Frame {
	r.mu.Lock()
	defer r.mu.Unlock()

	full := buildFullFrame(handle1, handle2, result)
	delta := buildDeltaFrame(r.last, full)
	effects := inferEffects(result)

	r.last = append(r.last[:0], full...)

	return Frame{
		Full:    full,
		Delta:   delta,
		Effects: effects,
	}
}

func buildFullFrame(handle1, handle2 string, result combat.TurnResult) []string {
	s := result.Next
	leftPose := poseForAction(s.P1.LastAction, false)
	rightPose := poseForAction(s.P2.LastAction, true)
	lines := []string{
		fmt.Sprintf("Turn %d", s.Turn),
		fmt.Sprintf("%s HP:%d ST:%d MO:%d", handle1, s.P1.HP, s.P1.Stamina, s.P1.Momentum),
		fmt.Sprintf("%s HP:%d ST:%d MO:%d", handle2, s.P2.HP, s.P2.Stamina, s.P2.Momentum),
		fmt.Sprintf("%-24s%-24s", stanceLabel(handle1, s.P1.LastAction), stanceLabel(handle2, s.P2.LastAction)),
		fmt.Sprintf("%-24s%-24s", "   "+leftPose.Head, "   "+rightPose.Head),
		fmt.Sprintf("%-24s%-24s", "   "+leftPose.Torso, "   "+rightPose.Torso),
		fmt.Sprintf("%-24s%-24s", "   "+leftPose.Legs, "   "+rightPose.Legs),
	}
	limit := 3
	for _, e := range result.Events {
		if limit == 0 {
			break
		}
		if e.Type == combat.EventActionResolved || e.Type == combat.EventDamageApplied || e.Type == combat.EventMatchFinished {
			lines = append(lines, fmt.Sprintf("event: %s %s success=%t", e.Type, e.Detail, e.Success))
			limit--
		}
	}
	if s.Outcome.Finished {
		lines = append(lines, fmt.Sprintf("Outcome: %s", s.Outcome.Method))
	}
	return lines
}

type stickPose struct {
	Head  string
	Torso string
	Legs  string
}

func poseForAction(action combat.Action, mirror bool) stickPose {
	head := " o "
	legs := "/ \\"
	switch action {
	case combat.ActionStrike:
		if mirror {
			return stickPose{Head: head, Torso: "<|-", Legs: legs}
		}
		return stickPose{Head: head, Torso: "-|>", Legs: legs}
	case combat.ActionBlock:
		return stickPose{Head: head, Torso: "[|]", Legs: legs}
	case combat.ActionDodge:
		if mirror {
			return stickPose{Head: head, Torso: "<| ", Legs: legs}
		}
		return stickPose{Head: head, Torso: " |>", Legs: legs}
	case combat.ActionCounter:
		return stickPose{Head: head, Torso: "<|>", Legs: legs}
	case combat.ActionGrapple, combat.ActionBreak:
		return stickPose{Head: head, Torso: "\\|/", Legs: legs}
	case combat.ActionFeint:
		return stickPose{Head: head, Torso: "~|~", Legs: legs}
	default:
		return stickPose{Head: head, Torso: "/|\\", Legs: legs}
	}
}

func stanceLabel(handle string, action combat.Action) string {
	return fmt.Sprintf("%s [%s]", handle, actionLabel(action))
}

func actionLabel(action combat.Action) string {
	switch action {
	case combat.ActionStrike:
		return "STRIKE"
	case combat.ActionGrapple:
		return "GRAPPLE"
	case combat.ActionBlock:
		return "BLOCK"
	case combat.ActionDodge:
		return "DODGE"
	case combat.ActionCounter:
		return "COUNTER"
	case combat.ActionFeint:
		return "FEINT"
	case combat.ActionBreak:
		return "BREAK"
	default:
		return "IDLE"
	}
}

func buildDeltaFrame(prev, curr []string) []string {
	maxLen := len(curr)
	if len(prev) > maxLen {
		maxLen = len(prev)
	}
	delta := make([]string, 0, maxLen)
	for i := 0; i < maxLen; i++ {
		prevLine := ""
		if i < len(prev) {
			prevLine = prev[i]
		}
		currLine := ""
		if i < len(curr) {
			currLine = curr[i]
		}
		if prevLine == currLine {
			continue
		}
		delta = append(delta, fmt.Sprintf("[Δ L%d] %s", i+1, currLine))
	}
	return delta
}

func inferEffects(result combat.TurnResult) []Effect {
	hasDamage := false
	hasMatchFinished := false
	hasShake := false
	hasKnockback := false

	for _, e := range result.Events {
		switch e.Type {
		case combat.EventDamageApplied:
			hasDamage = true
			if e.Value >= 15 {
				hasShake = true
			}
			if e.Success && (e.Action == combat.ActionGrapple || e.Action == combat.ActionCounter) {
				hasKnockback = true
			}
		case combat.EventMatchFinished:
			hasMatchFinished = true
			hasShake = true
		}
	}

	effects := make([]Effect, 0, 4)
	if hasDamage {
		effects = append(effects, EffectHitstop)
	}
	if hasShake {
		effects = append(effects, EffectShake)
	}
	if hasKnockback {
		effects = append(effects, EffectKnockback)
	}
	if hasMatchFinished {
		effects = append(effects, EffectSlowmo)
	}
	return effects
}
