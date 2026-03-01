package matchmaking

import (
	"context"
	"fmt"
	"hash/fnv"
	"sync"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/engine"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
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
	lobby     queueLobby
	finalizer MatchFinalizer
	resolver  combat.Resolver
	cfg       MatchConfig
	telemetry Telemetry

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
		lobby:     lobbySvc,
		finalizer: finalizer,
		resolver:  combat.NewStandardResolver(),
		cfg:       cfg,
		telemetry: telemetry,
		nowFn:     func() time.Time { return time.Now().UTC() },
		stopCh:    make(chan struct{}),
		runCtx:    context.Background(),
		inMatch:   make(map[string]struct{}),
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

		inputs, ok := s.collectTurnInputs(ctx, sess1, sess2)
		if !ok {
			return
		}
		canonicalInputs, err := combat.CanonicalizeInputs(state, inputs)
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

		frame := renderFrame(sess1.Handle, sess2.Handle, result)
		s.sendFrame(sess1, frame...)
		s.sendFrame(sess2, frame...)
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

func (s *InMemoryService) collectTurnInputs(ctx context.Context, sess1, sess2 player.Session) ([]combat.TurnInput, bool) {
	results := make(chan turnInputResult, 2)
	go func() { results <- waitForTurnInput(ctx, sess1, s.cfg.TurnTimeout) }()
	go func() { results <- waitForTurnInput(ctx, sess2, s.cfg.TurnTimeout) }()

	in1 := <-results
	in2 := <-results
	if !in1.ok || !in2.ok {
		return nil, false
	}
	return []combat.TurnInput{in1.input, in2.input}, true
}

type turnInputResult struct {
	input combat.TurnInput
	ok    bool
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
			return turnInputResult{ok: false}
		case cmd, ok := <-sess.Input:
			if !ok {
				return turnInputResult{
					input: combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
					ok:    true,
				}
			}
			switch cmd.Kind {
			case player.CommandAction:
				decisionMS := int(time.Since(cmd.ReceivedAt).Milliseconds())
				if decisionMS < 0 {
					decisionMS = 0
				}
				return turnInputResult{
					input: combat.TurnInput{PlayerID: sess.PlayerID, Action: cmd.Action, Target: cmd.Target, DecisionMS: decisionMS},
					ok:    true,
				}
			case player.CommandQuit:
				return turnInputResult{
					input: combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
					ok:    true,
				}
			default:
				// Non-action commands are ignored during turn collection.
			}
		case <-timer.C:
			return turnInputResult{
				input: combat.TurnInput{PlayerID: sess.PlayerID, Action: combat.ActionNone, Target: combat.ZoneTorso},
				ok:    true,
			}
		}
	}
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

func renderFrame(handle1, handle2 string, result combat.TurnResult) []string {
	s := result.Next
	lines := []string{
		fmt.Sprintf("Turn %d", s.Turn),
		fmt.Sprintf("%s HP:%d ST:%d MO:%d", handle1, s.P1.HP, s.P1.Stamina, s.P1.Momentum),
		fmt.Sprintf("%s HP:%d ST:%d MO:%d", handle2, s.P2.HP, s.P2.Stamina, s.P2.Momentum),
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
