package matchmaking

import (
	"context"
	"fmt"
	"hash/fnv"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/animation"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/npc"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/spectator"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	telemetrypkg "github.com/Donotavio/Terminal-Wrestling-League/internal/telemetry"
	"github.com/google/uuid"
)

// Service coordinates queue and authoritative match execution.
type Service interface {
	Start(ctx context.Context)
	Stop()
	Enqueue(playerID string) error
	Dequeue(playerID string)
	StartNPCMatch(sess player.Session) error
	RunTutorial(ctx context.Context, sess player.Session) (storage.TutorialRun, error)
	WatchByHandle(ctx context.Context, spectatorSession player.Session, targetHandle string, waitTimeout time.Duration, maxSpectators int) error
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

type m5TelemetryStore interface {
	GetByHandle(ctx context.Context, handle string) (storage.Player, error)
	LoadAntiBotConfig(ctx context.Context) (storage.AntiBotConfig, error)
	CreateAntiBotFlag(ctx context.Context, flag storage.AntiBotFlag) (storage.AntiBotFlag, error)
	CreateNavigationTelemetryEvent(ctx context.Context, event storage.NavigationTelemetryEvent) (storage.NavigationTelemetryEvent, error)
	InsertTurnTelemetryBatch(ctx context.Context, rows []storage.MatchTurnTelemetry) error
	InsertMatchSummaryTelemetry(ctx context.Context, summary storage.MatchSummaryTelemetry) (storage.MatchSummaryTelemetry, error)
	CreateQueueTelemetryEvent(ctx context.Context, event storage.QueueTelemetryEvent) (storage.QueueTelemetryEvent, error)
	CreateSpectatorTelemetryEvent(ctx context.Context, event storage.SpectatorTelemetryEvent) (storage.SpectatorTelemetryEvent, error)
	CreateTutorialRun(ctx context.Context, run storage.TutorialRun) (storage.TutorialRun, error)
	MarkTutorialCompleted(ctx context.Context, playerID string, now time.Time) (storage.PlayerProfile, error)
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
	m5Store      m5TelemetryStore
	resolver     combat.Resolver
	cfg          MatchConfig
	telemetry    Telemetry
	newRenderer  func() animation.Renderer
	newNPCEngine func() npc.Engine
	spectators   *spectator.Hub

	nowFn  func() time.Time
	stopCh chan struct{}
	runCtx context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	mu             sync.Mutex
	running        bool
	inMatch        map[string]struct{}
	queueJoinedAt  map[string]time.Time
	activeMatches  map[string]*activeMatch
	activeByHandle map[string]string
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
	var m5Store m5TelemetryStore
	if typed, ok := finalizer.(m5TelemetryStore); ok {
		m5Store = typed
	}

	return &InMemoryService{
		lobby:          lobbySvc,
		finalizer:      finalizer,
		m5Store:        m5Store,
		resolver:       combat.NewStandardResolver(),
		cfg:            cfg,
		telemetry:      telemetry,
		newRenderer:    func() animation.Renderer { return animation.NewASCIIRenderer() },
		newNPCEngine:   func() npc.Engine { return npc.NewProbabilisticEngine() },
		spectators:     spectator.NewHub(),
		nowFn:          func() time.Time { return time.Now().UTC() },
		stopCh:         make(chan struct{}),
		runCtx:         context.Background(),
		inMatch:        make(map[string]struct{}),
		queueJoinedAt:  make(map[string]time.Time),
		activeMatches:  make(map[string]*activeMatch),
		activeByHandle: make(map[string]string),
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
	s.mu.Lock()
	if _, exists := s.queueJoinedAt[playerID]; !exists {
		s.queueJoinedAt[playerID] = s.nowFn()
	}
	s.mu.Unlock()
	s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
		PlayerID:    playerID,
		EventType:   "join",
		QueueWaitMS: 0,
	})
	if s.telemetry != nil {
		s.telemetry.IncCounter("queue_join")
	}
	if sess, ok := s.lobby.GetSession(playerID); ok {
		s.sendFrame(sess, "Entered queue. Waiting for opponent...")
	}
	return nil
}

func (s *InMemoryService) Dequeue(playerID string) {
	s.mu.Lock()
	joinedAt, exists := s.queueJoinedAt[playerID]
	if exists {
		delete(s.queueJoinedAt, playerID)
	}
	s.mu.Unlock()
	_ = s.lobby.LeaveQueue(playerID)
	waitMS := int64(0)
	if exists {
		waitMS = s.nowFn().Sub(joinedAt).Milliseconds()
		if waitMS < 0 {
			waitMS = 0
		}
	}
	s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
		PlayerID:    playerID,
		EventType:   "leave",
		QueueWaitMS: waitMS,
	})
	if sess, ok := s.lobby.GetSession(playerID); ok {
		s.sendFrame(sess, "Left queue.")
	}
}

func (s *InMemoryService) StartNPCMatch(sess player.Session) error {
	if sess.PlayerID == "" {
		return fmt.Errorf("player id is required")
	}
	if _, ok := s.lobby.GetSession(sess.PlayerID); !ok {
		return fmt.Errorf("player %s is not registered", sess.PlayerID)
	}

	s.mu.Lock()
	if _, busy := s.inMatch[sess.PlayerID]; busy {
		s.mu.Unlock()
		return fmt.Errorf("player %s is already in a match", sess.PlayerID)
	}
	s.inMatch[sess.PlayerID] = struct{}{}
	joinedAt, queued := s.queueJoinedAt[sess.PlayerID]
	if queued {
		delete(s.queueJoinedAt, sess.PlayerID)
	}
	matchCtx := s.runCtx
	s.mu.Unlock()

	_ = s.lobby.LeaveQueue(sess.PlayerID)
	if queued {
		waitMS := s.nowFn().Sub(joinedAt).Milliseconds()
		if waitMS < 0 {
			waitMS = 0
		}
		s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
			PlayerID:    sess.PlayerID,
			EventType:   "leave",
			QueueWaitMS: waitMS,
		})
	}

	s.sendFrame(sess,
		"Practice match started against Coach NPC.",
		"Send actions with: a <action> <zone>",
	)
	s.persistNavigationEvent(context.Background(), storage.NavigationTelemetryEvent{
		PlayerID:  sess.PlayerID,
		State:     storage.NavigationStateLobby,
		EventType: "practice_started",
		Source:    storage.NavigationSourceSystem,
		Detail:    map[string]any{"mode": "npc"},
	})
	if s.telemetry != nil {
		s.telemetry.IncCounter("npc_matches_started")
	}

	if matchCtx == nil {
		matchCtx = context.Background()
	}
	s.wg.Add(1)
	go func(ctx context.Context) {
		defer s.wg.Done()
		s.runNPCMatch(ctx, sess)
		s.releasePlayers(sess.PlayerID)
	}(matchCtx)

	return nil
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
		waitMS := s.queueWaitAndClear(playerID, now)
		s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
			PlayerID:    playerID,
			EventType:   "timeout",
			QueueWaitMS: waitMS,
		})
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
	waitP1 := s.queueWaitAndClear(pair[0], now)
	waitP2 := s.queueWaitAndClear(pair[1], now)
	s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
		PlayerID:    pair[0],
		EventType:   "matched",
		QueueWaitMS: waitP1,
	})
	s.persistQueueEvent(context.Background(), storage.QueueTelemetryEvent{
		PlayerID:    pair[1],
		EventType:   "matched",
		QueueWaitMS: waitP2,
	})
	s.persistNavigationEvent(context.Background(), storage.NavigationTelemetryEvent{
		PlayerID:  pair[0],
		State:     storage.NavigationStateQueue,
		EventType: "queue_matched",
		Source:    storage.NavigationSourceSystem,
		Detail: map[string]any{
			"queue_wait_ms": waitP1,
			"opponent_id":   pair[1],
		},
	})
	s.persistNavigationEvent(context.Background(), storage.NavigationTelemetryEvent{
		PlayerID:  pair[1],
		State:     storage.NavigationStateQueue,
		EventType: "queue_matched",
		Source:    storage.NavigationSourceSystem,
		Detail: map[string]any{
			"queue_wait_ms": waitP2,
			"opponent_id":   pair[0],
		},
	})

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
		s.runMatch(ctx, sess1, sess2, waitP1, waitP2)
		s.releasePlayers(pair[0], pair[1])
	}(matchCtx)
}

func (s *InMemoryService) runMatch(ctx context.Context, sess1, sess2 player.Session, queueWaitMSP1, queueWaitMSP2 int64) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer drainSessionInput(sess1)
	defer drainSessionInput(sess2)

	startedAt := s.nowFn()
	s.sendFrame(sess1, "Match found!", fmt.Sprintf("Opponent: %s", sess2.Handle))
	s.sendFrame(sess2, "Match found!", fmt.Sprintf("Opponent: %s", sess1.Handle))
	s.persistNavigationEvent(context.Background(), storage.NavigationTelemetryEvent{
		PlayerID:  sess1.PlayerID,
		State:     storage.NavigationStateMatch,
		EventType: "pvp_started",
		Source:    storage.NavigationSourceSystem,
		Detail: map[string]any{
			"opponent_id": sess2.PlayerID,
		},
	})
	s.persistNavigationEvent(context.Background(), storage.NavigationTelemetryEvent{
		PlayerID:  sess2.PlayerID,
		State:     storage.NavigationStateMatch,
		EventType: "pvp_started",
		Source:    storage.NavigationSourceSystem,
		Detail: map[string]any{
			"opponent_id": sess1.PlayerID,
		},
	})

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
	matchID := uuid.NewString()
	initialState := combat.NewMatchState(fighter1, fighter2)
	replayTurns := make([]storage.MatchReplayTurnWrite, 0, s.cfg.MaxTurns)
	sim := engine.NewCombatSimulator(initialState, s.resolver, seed)
	renderer := s.newRenderer()
	npcEngine := s.newNPCEngine()
	antiBotCfg := storage.DefaultAntiBotConfig()
	if s.m5Store != nil {
		cfg, err := s.m5Store.LoadAntiBotConfig(ctx)
		if err == nil {
			antiBotCfg = cfg
		}
	}
	turnTelemetryRows := make([]storage.MatchTurnTelemetry, 0, s.cfg.MaxTurns*2)
	playerObs := map[string][]telemetrypkg.TurnObservation{
		sess1.PlayerID: {},
		sess2.PlayerID: {},
	}
	staminaSum := map[string]float64{
		sess1.PlayerID: 0,
		sess2.PlayerID: 0,
	}
	momentumSum := map[string]float64{
		sess1.PlayerID: 0,
		sess2.PlayerID: 0,
	}
	comboCount := map[string]int{
		sess1.PlayerID: 0,
		sess2.PlayerID: 0,
	}
	stunEvents := 0
	submissionAttempts := 0
	match := &activeMatch{
		MatchID:  matchID,
		P1ID:     sess1.PlayerID,
		P2ID:     sess2.PlayerID,
		P1Handle: sess1.Handle,
		P2Handle: sess2.Handle,
		Done:     make(chan struct{}),
	}
	s.registerActiveMatch(match)
	defer s.unregisterActiveMatch(match)
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
		if !s.emitCinematicSequence(ctx, frame, matchID, sess1, sess2) {
			return
		}

		for _, e := range result.Events {
			if e.Type == combat.EventStatusApplied && e.Detail == "stunned" {
				stunEvents++
			}
		}
		if state.CombatState != combat.StateSubmissionAttempt && result.Next.CombatState == combat.StateSubmissionAttempt {
			submissionAttempts++
		}

		turnRows, perTurnObs := s.buildTurnTelemetryRows(matchID, state, result.Next, canonicalInputs, result.Events, slot1, slot2, antiBotCfg.OptimalityEpsilon)
		turnTelemetryRows = append(turnTelemetryRows, turnRows...)
		playerObs[sess1.PlayerID] = append(playerObs[sess1.PlayerID], perTurnObs[sess1.PlayerID]...)
		playerObs[sess2.PlayerID] = append(playerObs[sess2.PlayerID], perTurnObs[sess2.PlayerID]...)
		staminaSum[sess1.PlayerID] += float64(result.Next.P1.Stamina)
		staminaSum[sess2.PlayerID] += float64(result.Next.P2.Stamina)
		momentumSum[sess1.PlayerID] += float64(result.Next.P1.Momentum)
		momentumSum[sess2.PlayerID] += float64(result.Next.P2.Momentum)
		for _, ev := range result.Events {
			if ev.Type == combat.EventStatusApplied && ev.Detail == "combo_active" {
				comboCount[ev.PlayerID]++
			}
		}
	}

	finalState := sim.State()
	endedAt := s.nowFn()
	resultType, winnerID := mapResult(finalState.Outcome)
	if !finalState.Outcome.Finished {
		resultType = storage.MatchResultDraw
		winnerID = nil
	}

	if s.finalizer != nil {
		finalized, err := s.finalizer.FinalizeMatch(ctx, storage.FinalizeMatchParams{
			MatchID:    matchID,
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
		} else {
			s.persistMatchTelemetry(ctx, finalized, turnTelemetryRows, playerObs, antiBotCfg, comboCount, stunEvents, submissionAttempts, staminaSum, momentumSum, queueWaitMSP1, queueWaitMSP2)
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

func (s *InMemoryService) runNPCMatch(ctx context.Context, sess player.Session) {
	if ctx == nil {
		ctx = context.Background()
	}
	defer drainSessionInput(sess)

	startedAt := s.nowFn()
	npcID := "npc_practice_" + sess.PlayerID
	playerFighter, err := combat.NewFighter(sess.PlayerID, pickArchetype(sess.PlayerID, 0))
	if err != nil {
		s.sendFrame(sess, "Practice aborted: failed to initialize player fighter.")
		return
	}
	npcFighter, err := combat.NewFighter(npcID, combat.ArchetypeTechnician)
	if err != nil {
		s.sendFrame(sess, "Practice aborted: failed to initialize npc fighter.")
		return
	}

	seed := seedWithSalt(seedForPair(sess.PlayerID, npcID, startedAt), 0xBADC0DE)
	sim := engine.NewCombatSimulator(combat.NewMatchState(playerFighter, npcFighter), s.resolver, seed)
	renderer := s.newRenderer()
	npcEngine := s.newNPCEngine()

	playerSlot := &matchSlot{
		session: sess,
		npcRNG:  engine.NewDeterministicRNG(seedWithSalt(seed, npcSeedSaltP1)),
	}
	npcSlot := &matchSlot{
		session: player.Session{
			PlayerID: npcID,
			Handle:   "coach-npc",
		},
		npcControlled: true,
		npcRNG:        engine.NewDeterministicRNG(seedWithSalt(seed, npcSeedSaltP2)),
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

		turnInputs, ok := s.collectTurnInputs(ctx, state, playerSlot, npcSlot, npcEngine)
		if !ok {
			return
		}
		canonicalInputs, err := combat.CanonicalizeInputs(state, turnInputs)
		if err != nil {
			s.sendFrame(sess, "Practice aborted: invalid turn inputs.")
			return
		}

		result, err := sim.Step(canonicalInputs)
		if err != nil {
			s.sendFrame(sess, "Practice aborted: resolution error.")
			return
		}
		frame := renderer.Render(sess.Handle, "Coach NPC", result)
		if !s.emitCinematicSequence(ctx, frame, "", sess) {
			return
		}
	}

	endedAt := s.nowFn()
	if s.telemetry != nil {
		s.telemetry.IncCounter("npc_matches_finished")
		s.telemetry.ObserveDuration("npc_match_duration", endedAt.Sub(startedAt))
	}

	resultType, winnerID := mapResult(sim.State().Outcome)
	if winnerID == nil || resultType == storage.MatchResultDraw {
		s.sendFrame(sess, "Practice result: draw.")
		return
	}
	if *winnerID == sess.PlayerID {
		s.sendFrame(sess, "Practice result: victory.")
		return
	}
	s.sendFrame(sess, "Practice result: defeat.")
}

const (
	npcTakeoverTimeoutStreak = 2
	npcSeedSaltP1            = 0xA5A5A5A5A5A5A5A5
	npcSeedSaltP2            = 0x5A5A5A5A5A5A5A5A
	tutorialMaxTurns         = 8
	cinematicFrameDelay      = 140 * time.Millisecond
	cinematicSlowmoDelay     = 260 * time.Millisecond
	cinematicTurnSettleDelay = 220 * time.Millisecond
	ansiClearHome            = "\x1b[2J\x1b[H"
)

type activeMatch struct {
	MatchID  string
	P1ID     string
	P2ID     string
	P1Handle string
	P2Handle string
	Done     chan struct{}
}

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

	orderedResults, ok := orderTurnResultsBySlot(slot1, slot2, res1, res2)
	if !ok {
		return nil, false
	}
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

type payloadStep struct {
	Lines []string
	Delay time.Duration
}

func buildCinematicSequence(frame animation.Frame) []payloadStep {
	baseFrames := frame.Keyframes
	if len(baseFrames) == 0 && len(frame.Full) > 0 {
		baseFrames = [][]string{frame.Full}
	}
	if len(baseFrames) == 0 {
		return nil
	}

	slowmo := containsEffect(frame.Effects, animation.EffectSlowmo)
	steps := make([]payloadStep, 0, len(baseFrames))
	for i, keyframe := range baseFrames {
		lines := make([]string, 0, len(keyframe)+1)
		lines = append(lines, ansiClearHome)
		lines = append(lines, keyframe...)

		delay := cinematicTurnSettleDelay
		if i < len(baseFrames)-1 {
			delay = cinematicFrameDelay
			if slowmo && i == len(baseFrames)-2 {
				delay = cinematicSlowmoDelay
			}
		} else if slowmo {
			delay = cinematicSlowmoDelay
		}
		steps = append(steps, payloadStep{
			Lines: lines,
			Delay: delay,
		})
	}
	return steps
}

func containsEffect(effects []animation.Effect, want animation.Effect) bool {
	for _, effect := range effects {
		if effect == want {
			return true
		}
	}
	return false
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return true
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (s *InMemoryService) emitCinematicSequence(ctx context.Context, frame animation.Frame, spectatorMatchID string, sessions ...player.Session) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	steps := buildCinematicSequence(frame)
	for _, step := range steps {
		for _, sess := range sessions {
			s.sendFrame(sess, step.Lines...)
		}
		if spectatorMatchID != "" {
			s.spectators.Broadcast(spectatorMatchID, step.Lines)
		}
		if !sleepWithContext(ctx, step.Delay) {
			return false
		}
	}
	return true
}

func orderTurnResultsBySlot(slot1, slot2 *matchSlot, results ...turnInputResult) ([]turnInputResult, bool) {
	if slot1 == nil || slot2 == nil {
		return nil, false
	}

	resultByPlayer := make(map[string]turnInputResult, len(results))
	for _, result := range results {
		if result.playerID == "" {
			return nil, false
		}
		if _, exists := resultByPlayer[result.playerID]; exists {
			return nil, false
		}
		resultByPlayer[result.playerID] = result
	}

	ordered := make([]turnInputResult, 0, 2)
	for _, slot := range []*matchSlot{slot1, slot2} {
		result, ok := resultByPlayer[slot.session.PlayerID]
		if !ok {
			return nil, false
		}
		ordered = append(ordered, result)
	}
	return ordered, true
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

func (s *InMemoryService) queueWaitAndClear(playerID string, now time.Time) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	joinedAt, exists := s.queueJoinedAt[playerID]
	if exists {
		delete(s.queueJoinedAt, playerID)
	}
	if !exists {
		return 0
	}
	waitMS := now.Sub(joinedAt).Milliseconds()
	if waitMS < 0 {
		waitMS = 0
	}
	return waitMS
}

func (s *InMemoryService) persistQueueEvent(ctx context.Context, event storage.QueueTelemetryEvent) {
	if s.m5Store == nil {
		return
	}
	_, _ = s.m5Store.CreateQueueTelemetryEvent(ctx, event)
}

func (s *InMemoryService) persistNavigationEvent(ctx context.Context, event storage.NavigationTelemetryEvent) {
	if s.m5Store == nil {
		return
	}
	if event.Detail == nil {
		event.Detail = map[string]any{}
	}
	_, _ = s.m5Store.CreateNavigationTelemetryEvent(ctx, event)
}

func (s *InMemoryService) registerActiveMatch(match *activeMatch) {
	if match == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.activeMatches[match.MatchID] = match
	s.activeByHandle[strings.ToLower(match.P1Handle)] = match.MatchID
	s.activeByHandle[strings.ToLower(match.P2Handle)] = match.MatchID
	s.spectators.RegisterMatch(match.MatchID)
}

func (s *InMemoryService) unregisterActiveMatch(match *activeMatch) {
	if match == nil {
		return
	}
	close(match.Done)
	s.mu.Lock()
	delete(s.activeMatches, match.MatchID)
	delete(s.activeByHandle, strings.ToLower(match.P1Handle))
	delete(s.activeByHandle, strings.ToLower(match.P2Handle))
	s.mu.Unlock()
	s.spectators.UnregisterMatch(match.MatchID)
}

func (s *InMemoryService) lookupActiveMatchByHandle(handle string) (*activeMatch, bool) {
	handle = strings.ToLower(strings.TrimSpace(handle))
	if handle == "" {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	matchID, ok := s.activeByHandle[handle]
	if !ok {
		return nil, false
	}
	match, exists := s.activeMatches[matchID]
	return match, exists
}

func (s *InMemoryService) buildTurnTelemetryRows(
	matchID string,
	before combat.MatchState,
	after combat.MatchState,
	inputs []combat.TurnInput,
	events []combat.Event,
	slot1 *matchSlot,
	slot2 *matchSlot,
	optimalityEpsilon float64,
) ([]storage.MatchTurnTelemetry, map[string][]telemetrypkg.TurnObservation) {
	rows := make([]storage.MatchTurnTelemetry, 0, len(inputs))
	obsByPlayer := make(map[string][]telemetrypkg.TurnObservation, 2)

	for _, input := range inputs {
		beforeF, afterF, opponent, ok := fightersBeforeAfterByID(before, after, input.PlayerID)
		if !ok {
			continue
		}
		slot := findSlotByPlayerID(input.PlayerID, slot1, slot2)
		isHuman := slot != nil && !slot.npcControlled
		success := findActionSuccess(events, input.PlayerID)
		optimalAction, optimalZone, chosenU, bestU := optimalChoice(before, *beforeF, *opponent, input)
		isOptimal := chosenU >= (bestU - optimalityEpsilon)
		if !isHuman {
			isOptimal = false
		}

		row := storage.MatchTurnTelemetry{
			MatchID:           matchID,
			Turn:              after.Turn,
			PlayerID:          input.PlayerID,
			IsHuman:           isHuman,
			Action:            input.Action.String(),
			TargetZone:        input.Target.String(),
			DecisionMS:        maxInt(input.DecisionMS, 0),
			Success:           success,
			HPBefore:          beforeF.HP,
			HPAfter:           afterF.HP,
			StaminaBefore:     beforeF.Stamina,
			StaminaAfter:      afterF.Stamina,
			MomentumBefore:    beforeF.Momentum,
			MomentumAfter:     afterF.Momentum,
			IsOptimalChoice:   isOptimal,
			OptimalAction:     optimalAction.String(),
			OptimalTargetZone: optimalZone.String(),
		}
		rows = append(rows, row)
		if isHuman {
			obsByPlayer[input.PlayerID] = append(obsByPlayer[input.PlayerID], telemetrypkg.TurnObservation{
				DecisionMS: row.DecisionMS,
				IsOptimal:  row.IsOptimalChoice,
			})
		}
	}
	return rows, obsByPlayer
}

func fightersBeforeAfterByID(before, after combat.MatchState, playerID string) (beforeF, afterF, opponent *combat.FighterState, ok bool) {
	switch playerID {
	case before.P1.PlayerID:
		return &before.P1, &after.P1, &before.P2, true
	case before.P2.PlayerID:
		return &before.P2, &after.P2, &before.P1, true
	default:
		return nil, nil, nil, false
	}
}

func findActionSuccess(events []combat.Event, playerID string) bool {
	for _, e := range events {
		if e.Type == combat.EventActionResolved && e.PlayerID == playerID {
			return e.Success
		}
	}
	return false
}

func optimalChoice(state combat.MatchState, actor, defender combat.FighterState, chosen combat.TurnInput) (combat.Action, combat.Zone, float64, float64) {
	actions := []combat.Action{
		combat.ActionStrike,
		combat.ActionGrapple,
		combat.ActionBlock,
		combat.ActionDodge,
		combat.ActionCounter,
		combat.ActionFeint,
		combat.ActionBreak,
	}
	zones := []combat.Zone{combat.ZoneHead, combat.ZoneTorso, combat.ZoneLegs}
	bestU := math.Inf(-1)
	bestAction := combat.ActionNone
	bestZone := combat.ZoneTorso
	chosenU := math.Inf(-1)

	for _, action := range actions {
		for _, zone := range zones {
			u := estimateUtility(state, actor, defender, action, zone)
			if action == chosen.Action && zone == chosen.Target {
				chosenU = u
			}
			if u > bestU {
				bestU = u
				bestAction = action
				bestZone = zone
			}
		}
	}
	if math.IsInf(chosenU, -1) {
		chosenU = estimateUtility(state, actor, defender, chosen.Action, chosen.Target)
	}
	return bestAction, bestZone, chosenU, bestU
}

func estimateUtility(state combat.MatchState, actor, defender combat.FighterState, action combat.Action, _ combat.Zone) float64 {
	attackerAttr := combat.ActionRelevantAttribute(action, actor.Stats)
	defenderAttr := defensiveAttributeForUtility(action, defender)
	chance := float64(combat.SuccessChanceBPS(attackerAttr, defenderAttr, actor.Momentum, actor.Stamina)) / 10000.0
	baseDamage := combat.BaseDamage(action, actor.Stats)
	expectedDamage := chance * float64(combat.DamageFinal(baseDamage, actor.Momentum, false))

	counterChance := float64(combat.SuccessChanceBPS(
		combat.ActionRelevantAttribute(combat.ActionStrike, defender.Stats),
		combat.ActionRelevantAttribute(combat.ActionBlock, actor.Stats),
		defender.Momentum,
		defender.Stamina,
	)) / 10000.0
	expectedReceived := counterChance * float64(combat.DamageFinal(combat.BaseDamage(combat.ActionStrike, defender.Stats), defender.Momentum, false))
	if action == combat.ActionBlock || action == combat.ActionDodge {
		expectedReceived *= 0.7
	}

	momentumGain := 0.0
	if combat.IsOffensive(action) {
		momentumGain = 8.0 * chance
	}
	momentumLoss := 6.0 * chance
	deltaMomentum := momentumGain - momentumLoss
	staminaCost := float64(combat.ActionStaminaCost(action, actor.Stats.Endurance))

	u := expectedDamage - 0.7*expectedReceived + 0.2*deltaMomentum - 0.1*staminaCost
	if state.CombatState == combat.StateSubmissionAttempt && action == combat.ActionBreak {
		u += 2.0
	}
	return u
}

func defensiveAttributeForUtility(action combat.Action, defender combat.FighterState) int {
	switch action {
	case combat.ActionStrike:
		return combat.ClampInt(defender.Stats.Agility, 1, 10)
	case combat.ActionGrapple, combat.ActionCounter, combat.ActionFeint:
		return combat.ClampInt(defender.Stats.Technique, 1, 10)
	case combat.ActionBreak:
		return combat.ClampInt(defender.Stats.Power, 1, 10)
	default:
		return combat.ClampInt(defender.Stats.Technique, 1, 10)
	}
}

func (s *InMemoryService) persistMatchTelemetry(
	ctx context.Context,
	finalized storage.FinalizedMatch,
	turnRows []storage.MatchTurnTelemetry,
	playerObs map[string][]telemetrypkg.TurnObservation,
	antiBotCfg storage.AntiBotConfig,
	comboCount map[string]int,
	stunEvents int,
	submissionAttempts int,
	staminaSum map[string]float64,
	momentumSum map[string]float64,
	queueWaitMSP1 int64,
	queueWaitMSP2 int64,
) {
	if s.m5Store == nil {
		return
	}
	if len(turnRows) > 0 {
		_ = s.m5Store.InsertTurnTelemetryBatch(ctx, turnRows)
	}
	turnCount := maxInt(len(turnRows)/2, 1)
	summary := storage.MatchSummaryTelemetry{
		MatchID:            finalized.Match.ID,
		SeasonID:           finalized.Season.ID,
		Player1ID:          finalized.Player1ID,
		Player2ID:          finalized.Player2ID,
		WinnerID:           finalized.Match.WinnerID,
		ResultType:         string(finalized.Match.ResultType),
		TurnCount:          turnCount,
		DurationMS:         finalized.Match.DurationMS,
		AvgDecisionMSP1:    avgDecisionMS(playerObs[finalized.Player1ID]),
		AvgDecisionMSP2:    avgDecisionMS(playerObs[finalized.Player2ID]),
		AvgStaminaP1:       staminaSum[finalized.Player1ID] / float64(turnCount),
		AvgStaminaP2:       staminaSum[finalized.Player2ID] / float64(turnCount),
		AvgMomentumP1:      momentumSum[finalized.Player1ID] / float64(turnCount),
		AvgMomentumP2:      momentumSum[finalized.Player2ID] / float64(turnCount),
		MaxComboP1:         comboCount[finalized.Player1ID],
		MaxComboP2:         comboCount[finalized.Player2ID],
		StunEvents:         stunEvents,
		SubmissionAttempts: submissionAttempts,
		QueueWaitMSP1:      queueWaitMSP1,
		QueueWaitMSP2:      queueWaitMSP2,
	}
	_, _ = s.m5Store.InsertMatchSummaryTelemetry(ctx, summary)

	for _, playerID := range []string{finalized.Player1ID, finalized.Player2ID} {
		observations := playerObs[playerID]
		if len(observations) == 0 {
			continue
		}
		eval := telemetrypkg.EvaluateAntiBot(telemetrypkg.ComputeAntiBotMetrics(observations), antiBotCfg)
		_, _ = s.m5Store.CreateAntiBotFlag(ctx, storage.AntiBotFlag{
			PlayerID:            playerID,
			SeasonID:            finalized.Season.ID,
			MatchID:             finalized.Match.ID,
			DecisionCount:       eval.Metrics.DecisionCount,
			MeanDecisionMS:      eval.Metrics.MeanDecisionMS,
			DecisionVarianceMS2: eval.Metrics.DecisionVarianceMS2,
			OptimalPickRate:     eval.Metrics.OptimalPickRate,
			SuspicionScore:      eval.SuspicionScore,
			Flagged:             eval.Flagged,
			Reason:              eval.Reason,
		})
	}
}

func avgDecisionMS(observations []telemetrypkg.TurnObservation) float64 {
	if len(observations) == 0 {
		return 0
	}
	sum := 0
	for _, obs := range observations {
		sum += obs.DecisionMS
	}
	return float64(sum) / float64(len(observations))
}

func maxInt(v, minV int) int {
	if v < minV {
		return minV
	}
	return v
}

func (s *InMemoryService) RunTutorial(ctx context.Context, sess player.Session) (storage.TutorialRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	startedAt := s.nowFn()
	npcID := "npc_tutorial_" + sess.PlayerID
	playerFighter, err := combat.NewFighter(sess.PlayerID, combat.ArchetypeBalanced)
	if err != nil {
		return storage.TutorialRun{}, fmt.Errorf("create tutorial fighter: %w", err)
	}
	npcFighter, err := combat.NewFighter(npcID, combat.ArchetypeTechnician)
	if err != nil {
		return storage.TutorialRun{}, fmt.Errorf("create tutorial npc fighter: %w", err)
	}

	seed := seedWithSalt(seedForPair(sess.PlayerID, npcID, startedAt), 0xC0FFEE)
	state := combat.NewMatchState(playerFighter, npcFighter)
	sim := engine.NewCombatSimulator(state, s.resolver, seed)
	renderer := s.newRenderer()
	rng := engine.NewDeterministicRNG(seedWithSalt(seed, 0x1234))

	for turn := 0; turn < tutorialMaxTurns; turn++ {
		select {
		case <-ctx.Done():
			return storage.TutorialRun{}, ctx.Err()
		default:
		}
		curr := sim.State()
		if curr.Outcome.Finished {
			break
		}
		playerInput := combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionStrike, Target: combat.ZoneTorso, DecisionMS: 250}
		npcInput := decideTutorialNPCInput(curr, npcID, rng)
		canonical, err := combat.CanonicalizeInputs(curr, []combat.TurnInput{playerInput, npcInput})
		if err != nil {
			return storage.TutorialRun{}, fmt.Errorf("canonicalize tutorial inputs: %w", err)
		}
		result, err := sim.Step(canonical)
		if err != nil {
			return storage.TutorialRun{}, fmt.Errorf("run tutorial step: %w", err)
		}
		frame := renderer.Render(sess.Handle, "Coach NPC", result)
		if !s.emitCinematicSequence(ctx, frame, "", sess) {
			return storage.TutorialRun{}, ctx.Err()
		}
	}

	finalState := sim.State()
	result := storage.TutorialResultDraw
	switch {
	case !finalState.Outcome.Finished:
		result = storage.TutorialResultDraw
	case finalState.Outcome.WinnerID == sess.PlayerID:
		result = storage.TutorialResultWin
	case finalState.Outcome.WinnerID == npcID:
		result = storage.TutorialResultLoss
	default:
		result = storage.TutorialResultDraw
	}
	endedAt := s.nowFn()
	run := storage.TutorialRun{
		PlayerID:   sess.PlayerID,
		Result:     result,
		StartedAt:  startedAt,
		EndedAt:    endedAt,
		DurationMS: int(endedAt.Sub(startedAt).Milliseconds()),
	}
	if s.m5Store != nil {
		created, err := s.m5Store.CreateTutorialRun(ctx, run)
		if err == nil {
			run = created
		}
		_, _ = s.m5Store.MarkTutorialCompleted(ctx, sess.PlayerID, endedAt)
	}
	return run, nil
}

func decideTutorialNPCInput(state combat.MatchState, npcID string, rng npc.RandomSource) combat.TurnInput {
	self, opponent, err := tutorialFighters(state, npcID)
	if err != nil {
		return combat.TurnInput{PlayerID: npcID, Action: combat.ActionNone, Target: combat.ZoneTorso}
	}
	action := combat.ActionDodge
	target := combat.ZoneTorso

	if self.HP > opponent.HP+10 {
		action = combat.ActionStrike
		target = combat.ZoneHead
	} else if self.Stamina < 35 {
		action = combat.ActionBlock
	} else if rng.NextInt(100) < 25 {
		action = combat.ActionGrapple
		target = combat.ZoneLegs
	}
	return combat.TurnInput{
		PlayerID:   npcID,
		Action:     action,
		Target:     target,
		DecisionMS: 250,
	}
}

func tutorialFighters(state combat.MatchState, npcID string) (*combat.FighterState, *combat.FighterState, error) {
	if state.P1.PlayerID == npcID {
		return &state.P1, &state.P2, nil
	}
	if state.P2.PlayerID == npcID {
		return &state.P2, &state.P1, nil
	}
	return nil, nil, fmt.Errorf("npc id not present")
}

func (s *InMemoryService) WatchByHandle(ctx context.Context, spectatorSession player.Session, targetHandle string, waitTimeout time.Duration, maxSpectators int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	targetHandle = strings.TrimSpace(targetHandle)
	if targetHandle == "" {
		return fmt.Errorf("target handle is required")
	}
	targetPlayerID, err := s.resolveTargetPlayerID(ctx, targetHandle)
	if err != nil {
		return fmt.Errorf("target handle %q not found", targetHandle)
	}
	if waitTimeout <= 0 {
		waitTimeout = 120 * time.Second
	}
	if maxSpectators <= 0 {
		maxSpectators = 20
	}
	requestedAt := s.nowFn()
	s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
		SpectatorPlayerID: spectatorSession.PlayerID,
		TargetPlayerID:    targetPlayerID,
		EventType:         "watch_requested",
		WaitMS:            0,
		Detail:            map[string]any{"target_handle": targetHandle},
	})

	deadline := requestedAt.Add(waitTimeout)
	var match *activeMatch
	for s.nowFn().Before(deadline) {
		found, ok := s.lookupActiveMatchByHandle(targetHandle)
		if ok && found != nil {
			match = found
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	if match == nil {
		waitMS := s.nowFn().Sub(requestedAt).Milliseconds()
		if waitMS < 0 {
			waitMS = 0
		}
		s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
			SpectatorPlayerID: spectatorSession.PlayerID,
			TargetPlayerID:    targetPlayerID,
			EventType:         "watch_timeout",
			WaitMS:            waitMS,
			Detail:            map[string]any{"target_handle": targetHandle},
		})
		return fmt.Errorf("target has no active pvp match")
	}

	watcherID := spectatorSession.PlayerID + ":" + uuid.NewString()
	ch := make(chan []string, 32)
	if err := s.spectators.AddWatcher(match.MatchID, watcherID, ch, maxSpectators); err != nil {
		s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
			SpectatorPlayerID: spectatorSession.PlayerID,
			TargetPlayerID:    targetPlayerID,
			MatchID:           &match.MatchID,
			EventType:         "watch_rejected",
			WaitMS:            0,
			Detail:            map[string]any{"error": err.Error()},
		})
		return err
	}
	defer s.spectators.RemoveWatcher(match.MatchID, watcherID)

	waitMS := s.nowFn().Sub(requestedAt).Milliseconds()
	if waitMS < 0 {
		waitMS = 0
	}
	s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
		SpectatorPlayerID: spectatorSession.PlayerID,
		TargetPlayerID:    targetPlayerID,
		MatchID:           &match.MatchID,
		EventType:         "watch_attached",
		WaitMS:            waitMS,
		Detail:            map[string]any{"target_handle": targetHandle},
	})

	s.sendFrame(spectatorSession, fmt.Sprintf("Watching %s...", targetHandle))
	for {
		select {
		case <-ctx.Done():
			s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
				SpectatorPlayerID: spectatorSession.PlayerID,
				TargetPlayerID:    targetPlayerID,
				MatchID:           &match.MatchID,
				EventType:         "watch_ended",
				WaitMS:            0,
				Detail:            map[string]any{"reason": "context_canceled"},
			})
			return ctx.Err()
		case <-match.Done:
			s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
				SpectatorPlayerID: spectatorSession.PlayerID,
				TargetPlayerID:    targetPlayerID,
				MatchID:           &match.MatchID,
				EventType:         "watch_ended",
				WaitMS:            0,
				Detail:            map[string]any{"reason": "match_finished"},
			})
			return nil
		case payload, ok := <-ch:
			if !ok {
				s.persistSpectatorEvent(ctx, storage.SpectatorTelemetryEvent{
					SpectatorPlayerID: spectatorSession.PlayerID,
					TargetPlayerID:    targetPlayerID,
					MatchID:           &match.MatchID,
					EventType:         "watch_ended",
					WaitMS:            0,
					Detail:            map[string]any{"reason": "stream_closed"},
				})
				return nil
			}
			s.sendFrame(spectatorSession, payload...)
		}
	}
}

func (s *InMemoryService) persistSpectatorEvent(ctx context.Context, event storage.SpectatorTelemetryEvent) {
	if s == nil || s.m5Store == nil {
		return
	}
	_, _ = s.m5Store.CreateSpectatorTelemetryEvent(ctx, event)
}

func (s *InMemoryService) resolveTargetPlayerID(ctx context.Context, targetHandle string) (string, error) {
	match, ok := s.lookupActiveMatchByHandle(targetHandle)
	if ok && match != nil {
		switch {
		case strings.EqualFold(targetHandle, match.P1Handle):
			return match.P1ID, nil
		case strings.EqualFold(targetHandle, match.P2Handle):
			return match.P2ID, nil
		default:
			return match.P1ID, nil
		}
	}
	if s == nil || s.m5Store == nil {
		return "", storage.ErrNotFound
	}
	playerEntity, err := s.m5Store.GetByHandle(ctx, targetHandle)
	if err != nil {
		return "", err
	}
	return playerEntity.ID, nil
}
