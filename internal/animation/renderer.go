package animation

import (
	"fmt"
	"strings"

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
	// Full and Delta are kept for backward compatibility.
	Full      []string
	Delta     []string
	Keyframes [][]string
	Summary   string
	Effects   []Effect
}

// Renderer builds deterministic combat frames.
type Renderer interface {
	Render(handle1, handle2 string, result combat.TurnResult) Frame
}

// ASCIIRenderer renders deterministic cinematic ASCII frames.
type ASCIIRenderer struct{}

func NewASCIIRenderer() *ASCIIRenderer {
	return &ASCIIRenderer{}
}

func (r *ASCIIRenderer) Render(handle1, handle2 string, result combat.TurnResult) Frame {
	effects := inferEffects(result)
	summary := summarizeTurn(result)
	keyframes := buildCinematicKeyframes(handle1, handle2, result, summary, effects)
	full := []string(nil)
	if len(keyframes) > 0 {
		full = append([]string(nil), keyframes[len(keyframes)-1]...)
	}

	return Frame{
		Full:      full,
		Delta:     nil,
		Keyframes: keyframes,
		Summary:   summary,
		Effects:   effects,
	}
}

const (
	arenaWidth = 58
	hpBarSize  = 14
)

type cinematicPhase struct {
	Name         string
	LeftAdvance  int
	RightAdvance int
	Shake        bool
	LeftPose     stickPose
	RightPose    stickPose
}

type turnVFX struct {
	HitCount     int
	TotalDamage  int
	HeavyHit     bool
	LeftHit      bool
	RightHit     bool
	Stunned      bool
	ComboActive  bool
	KnockbackHit bool
}

func buildCinematicKeyframes(handle1, handle2 string, result combat.TurnResult, summary string, effects []Effect) [][]string {
	state := result.Next
	leftAdvance := approachDistance(state.P1.LastAction)
	rightAdvance := approachDistance(state.P2.LastAction)
	impactShake := hasEffect(effects, EffectShake) || hasEffect(effects, EffectHitstop)
	vfx := deriveTurnVFX(result, effects)

	phases := []cinematicPhase{
		{
			Name:         "Guard",
			LeftAdvance:  0,
			RightAdvance: 0,
			LeftPose:     poseForPhase(combat.ActionNone, false, "guard"),
			RightPose:    poseForPhase(combat.ActionNone, true, "guard"),
		},
		{
			Name:         "Windup",
			LeftAdvance:  leftAdvance / 2,
			RightAdvance: rightAdvance / 2,
			LeftPose:     poseForPhase(state.P1.LastAction, false, "windup"),
			RightPose:    poseForPhase(state.P2.LastAction, true, "windup"),
		},
		{
			Name:         "Impact",
			LeftAdvance:  leftAdvance,
			RightAdvance: rightAdvance,
			Shake:        impactShake,
			LeftPose:     poseForPhase(state.P1.LastAction, false, "impact"),
			RightPose:    poseForPhase(state.P2.LastAction, true, "impact"),
		},
	}
	if hasEffect(effects, EffectHitstop) && vfx.HitCount > 0 {
		phases = append(phases, cinematicPhase{
			Name:         "Hitstop",
			LeftAdvance:  leftAdvance,
			RightAdvance: rightAdvance,
			Shake:        true,
			LeftPose:     poseForPhase(state.P1.LastAction, false, "impact"),
			RightPose:    poseForPhase(state.P2.LastAction, true, "impact"),
		})
	}
	recoverName := "Recover"
	if hasEffect(effects, EffectKnockback) && (vfx.LeftHit || vfx.RightHit || vfx.KnockbackHit) {
		recoverName = "Knockback"
	}
	phases = append(phases, cinematicPhase{
		Name:         recoverName,
		LeftAdvance:  maxInt(1, leftAdvance/3),
		RightAdvance: maxInt(1, rightAdvance/3),
		LeftPose:     poseForPhase(state.P1.LastAction, false, "recover"),
		RightPose:    poseForPhase(state.P2.LastAction, true, "recover"),
	})
	if vfx.Stunned {
		phases = append(phases, cinematicPhase{
			Name:         "Stunned",
			LeftAdvance:  maxInt(1, leftAdvance/4),
			RightAdvance: maxInt(1, rightAdvance/4),
			Shake:        true,
			LeftPose:     poseForPhase(state.P1.LastAction, false, "stunned"),
			RightPose:    poseForPhase(state.P2.LastAction, true, "stunned"),
		})
	}

	keyframes := make([][]string, 0, len(phases)+1)
	for i, phase := range phases {
		last := i == len(phases)-1
		keyframes = append(keyframes, renderKeyframe(handle1, handle2, state, phase, summary, vfx, last))
	}
	if hasEffect(effects, EffectSlowmo) || state.Outcome.Finished {
		last := append([]string(nil), keyframes[len(keyframes)-1]...)
		if len(last) > 0 {
			last[0] = last[0] + " | SLOWMO"
		}
		keyframes = append(keyframes, last)
	}
	return keyframes
}

func renderKeyframe(handle1, handle2 string, state combat.MatchState, phase cinematicPhase, summary string, vfx turnVFX, final bool) []string {
	leftX := 7 + phase.LeftAdvance
	rightX := arenaWidth - 10 - phase.RightAdvance
	if phase.Shake {
		if state.Turn%2 == 0 {
			leftX++
			rightX--
		} else {
			leftX--
			rightX++
		}
	}
	if leftX+4 >= rightX {
		mid := arenaWidth / 2
		leftX = mid - 3
		rightX = mid + 1
	}
	if strings.EqualFold(phase.Name, "knockback") {
		if vfx.LeftHit {
			leftX -= 2
		}
		if vfx.RightHit || vfx.KnockbackHit {
			rightX += 2
		}
	}
	leftX = clampInt(leftX, 1, arenaWidth-4)
	rightX = clampInt(rightX, 1, arenaWidth-4)

	lines := []string{
		fmt.Sprintf("Turn %d | %s", state.Turn, strings.ToUpper(phase.Name)),
		fmt.Sprintf("%-18s HP:%3d ST:%3d MO:%3d %s", fitHandle(handle1, 18), state.P1.HP, state.P1.Stamina, state.P1.Momentum, healthBar(state.P1.HP)),
		fmt.Sprintf("%-18s HP:%3d ST:%3d MO:%3d %s", fitHandle(handle2, 18), state.P2.HP, state.P2.Stamina, state.P2.Momentum, healthBar(state.P2.HP)),
		fmt.Sprintf("%-32s%-32s", stanceLabel(handle1, state.P1.LastAction), stanceLabel(handle2, state.P2.LastAction)),
		"+" + strings.Repeat("-", arenaWidth) + "+",
		"|" + composeArenaLine(arenaWidth, leftX, phase.LeftPose.Head, rightX, phase.RightPose.Head) + "|",
		"|" + composeArenaLine(arenaWidth, leftX, phase.LeftPose.Torso, rightX, phase.RightPose.Torso) + "|",
		"|" + composeFXOverlayLine(arenaWidth, phase.Name, vfx) + "|",
		"|" + composeArenaLine(arenaWidth, leftX, phase.LeftPose.Legs, rightX, phase.RightPose.Legs) + "|",
		"|" + strings.Repeat("_", arenaWidth) + "|",
		"+" + strings.Repeat("-", arenaWidth) + "+",
	}
	fxCaption := phaseEffectCaption(phase.Name, vfx)
	if fxCaption == "" {
		fxCaption = " "
	}
	if final {
		lines = append(lines, fxCaption, summary)
	} else {
		lines = append(lines, fxCaption)
	}
	return lines
}

func composeArenaLine(width int, leftX int, left string, rightX int, right string) string {
	row := make([]rune, width)
	for i := range row {
		row[i] = ' '
	}
	if width > 0 {
		row[width/2] = ':'
	}
	stamp := func(x int, sprite string) {
		for i, r := range []rune(sprite) {
			idx := x + i
			if idx < 0 || idx >= width {
				continue
			}
			row[idx] = r
		}
	}
	stamp(leftX, left)
	stamp(rightX, right)
	return string(row)
}

func composeFXOverlayLine(width int, phaseName string, vfx turnVFX) string {
	row := make([]rune, width)
	for i := range row {
		row[i] = ' '
	}
	if width > 0 {
		row[width/2] = ':'
	}
	phase := strings.ToLower(strings.TrimSpace(phaseName))
	marker := ""
	switch phase {
	case "impact":
		if vfx.HitCount > 0 {
			marker = impactWord(vfx.TotalDamage)
		}
	case "hitstop":
		marker = "<<< HITSTOP >>>"
	case "knockback":
		if vfx.LeftHit || vfx.RightHit || vfx.KnockbackHit {
			marker = "<< KNOCKBACK >>"
		}
	case "stunned":
		if vfx.Stunned {
			marker = "** STUN **"
		}
	}
	if marker == "" {
		return string(row)
	}
	return overlayCenter(string(row), marker)
}

func overlayCenter(base string, marker string) string {
	row := []rune(base)
	mr := []rune(marker)
	if len(mr) == 0 || len(row) == 0 {
		return base
	}
	start := (len(row) - len(mr)) / 2
	if start < 0 {
		start = 0
	}
	for i, r := range mr {
		idx := start + i
		if idx >= len(row) {
			break
		}
		row[idx] = r
	}
	return string(row)
}

func impactWord(totalDamage int) string {
	switch {
	case totalDamage >= 28:
		return "!!! KRAK !!!"
	case totalDamage >= 18:
		return "!! BOOM !!"
	case totalDamage >= 10:
		return "! THUD !"
	default:
		return "* TAP *"
	}
}

func phaseEffectCaption(phaseName string, vfx turnVFX) string {
	phase := strings.ToLower(strings.TrimSpace(phaseName))
	switch phase {
	case "impact":
		if vfx.HitCount == 0 {
			return "FX: whiff"
		}
		if vfx.ComboActive {
			return fmt.Sprintf("FX: combo chain (%d hits)", vfx.HitCount)
		}
		return fmt.Sprintf("FX: impact x%d (%d dmg)", vfx.HitCount, vfx.TotalDamage)
	case "hitstop":
		return "FX: freeze frame"
	case "knockback":
		return "FX: knockback push"
	case "stunned":
		return "FX: dizzy state"
	default:
		return ""
	}
}

func healthBar(hp int) string {
	hp = clampInt(hp, 0, combat.HPMax)
	fill := (hp * hpBarSize) / combat.HPMax
	return fmt.Sprintf("[%s%s]", strings.Repeat("#", fill), strings.Repeat(".", hpBarSize-fill))
}

func fitHandle(handle string, max int) string {
	if max <= 0 {
		return ""
	}
	if len(handle) <= max {
		return handle
	}
	if max == 1 {
		return handle[:1]
	}
	return handle[:max-1] + "~"
}

func summarizeTurn(result combat.TurnResult) string {
	state := result.Next
	if state.Outcome.Finished {
		if state.Outcome.Method == "" {
			return "Outcome: finished"
		}
		return fmt.Sprintf("Outcome: %s", state.Outcome.Method)
	}

	hits := 0
	totalDamage := 0
	for _, event := range result.Events {
		if event.Type != combat.EventDamageApplied || !event.Success {
			continue
		}
		hits++
		if event.Value > 0 {
			totalDamage += event.Value
		}
	}
	switch hits {
	case 0:
		return "Exchange: no clean hit"
	case 1:
		return fmt.Sprintf("Exchange: clean hit (%d dmg)", totalDamage)
	default:
		return fmt.Sprintf("Exchange: traded blows (%d dmg total)", totalDamage)
	}
}

func deriveTurnVFX(result combat.TurnResult, effects []Effect) turnVFX {
	state := result.Next
	vfx := turnVFX{}
	for _, event := range result.Events {
		switch event.Type {
		case combat.EventDamageApplied:
			if !event.Success || event.Value <= 0 {
				continue
			}
			vfx.HitCount++
			vfx.TotalDamage += event.Value
			if event.TargetID == state.P1.PlayerID {
				vfx.LeftHit = true
			}
			if event.TargetID == state.P2.PlayerID {
				vfx.RightHit = true
			}
		case combat.EventStatusApplied:
			if strings.EqualFold(event.Detail, "stunned") {
				vfx.Stunned = true
				if event.TargetID == state.P1.PlayerID {
					vfx.LeftHit = true
				}
				if event.TargetID == state.P2.PlayerID {
					vfx.RightHit = true
				}
			}
			if strings.EqualFold(event.Detail, "combo_active") {
				vfx.ComboActive = true
			}
		}
	}
	vfx.HeavyHit = vfx.TotalDamage >= 22
	vfx.KnockbackHit = hasEffect(effects, EffectKnockback)
	if !vfx.LeftHit && !vfx.RightHit && vfx.HitCount > 0 {
		// Fallback when target ids are unavailable in events.
		vfx.RightHit = true
	}
	return vfx
}

func poseForPhase(action combat.Action, mirror bool, phase string) stickPose {
	switch strings.ToLower(phase) {
	case "guard":
		return stickPose{Head: " o ", Torso: "/|\\", Legs: "/ \\"}
	case "windup":
		return windupPose(action, mirror)
	case "recover":
		return recoverPose(action, mirror)
	case "stunned":
		return stickPose{Head: " * ", Torso: "x|x", Legs: "/ \\"}
	default:
		return poseForAction(action, mirror)
	}
}

func windupPose(action combat.Action, mirror bool) stickPose {
	head := " o "
	legs := "/ \\"
	switch action {
	case combat.ActionStrike, combat.ActionCounter:
		if mirror {
			return stickPose{Head: head, Torso: "<| ", Legs: legs}
		}
		return stickPose{Head: head, Torso: " |>", Legs: legs}
	case combat.ActionGrapple, combat.ActionBreak:
		return stickPose{Head: head, Torso: "/| ", Legs: legs}
	case combat.ActionBlock:
		return stickPose{Head: head, Torso: "[|]", Legs: legs}
	case combat.ActionFeint:
		return stickPose{Head: head, Torso: "~| ", Legs: legs}
	case combat.ActionDodge:
		if mirror {
			return stickPose{Head: head, Torso: "<| ", Legs: "< \\"}
		}
		return stickPose{Head: head, Torso: " |>", Legs: "/ >"}
	default:
		return stickPose{Head: head, Torso: "/|\\", Legs: legs}
	}
}

func recoverPose(action combat.Action, mirror bool) stickPose {
	head := " o "
	legs := "/ \\"
	switch action {
	case combat.ActionStrike:
		if mirror {
			return stickPose{Head: head, Torso: "\\| ", Legs: legs}
		}
		return stickPose{Head: head, Torso: " |/", Legs: legs}
	case combat.ActionDodge:
		if mirror {
			return stickPose{Head: head, Torso: " |<", Legs: "< /"}
		}
		return stickPose{Head: head, Torso: ">| ", Legs: "\\ >"}
	default:
		return stickPose{Head: head, Torso: "/|\\", Legs: legs}
	}
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

func approachDistance(action combat.Action) int {
	switch action {
	case combat.ActionStrike:
		return 7
	case combat.ActionGrapple:
		return 8
	case combat.ActionCounter:
		return 6
	case combat.ActionFeint:
		return 5
	case combat.ActionBreak:
		return 4
	case combat.ActionDodge:
		return 3
	case combat.ActionBlock:
		return 2
	default:
		return 1
	}
}

func hasEffect(effects []Effect, want Effect) bool {
	for _, effect := range effects {
		if effect == want {
			return true
		}
	}
	return false
}

func clampInt(v, minV, maxV int) int {
	if v < minV {
		return minV
	}
	if v > maxV {
		return maxV
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
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
