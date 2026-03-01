package matchmaking

import (
	"context"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/animation"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/npc"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	"github.com/google/uuid"
)

// Service coordinates queue and authoritative match execution.
type Service interface {
	Start(ctx context.Context)
	Stop()
	Enqueue(playerID string) error
	Dequeue(playerID string)
}

// MatchConfig controls queue and match timings.
type MatchConfig struct {
	QueueTimeout time.Duration
	TurnTimeout  time.Duration
	MaxTurns     int
}

// MatchResult summarizes one completed match.
type MatchResult struct {
	MatchID    string
	WinnerID   *string
	ResultType storage.MatchResultType
	StartedAt  time.Time
	EndedAt    time.Time
}

// MatchFinalizer persists authoritative match outcomes.
type MatchFinalizer interface {
	FinalizeMatch(ctx context.Context, params storage.FinalizeMatchParams) (storage.FinalizedMatch, error)
}

// Telemetry collects counters/timers from matchmaking flows.
type Telemetry interface {
	IncCounter(name string)
	ObserveDuration(name string, d time.Duration)
}

type queueLobby interface {
	lobby.Service
	PopNextPair(now time.Time, queueTimeout time.Duration) (pair [2]string, ok bool, timedOut []string)
	GetSession(playerID string) (player.Session, bool)
}

// InMemoryService is a concrete queue/match coordinator.
type InMemoryService struct {
	lobby        queueLobby
	finalizer    MatchFinalizer
	resolver     combat.Resolver
	cfg          MatchConfig
	telemetry    Telemetry
	newRenderer  func() animation.Renderer
	newNPCEngine func() npc.Engine

	nowFn  func() time.Time
	stopCh chan struct{}
	runCtx context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu      sync.Mutex
	running bool
	inMatch map[string]struct{}
}

func NewInMemoryService(lobbySvc queueLobby, finalizer MatchFinalizer, cfg MatchConfig, telemetry Telemetry) *InMemoryService {
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 45 * time.Second
	}
	if cfg.TurnTimeout <= 0 {
		cfg.TurnTimeout = 5 * time.Second
	}
	if cfg.MaxTurns <= 0 {
		cfg.MaxTurns = 120
	}
	return &InMemoryService{
		lobby:        lobbySvc,
		finalizer:    finalizer,
		resolver:     combat.NewStandardResolver(),
		cfg:          cfg,
		telemetry:    telemetry,
		newRenderer:  func() animation.Renderer { return animation.NewASCIIRenderer() },
		newNPCEngine: func() npc.Engine { return npc.NewProbabilisticEngine() },
		nowFn:        func() time.Time { return time.Now().UTC() },
		stopCh:       make(chan struct{}),
		runCtx:       context.Background(),
		inMatch:      make(map[string]struct{}),
	}
}

func (s *InMemoryService) Start(ctx context.Context) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	runCtx, cancel := context.WithCancel(ctx)
	s.running = true
	s.runCtx = runCtx
	s.cancel = cancel
	s.mu.Unlock()

	s.wg.Add(1)
	go s.loop(runCtx)
}

func (s *InMemoryService) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	close(s.stopCh)
	s.mu.Unlock()
	s.wg.Wait()
}

func (s *InMemoryService) Enqueue(playerID string) error {
	s.mu.Lock()
	_, busy := s.inMatch[playerID]
	s.mu.Unlock()
	if busy {
		return fmt.Errorf("player %s is already in a match", playerID)
	}
	if err := s.lobby.JoinQueue(playerID); err != nil {
		return err
	}
	if s.telemetry != nil {
		s.telemetry.IncCounter("queue_join")
	}
	if sess, ok := s.lobby.GetSession(playerID); ok {
		s.sendFrame(sess, "Entered queue. Waiting for opponent...")
	}
	return nil
}

func (s *InMemoryService) Dequeue(playerID string) {
	_ = s.lobby.LeaveQueue(playerID)
	if sess, ok := s.lobby.GetSession(playerID); ok {
		s.sendFrame(sess, "Left queue.")
	}
}

func (s *InMemoryService) IsPlayerInMatch(playerID string) bool {
	if playerID == "" {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.inMatch[playerID]
	return ok
}

func (s *InMemoryService) loop(ctx context.Context) {
	defer s.wg.Done()
	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			now := s.nowFn()
			s.processQueue(now)
		}
	}
}

func (s *InMemoryService) processQueue(now time.Time) {
	pair, ok, timedOut := s.lobby.PopNextPair(now, s.cfg.QueueTimeout)
	for _, playerID := range timedOut {
		if s.telemetry != nil {
			s.telemetry.IncCounter("queue_timeout")
		}
		if sess, exists := s.lobby.GetSession(playerID); exists {
			s.sendFrame(sess, "Queue timeout reached. Re-enter queue with q.")
		}
	}
	if !ok {
		return
	}
	if pair[0] == "" || pair[1] == "" || pair[0] == pair[1] {
		return
	}

	s.mu.Lock()
	if _, busy := s.inMatch[pair[0]]; busy {
		s.mu.Unlock()
		return
	}
	if _, busy := s.inMatch[pair[1]]; busy {
		s.mu.Unlock()
		return
	}
	s.inMatch[pair[0]] = struct{}{}
	s.inMatch[pair[1]] = struct{}{}
	matchCtx := s.runCtx
	s.mu.Unlock()

	sess1, ok1 := s.lobby.GetSession(pair[0])
	sess2, ok2 := s.lobby.GetSession(pair[1])
	if !ok1 || !ok2 {
		s.releasePlayers(pair[0], pair[1])
		if ok1 {
			s.requeueAfterPairFailure(sess1)
		}
		if ok2 {
			s.requeueAfterPairFailure(sess2)
		}
		return
	}

	if s.telemetry != nil {
		s.telemetry.IncCounter("matches_started")
	}

	s.wg.Add(1)
	go func(ctx context.Context) {
		defer s.wg.Done()
		s.runMatch(ctx, sess1, sess2)
		s.releasePlayers(pair[0], pair[1])
	}(matchCtx)
}

func (s *InMemoryService) runMatch(ctx context.Context, sess1, sess2 player.Session) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer drainSessionInput(sess1)
	defer drainSessionInput(sess2)

	startedAt := s.nowFn()
	s.sendFrame(sess1, "Match found!", fmt.Sprintf("Opponent: %s", sess2.Handle))
	s.sendFrame(sess2, "Match found!", fmt.Sprintf("Opponent: %s", sess1.Handle))

	fighter1, err := combat.NewFighter(sess1.PlayerID, pickArchetype(sess1.PlayerID, 0))
	if err != nil {
		s.sendFrame(sess1, "Failed to initialize fighter.")
		s.sendFrame(sess2, "Failed to initialize fighter.")
		return
	}
	fighter2, err := combat.NewFighter(sess2.PlayerID, pickArchetype(sess2.PlayerID, 1))
	if err != nil {
		s.sendFrame(sess1, "Failed to initialize fighter.")
		s.sendFrame(sess2, "Failed to initialize fighter.")
		return
	}

	seed := seedForPair(sess1.PlayerID, sess2.PlayerID, startedAt)
	initialState := combat.NewMatchState(fighter1, fighter2)
	replayTurns := make([]storage.MatchReplayTurnWrite, 0, s.cfg.MaxTurns)
	sim := engine.NewCombatSimulator(initialState, s.resolver, seed)
	renderer := s.newRenderer()
	npcEngine := s.newNPCEngine()
	slot1 := &matchSlot{
		session: sess1,
		npcRNG:  engine.NewDeterministicRNG(seedWithSalt(seed, npcSeedSaltP1)),
	}
	slot2 := &matchSlot{
		session: sess2,
		npcRNG:  engine.NewDeterministicRNG(seedWithSalt(seed, npcSeedSaltP2)),
	}

	for turn := 0; turn < s.cfg.MaxTurns; turn++ {
		select {
		case <-ctx.Done():
			return
		default:
		}

		state := sim.State()
		if state.Outcome.Finished {
			break
		}

		turnInputs, ok := s.collectTurnInputs(ctx, state, slot1, slot2, npcEngine)
		if !ok {
			return
		}
		canonicalInputs, err := combat.CanonicalizeInputs(state, turnInputs)
		if err != nil {
			s.sendFrame(sess1, "Match aborted: invalid turn inputs.")
			s.sendFrame(sess2, "Match aborted: invalid turn inputs.")
			return
		}

		result, err := sim.Step(canonicalInputs)
		if err != nil {
			s.sendFrame(sess1, "Match aborted: resolution error.")
			s.sendFrame(sess2, "Match aborted: resolution error.")
			return
		}
		relativeMS := s.nowFn().Sub(startedAt).Milliseconds()
		if relativeMS < 0 {
			relativeMS = 0
		}
		replayTurns = append(replayTurns, storage.MatchReplayTurnWrite{
			Turn:       result.Next.Turn,
			RelativeMS: relativeMS,
			Inputs:     canonicalInputs,
			Checksums:  result.Checksums,
		})

		frame := renderer.Render(sess1.Handle, sess2.Handle, result)
		payload := frame.Full
		if len(frame.Delta) > 0 {
			payload = frame.Delta
		}
		if len(frame.Effects) > 0 {
			effects := make([]string, 0, len(frame.Effects))
			for _, effect := range frame.Effects {
				effects = append(effects, string(effect))
			}
			payload = append(payload, fmt.Sprintf("effects: %s", strings.Join(effects, ",")))
		}
		s.sendFrame(sess1, payload...)
		s.sendFrame(sess2, payload...)
	}

	finalState := sim.State()
	endedAt := s.nowFn()
	resultType, winnerID := mapResult(finalState.Outcome)
	if !finalState.Outcome.Finished {
		resultType = storage.MatchResultDraw
		winnerID = nil
	}

	if s.finalizer != nil {
		_, err := s.finalizer.FinalizeMatch(ctx, storage.FinalizeMatchParams{
			MatchID:    uuid.NewString(),
			Player1ID:  sess1.PlayerID,
			Player2ID:  sess2.PlayerID,
			WinnerID:   winnerID,
			ResultType: resultType,
			StartedAt:  startedAt,
			EndedAt:    endedAt,
			Replay: &storage.MatchReplayWrite{
				Seed:         seed,
				InitialState: initialState,
				Turns:        replayTurns,
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			s.sendFrame(sess1, "Warning: failed to persist match result.")
			s.sendFrame(sess2, "Warning: failed to persist match result.")
		}
	}

	if s.telemetry != nil {
		s.telemetry.IncCounter("matches_finished")
		s.telemetry.ObserveDuration("match_duration", endedAt.Sub(startedAt))
	}

	if winnerID == nil {
		s.sendFrame(sess1, "Match ended in draw.")
		s.sendFrame(sess2, "Match ended in draw.")
		return
	}
	if *winnerID == sess1.PlayerID {
		s.sendFrame(sess1, "Victory!")
		s.sendFrame(sess2, "Defeat.")
	} else {
		s.sendFrame(sess1, "Defeat.")
		s.sendFrame(sess2, "Victory!")
	}
}

const (
	npcTakeoverTimeoutStreak = 2
	npcSeedSaltP1            = 0xA5A5A5A5A5A5A5A5
	npcSeedSaltP2            = 0x5A5A5A5A5A5A5A5A
)

type matchSlot struct {
	session       player.Session
	npcControlled bool
	timeoutStreak int
	npcRNG        npc.RandomSource
}

type turnInputStatus string

const (
	turnInputAction     turnInputStatus = "action"
	turnInputTimeout    turnInputStatus = "timeout"
	turnInputDisconnect turnInputStatus = "disconnect"
	turnInputCanceled   turnInputStatus = "canceled"
	turnInputNPC        turnInputStatus = "npc"
)

type turnInputResult struct {
	playerID string
	input    combat.TurnInput
	status   turnInputStatus
}

func (s *InMemoryService) collectTurnInputs(
	ctx context.Context,
	state combat.MatchState,
	slot1 *matchSlot,
	slot2 *matchSlot,
	npcEngine npc.Engine,
) ([]combat.TurnInput, bool) {
	results := make(chan turnInputResult, 2)

	collect := func(slot *matchSlot) {
		if slot.npcControlled {
			results <- turnInputResult{
				playerID: slot.session.PlayerID,
				input:    decideNPCInput(state, slot.session.PlayerID, npcEngine, slot.npcRNG),
				status:   turnInputNPC,
			}
			return
		}
		go func(sess player.Session) {
			results <- waitForTurnInput(ctx, sess, s.cfg.TurnTimeout)
		}(slot.session)
	}
	collect(slot1)
	collect(slot2)

	res1 := <-results
	res2 := <-results
	if res1.status == turnInputCanceled || res2.status == turnInputCanceled {
		return nil, false
	}

	orderedResults := []turnInputResult{res1, res2}
	turnInputs := make([]combat.TurnInput, 0, 2)
	for _, res := range orderedResults {
		slot := findSlotByPlayerID(res.playerID, slot1, slot2)
		if slot == nil {
			return nil, false
		}
		input, takeoverReason := resolveTurnInput(state, slot, res, npcEngine)
		if takeoverReason != "" {
			other := slot1
			if slot == slot1 {
				other = slot2
			}
			s.sendFrame(slot.session, fmt.Sprintf("NPC takeover enabled (%s).", takeoverReason))
			s.sendFrame(other.session, fmt.Sprintf("Opponent %s is now controlled by NPC (%s).", slot.session.Handle, takeoverReason))
		}
		turnInputs = append(turnInputs, input)
	}
	return turnInputs, true
}

func waitForTurnInput(ctx context.Context, sess player.Session, timeout time.Duration) turnInputResult {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()

	for {
		select {
		case <-ctx.Done():
			return turnInputResult{playerID: sess.PlayerID, status: turnInputCanceled}
		case cmd, ok := <-sess.Input:
			if !ok {
				return turnInputResult{
					playerID: sess.PlayerID,
					input:    combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
					status:   turnInputDisconnect,
				}
			}
			switch cmd.Kind {
			case player.CommandAction:
				decisionMS := int(time.Since(cmd.ReceivedAt).Milliseconds())
				if decisionMS < 0 {
					decisionMS = 0
				}
				return turnInputResult{
					playerID: sess.PlayerID,
					input:    combat.TurnInput{PlayerID: sess.PlayerID, Action: cmd.Action, Target: cmd.Target, DecisionMS: decisionMS},
					status:   turnInputAction,
				}
			case player.CommandQuit:
				return turnInputResult{
					playerID: sess.PlayerID,
					input:    combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
					status:   turnInputDisconnect,
				}
			default:
				// Non-action commands are ignored during turn collection.
			}
		case <-timer.C:
			return turnInputResult{
				playerID: sess.PlayerID,
				input:    combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
				status:   turnInputTimeout,
			}
		}
	}
}

func resolveTurnInput(
	state combat.MatchState,
	slot *matchSlot,
	result turnInputResult,
	npcEngine npc.Engine,
) (combat.TurnInput, string) {
	input := normalizeTurnInput(result.input, slot.session.PlayerID)
	switch result.status {
	case turnInputNPC:
		slot.npcControlled = true
		slot.timeoutStreak = 0
		return input, ""
	case turnInputAction:
		slot.timeoutStreak = 0
		return input, ""
	case turnInputTimeout:
		slot.timeoutStreak++
		if slot.timeoutStreak >= npcTakeoverTimeoutStreak {
			slot.npcControlled = true
			slot.timeoutStreak = 0
			return decideNPCInput(state, slot.session.PlayerID, npcEngine, slot.npcRNG), "timeout"
		}
		return input, ""
	case turnInputDisconnect:
		slot.npcControlled = true
		slot.timeoutStreak = 0
		return decideNPCInput(state, slot.session.PlayerID, npcEngine, slot.npcRNG), "disconnect"
	default:
		return combat.TurnInput{PlayerID: slot.session.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso}, ""
	}
}

func decideNPCInput(
	state combat.MatchState,
	playerID string,
	npcEngine npc.Engine,
	rng npc.RandomSource,
) combat.TurnInput {
	if npcEngine == nil {
		return combat.TurnInput{PlayerID: playerID, Action: combat.ActionNone, Target: combat.ZoneTorso}
	}
	input, err := npcEngine.Decide(state, playerID, rng)
	if err != nil {
		return combat.TurnInput{PlayerID: playerID, Action: combat.ActionNone, Target: combat.ZoneTorso}
	}
	return normalizeTurnInput(input, playerID)
}

func normalizeTurnInput(input combat.TurnInput, playerID string) combat.TurnInput {
	if input.PlayerID == "" {
		input.PlayerID = playerID
	}
	if input.Target != combat.ZoneHead && input.Target != combat.ZoneTorso && input.Target != combat.ZoneLegs {
		input.Target = combat.ZoneTorso
	}
	switch input.Action {
	case combat.ActionNone,
		combat.ActionStrike,
		combat.ActionGrapple,
		combat.ActionBlock,
		combat.ActionDodge,
		combat.ActionCounter,
		combat.ActionFeint,
		combat.ActionBreak:
		// valid
	default:
		input.Action = combat.ActionNone
	}
	return input
}

func findSlotByPlayerID(playerID string, slots ...*matchSlot) *matchSlot {
	for _, slot := range slots {
		if slot != nil && slot.session.PlayerID == playerID {
			return slot
		}
	}
	return nil
}

func (s *InMemoryService) requeueAfterPairFailure(sess player.Session) {
	if err := s.lobby.JoinQueue(sess.PlayerID); err != nil {
		s.sendFrame(sess, "Opponent unavailable. Re-enter queue with q.")
		return
	}
	s.sendFrame(sess, "Opponent unavailable. You were requeued.")
}

func drainSessionInput(sess player.Session) {
	for {
		select {
		case _, ok := <-sess.Input:
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func mapResult(outcome combat.MatchOutcome) (storage.MatchResultType, *string) {
	if !outcome.Finished {
		return storage.MatchResultDraw, nil
	}
	winner := outcome.WinnerID
	switch outcome.Method {
	case "KO", "ko":
		return storage.MatchResultKO, &winner
	case "Submission", "submission":
		return storage.MatchResultSubmission, &winner
	case "abandon", "Abandon":
		return storage.MatchResultAbandon, &winner
	default:
		if winner == "" {
			return storage.MatchResultDraw, nil
		}
		return storage.MatchResultKO, &winner
	}
}

func (s *InMemoryService) sendFrame(sess player.Session, lines ...string) {
	if len(lines) == 0 {
		return
	}
	frame := player.Frame{Lines: lines, Timestamp: s.nowFn()}
	select {
	case sess.Output <- frame:
	default:
	}
}

func (s *InMemoryService) releasePlayers(playerIDs ...string) {
	s.mu.Lock()
	for _, playerID := range playerIDs {
		delete(s.inMatch, playerID)
	}
	s.mu.Unlock()
}

func pickArchetype(playerID string, salt byte) combat.Archetype {
	h := fnv.New32a()
	_, _ = h.Write([]byte(playerID))
	_, _ = h.Write([]byte{salt})
	v := h.Sum32() % 4
	switch v {
	case 0:
		return combat.ArchetypeBalanced
	case 1:
		return combat.ArchetypePowerhouse
	case 2:
		return combat.ArchetypeTechnician
	default:
		return combat.ArchetypeHighFlyer
	}
}

func seedForPair(player1ID, player2ID string, now time.Time) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(player1ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(player2ID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(now.UTC().Format(time.RFC3339Nano)))
	seed := h.Sum64()
	if seed == 0 {
		seed = 1
	}
	return seed
}

func seedWithSalt(seed uint64, salt uint64) uint64 {
	mixed := seed ^ salt
	if mixed == 0 {
		mixed = 1
	}
	return mixed
}
