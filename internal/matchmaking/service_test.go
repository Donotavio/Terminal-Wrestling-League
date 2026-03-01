package matchmaking

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/animation"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
)

type mockFinalizer struct {
	mu     sync.Mutex
	calls  int
	params []storage.FinalizeMatchParams
}

func (m *mockFinalizer) FinalizeMatch(_ context.Context, p storage.FinalizeMatchParams) (storage.FinalizedMatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.params = append(m.params, p)
	return storage.FinalizedMatch{}, nil
}

func (m *mockFinalizer) snapshot() (int, []storage.FinalizeMatchParams) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]storage.FinalizeMatchParams, len(m.params))
	copy(out, m.params)
	return m.calls, out
}

type mockM5Finalizer struct {
	mu sync.Mutex

	calls      int
	params     []storage.FinalizeMatchParams
	turnRows   []storage.MatchTurnTelemetry
	summaries  []storage.MatchSummaryTelemetry
	flags      []storage.AntiBotFlag
	queue      []storage.QueueTelemetryEvent
	spectator  []storage.SpectatorTelemetryEvent
	tutorial   []storage.TutorialRun
	profiles   map[string]storage.PlayerProfile
	byHandle   map[string]storage.Player
	antiBotCfg storage.AntiBotConfig
}

func newMockM5Finalizer() *mockM5Finalizer {
	return &mockM5Finalizer{
		profiles:   map[string]storage.PlayerProfile{},
		byHandle:   map[string]storage.Player{},
		antiBotCfg: storage.DefaultAntiBotConfig(),
	}
}

func (m *mockM5Finalizer) FinalizeMatch(_ context.Context, p storage.FinalizeMatchParams) (storage.FinalizedMatch, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.params = append(m.params, p)
	return storage.FinalizedMatch{
		Match: storage.Match{
			ID:         p.MatchID,
			WinnerID:   p.WinnerID,
			ResultType: p.ResultType,
			DurationMS: int(p.EndedAt.Sub(p.StartedAt).Milliseconds()),
		},
		Season:    storage.Season{ID: "season-1"},
		Player1ID: p.Player1ID,
		Player2ID: p.Player2ID,
	}, nil
}

func (m *mockM5Finalizer) GetByHandle(_ context.Context, handle string) (storage.Player, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	playerEntity, ok := m.byHandle[strings.ToLower(strings.TrimSpace(handle))]
	if !ok {
		return storage.Player{}, storage.ErrNotFound
	}
	return playerEntity, nil
}

func (m *mockM5Finalizer) LoadAntiBotConfig(_ context.Context) (storage.AntiBotConfig, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.antiBotCfg, nil
}

func (m *mockM5Finalizer) CreateAntiBotFlag(_ context.Context, flag storage.AntiBotFlag) (storage.AntiBotFlag, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if flag.ID == "" {
		flag.ID = fmt.Sprintf("flag-%d", len(m.flags)+1)
	}
	m.flags = append(m.flags, flag)
	return flag, nil
}

func (m *mockM5Finalizer) InsertTurnTelemetryBatch(_ context.Context, rows []storage.MatchTurnTelemetry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.turnRows = append(m.turnRows, rows...)
	return nil
}

func (m *mockM5Finalizer) InsertMatchSummaryTelemetry(_ context.Context, summary storage.MatchSummaryTelemetry) (storage.MatchSummaryTelemetry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.summaries = append(m.summaries, summary)
	return summary, nil
}

func (m *mockM5Finalizer) CreateQueueTelemetryEvent(_ context.Context, event storage.QueueTelemetryEvent) (storage.QueueTelemetryEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.queue = append(m.queue, event)
	return event, nil
}

func (m *mockM5Finalizer) CreateSpectatorTelemetryEvent(_ context.Context, event storage.SpectatorTelemetryEvent) (storage.SpectatorTelemetryEvent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.spectator = append(m.spectator, event)
	return event, nil
}

func (m *mockM5Finalizer) CreateTutorialRun(_ context.Context, run storage.TutorialRun) (storage.TutorialRun, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run.ID == "" {
		run.ID = fmt.Sprintf("tutorial-%d", len(m.tutorial)+1)
	}
	m.tutorial = append(m.tutorial, run)
	return run, nil
}

func (m *mockM5Finalizer) MarkTutorialCompleted(_ context.Context, playerID string, now time.Time) (storage.PlayerProfile, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	profile := m.profiles[playerID]
	profile.PlayerID = playerID
	profile.TutorialCompleted = true
	profile.TutorialCompletedAt = &now
	profile.UpdatedAt = now
	m.profiles[playerID] = profile
	return profile, nil
}

func (m *mockM5Finalizer) snapshotM5() (
	turnRows []storage.MatchTurnTelemetry,
	summaries []storage.MatchSummaryTelemetry,
	flags []storage.AntiBotFlag,
	queue []storage.QueueTelemetryEvent,
	spectator []storage.SpectatorTelemetryEvent,
) {
	m.mu.Lock()
	defer m.mu.Unlock()
	turnRows = append([]storage.MatchTurnTelemetry(nil), m.turnRows...)
	summaries = append([]storage.MatchSummaryTelemetry(nil), m.summaries...)
	flags = append([]storage.AntiBotFlag(nil), m.flags...)
	queue = append([]storage.QueueTelemetryEvent(nil), m.queue...)
	spectator = append([]storage.SpectatorTelemetryEvent(nil), m.spectator...)
	return turnRows, summaries, flags, queue, spectator
}

func makeSession(id, handle string) player.Session {
	return player.Session{
		PlayerID: id,
		Handle:   handle,
		Input:    make(chan player.Command, 64),
		Output:   make(chan player.Frame, 64),
	}
}

type pairFailureLobby struct {
	sessionByID map[string]player.Session
	missingID   string
	joinCalls   []string
}

func (l *pairFailureLobby) Register(session player.Session) error {
	if l.sessionByID == nil {
		l.sessionByID = make(map[string]player.Session)
	}
	l.sessionByID[session.PlayerID] = session
	return nil
}

func (l *pairFailureLobby) Unregister(playerID string) {
	delete(l.sessionByID, playerID)
}

func (l *pairFailureLobby) JoinQueue(playerID string) error {
	l.joinCalls = append(l.joinCalls, playerID)
	return nil
}

func (l *pairFailureLobby) LeaveQueue(_ string) error {
	return nil
}

func (l *pairFailureLobby) Snapshot() lobby.LobbySnapshot {
	return lobby.LobbySnapshot{Online: len(l.sessionByID)}
}

func (l *pairFailureLobby) PopNextPair(_ time.Time, _ time.Duration) (pair [2]string, ok bool, timedOut []string) {
	return [2]string{"p1", "p2"}, true, nil
}

func (l *pairFailureLobby) GetSession(playerID string) (player.Session, bool) {
	if playerID == l.missingID {
		return player.Session{}, false
	}
	sess, ok := l.sessionByID[playerID]
	return sess, ok
}

func TestWaitForTurnInputTimeoutReturnsNone(t *testing.T) {
	sess := makeSession("p1", "alice")
	result := waitForTurnInput(context.Background(), sess, 10*time.Millisecond)
	if result.status != turnInputTimeout {
		t.Fatalf("status = %s, want %s", result.status, turnInputTimeout)
	}
	input := result.input
	if input.PlayerID != "p1" {
		t.Fatalf("player id = %s, want p1", input.PlayerID)
	}
	if input.Action != combat.ActionNone {
		t.Fatalf("action = %s, want None", input.Action)
	}
	if input.Target != combat.ZoneTorso {
		t.Fatalf("target = %s, want Torso", input.Target)
	}
}

func TestBuildCinematicSequenceAddsANSIFrames(t *testing.T) {
	frame := animation.Frame{
		Keyframes: [][]string{
			{"Turn 2 | GUARD", "k1"},
			{"Turn 2 | IMPACT", "k2"},
		},
	}

	steps := buildCinematicSequence(frame)
	if len(steps) != 2 {
		t.Fatalf("sequence len = %d, want 2", len(steps))
	}
	if len(steps[0].Lines) < 2 || steps[0].Lines[0] != ansiClearHome {
		t.Fatalf("first step should start with ansi clear/home: %v", steps[0].Lines)
	}
	if steps[0].Delay != cinematicFrameDelay {
		t.Fatalf("first delay = %s, want %s", steps[0].Delay, cinematicFrameDelay)
	}
	if steps[1].Delay != cinematicTurnSettleDelay {
		t.Fatalf("last delay = %s, want %s", steps[1].Delay, cinematicTurnSettleDelay)
	}
}

func TestBuildCinematicSequenceAppliesSlowmoDelay(t *testing.T) {
	frame := animation.Frame{
		Keyframes: [][]string{
			{"f1"},
			{"f2"},
			{"f3"},
		},
		Effects: []animation.Effect{animation.EffectSlowmo},
	}

	steps := buildCinematicSequence(frame)
	if len(steps) != 3 {
		t.Fatalf("sequence len = %d, want 3", len(steps))
	}
	if steps[1].Delay != cinematicSlowmoDelay {
		t.Fatalf("slowmo delay = %s, want %s", steps[1].Delay, cinematicSlowmoDelay)
	}
	if steps[2].Delay != cinematicSlowmoDelay {
		t.Fatalf("final slowmo delay = %s, want %s", steps[2].Delay, cinematicSlowmoDelay)
	}
}

func TestEmitCinematicSequenceBroadcastsEachFrame(t *testing.T) {
	svc := NewInMemoryService(lobby.NewInMemoryService(), nil, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)
	svc.spectators.RegisterMatch("m1")
	watcher := make(chan []string, 4)
	if err := svc.spectators.AddWatcher("m1", "w1", watcher, 4); err != nil {
		t.Fatalf("add watcher: %v", err)
	}
	defer svc.spectators.RemoveWatcher("m1", "w1")
	defer svc.spectators.UnregisterMatch("m1")

	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	frame := animation.Frame{
		Keyframes: [][]string{
			{"Turn 1 | GUARD", "frame a"},
			{"Turn 1 | IMPACT", "frame b"},
		},
	}

	if ok := svc.emitCinematicSequence(context.Background(), frame, "m1", s1, s2); !ok {
		t.Fatalf("emitCinematicSequence returned false")
	}

	if got := drainSessionFrameCount(s1); got != 2 {
		t.Fatalf("p1 frame count = %d, want 2", got)
	}
	if got := drainSessionFrameCount(s2); got != 2 {
		t.Fatalf("p2 frame count = %d, want 2", got)
	}
	if got := drainWatcherFrameCount(watcher); got != 2 {
		t.Fatalf("watcher frame count = %d, want 2", got)
	}
}

func TestOrderTurnResultsBySlotUsesStablePlayerOrder(t *testing.T) {
	slot1 := &matchSlot{session: makeSession("p1", "alice")}
	slot2 := &matchSlot{session: makeSession("p2", "bob")}

	ordered, ok := orderTurnResultsBySlot(
		slot1,
		slot2,
		turnInputResult{playerID: "p2", status: turnInputTimeout},
		turnInputResult{playerID: "p1", status: turnInputDisconnect},
	)
	if !ok {
		t.Fatalf("expected results to be ordered")
	}
	if ordered[0].playerID != "p1" || ordered[1].playerID != "p2" {
		t.Fatalf("ordered players = [%s,%s], want [p1,p2]", ordered[0].playerID, ordered[1].playerID)
	}
}

func TestRunMatchTakesOverByNPCAfterTwoTimeouts(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}

	now := time.Now().UTC()
	s2.Input <- player.Command{Kind: player.CommandAction, Action: combat.ActionBlock, Target: combat.ZoneTorso, ReceivedAt: now}
	s2.Input <- player.Command{Kind: player.CommandAction, Action: combat.ActionStrike, Target: combat.ZoneHead, ReceivedAt: now}

	mf := &mockFinalizer{}
	svc := NewInMemoryService(lb, mf, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     2,
	}, nil)

	svc.runMatch(context.Background(), s1, s2, 0, 0)

	_, params := mf.snapshot()
	if len(params) != 1 || params[0].Replay == nil {
		t.Fatalf("expected one replay payload")
	}
	replay := params[0].Replay
	if len(replay.Turns) != 2 {
		t.Fatalf("replay turns = %d, want 2", len(replay.Turns))
	}

	t1p1, ok := replayInputForPlayer(replay, 1, "p1")
	if !ok {
		t.Fatalf("missing p1 input for turn 1")
	}
	if t1p1.Action != combat.ActionNone {
		t.Fatalf("turn1 p1 action = %s, want None", t1p1.Action)
	}

	t2p1, ok := replayInputForPlayer(replay, 2, "p1")
	if !ok {
		t.Fatalf("missing p1 input for turn 2")
	}
	if t2p1.Action == combat.ActionNone {
		t.Fatalf("turn2 p1 action = None, want NPC takeover action")
	}

	if !sessionOutputContains(s1, "NPC takeover enabled (timeout).") {
		t.Fatalf("expected timeout takeover notification")
	}
}

func TestRunMatchTakesOverByNPCOnDisconnectSameTurn(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}

	now := time.Now().UTC()
	s1.Input <- player.Command{Kind: player.CommandQuit, ReceivedAt: now}
	s2.Input <- player.Command{Kind: player.CommandAction, Action: combat.ActionBlock, Target: combat.ZoneTorso, ReceivedAt: now}

	mf := &mockFinalizer{}
	svc := NewInMemoryService(lb, mf, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  25 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	svc.runMatch(context.Background(), s1, s2, 0, 0)

	_, params := mf.snapshot()
	if len(params) != 1 || params[0].Replay == nil {
		t.Fatalf("expected one replay payload")
	}
	replay := params[0].Replay
	if len(replay.Turns) != 1 {
		t.Fatalf("replay turns = %d, want 1", len(replay.Turns))
	}

	t1p1, ok := replayInputForPlayer(replay, 1, "p1")
	if !ok {
		t.Fatalf("missing p1 input for turn 1")
	}
	if t1p1.Action == combat.ActionNone {
		t.Fatalf("turn1 p1 action = None, want immediate NPC takeover action")
	}

	if !sessionOutputContains(s1, "NPC takeover enabled (disconnect).") {
		t.Fatalf("expected disconnect takeover notification")
	}
}

func TestRunMatchDrawByMaxTurnsPersistsOnce(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}

	mf := &mockFinalizer{}
	svc := NewInMemoryService(lb, mf, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	svc.runMatch(context.Background(), s1, s2, 0, 0)

	calls, params := mf.snapshot()
	if calls != 1 {
		t.Fatalf("finalize calls = %d, want 1", calls)
	}
	if params[0].ResultType != storage.MatchResultDraw {
		t.Fatalf("result type = %s, want draw", params[0].ResultType)
	}
	if params[0].WinnerID != nil {
		t.Fatalf("winner id = %v, want nil", *params[0].WinnerID)
	}
	if params[0].Replay == nil {
		t.Fatalf("expected replay payload")
	}
	replay := params[0].Replay
	if replay.Seed == 0 {
		t.Fatalf("replay seed = 0, want non-zero")
	}
	if replay.InitialState.P1.PlayerID != "p1" || replay.InitialState.P2.PlayerID != "p2" {
		t.Fatalf("initial state players = (%s,%s), want (p1,p2)", replay.InitialState.P1.PlayerID, replay.InitialState.P2.PlayerID)
	}
	if len(replay.Turns) != 1 {
		t.Fatalf("replay turns = %d, want 1", len(replay.Turns))
	}
	if replay.Turns[0].Turn != 1 {
		t.Fatalf("replay turn number = %d, want 1", replay.Turns[0].Turn)
	}
	if replay.Turns[0].RelativeMS < 0 {
		t.Fatalf("replay relative_ms = %d, want >= 0", replay.Turns[0].RelativeMS)
	}
	if len(replay.Turns[0].Inputs) != 2 {
		t.Fatalf("replay inputs len = %d, want 2", len(replay.Turns[0].Inputs))
	}
	if replay.Turns[0].Inputs[0].PlayerID != "p1" || replay.Turns[0].Inputs[1].PlayerID != "p2" {
		t.Fatalf("replay input order = [%s, %s], want [p1, p2]",
			replay.Turns[0].Inputs[0].PlayerID, replay.Turns[0].Inputs[1].PlayerID)
	}
}

func TestProcessQueuePairsPlayersAndPersists(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}
	if err := lb.JoinQueue("p1"); err != nil {
		t.Fatalf("join queue p1: %v", err)
	}
	if err := lb.JoinQueue("p2"); err != nil {
		t.Fatalf("join queue p2: %v", err)
	}

	mf := &mockFinalizer{}
	svc := NewInMemoryService(lb, mf, MatchConfig{
		QueueTimeout: 10 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	now := time.Now().UTC()
	svc.processQueue(now)

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		calls, _ := mf.snapshot()
		if calls == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	calls, _ := mf.snapshot()
	if calls != 1 {
		t.Fatalf("finalize calls = %d, want 1", calls)
	}

	svc.wg.Wait()
	svc.mu.Lock()
	_, busy1 := svc.inMatch["p1"]
	_, busy2 := svc.inMatch["p2"]
	svc.mu.Unlock()
	if busy1 || busy2 {
		t.Fatalf("players should be released from inMatch after match completion")
	}
}

func TestProcessQueueRequeuesAvailablePlayerWhenPeerMissing(t *testing.T) {
	s1 := makeSession("p1", "alice")
	lb := &pairFailureLobby{
		sessionByID: map[string]player.Session{
			"p1": s1,
		},
		missingID: "p2",
	}
	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 10 * time.Second,
		TurnTimeout:  50 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	svc.processQueue(time.Now().UTC())

	if len(lb.joinCalls) != 1 || lb.joinCalls[0] != "p1" {
		t.Fatalf("join calls = %v, want [p1]", lb.joinCalls)
	}
	if svc.IsPlayerInMatch("p1") || svc.IsPlayerInMatch("p2") {
		t.Fatalf("players should not stay marked in match after pair failure")
	}
}

func TestEnqueueRejectedInMatchDoesNotTrackQueueStart(t *testing.T) {
	lb := lobby.NewInMemoryService()
	sess := makeSession("p1", "alice")
	if err := lb.Register(sess); err != nil {
		t.Fatalf("register session: %v", err)
	}

	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 10 * time.Second,
		TurnTimeout:  50 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	svc.mu.Lock()
	svc.inMatch["p1"] = struct{}{}
	svc.mu.Unlock()

	if err := svc.Enqueue("p1"); err == nil {
		t.Fatalf("expected enqueue to fail while in match")
	}

	svc.mu.Lock()
	_, exists := svc.queueJoinedAt["p1"]
	svc.mu.Unlock()
	if exists {
		t.Fatalf("queue start timestamp should not be recorded on rejected enqueue")
	}
}

func TestEnqueueSuccessfulJoinTracksQueueStart(t *testing.T) {
	lb := lobby.NewInMemoryService()
	sess := makeSession("p1", "alice")
	if err := lb.Register(sess); err != nil {
		t.Fatalf("register session: %v", err)
	}

	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 10 * time.Second,
		TurnTimeout:  50 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	if err := svc.Enqueue("p1"); err != nil {
		t.Fatalf("enqueue should succeed: %v", err)
	}

	svc.mu.Lock()
	joinedAt, exists := svc.queueJoinedAt["p1"]
	svc.mu.Unlock()
	if !exists {
		t.Fatalf("queue start timestamp should be recorded after successful enqueue")
	}
	if joinedAt.IsZero() {
		t.Fatalf("queue start timestamp is zero")
	}
}

func TestStartNPCMatchRunsAndReleasesPlayer(t *testing.T) {
	lb := lobby.NewInMemoryService()
	sess := makeSession("p1", "alice")
	if err := lb.Register(sess); err != nil {
		t.Fatalf("register session: %v", err)
	}

	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     2,
	}, nil)

	if err := svc.StartNPCMatch(sess); err != nil {
		t.Fatalf("StartNPCMatch returned error: %v", err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && svc.IsPlayerInMatch(sess.PlayerID) {
		time.Sleep(10 * time.Millisecond)
	}
	if svc.IsPlayerInMatch(sess.PlayerID) {
		t.Fatalf("player should be released after npc match")
	}

	lines := drainSessionOutputLines(sess)
	if !containsLine(lines, "Practice match started against Coach NPC.") {
		t.Fatalf("missing npc match start message; lines=%v", lines)
	}
	if !containsAnyLine(lines, "Practice result: victory.", "Practice result: defeat.", "Practice result: draw.") {
		t.Fatalf("missing npc match result message; lines=%v", lines)
	}
}

func TestStartNPCMatchRejectsBusyPlayer(t *testing.T) {
	lb := lobby.NewInMemoryService()
	sess := makeSession("p1", "alice")
	if err := lb.Register(sess); err != nil {
		t.Fatalf("register session: %v", err)
	}

	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     2,
	}, nil)
	svc.mu.Lock()
	svc.inMatch[sess.PlayerID] = struct{}{}
	svc.mu.Unlock()

	err := svc.StartNPCMatch(sess)
	if err == nil {
		t.Fatalf("expected busy player error")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "already in a match") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStopCancelsActiveTurnCollection(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}
	if err := lb.JoinQueue("p1"); err != nil {
		t.Fatalf("join queue p1: %v", err)
	}
	if err := lb.JoinQueue("p2"); err != nil {
		t.Fatalf("join queue p2: %v", err)
	}

	svc := NewInMemoryService(lb, nil, MatchConfig{
		QueueTimeout: 10 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	svc.Start(context.Background())

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if svc.IsPlayerInMatch("p1") && svc.IsPlayerInMatch("p2") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !svc.IsPlayerInMatch("p1") || !svc.IsPlayerInMatch("p2") {
		svc.Stop()
		t.Fatalf("players did not enter match")
	}

	done := make(chan struct{})
	go func() {
		svc.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Stop did not cancel active turn collection promptly")
	}
}

func TestRunMatchPersistsM5TelemetryAndAntiBotFlags(t *testing.T) {
	lb := lobby.NewInMemoryService()
	s1 := makeSession("p1", "alice")
	s2 := makeSession("p2", "bob")
	if err := lb.Register(s1); err != nil {
		t.Fatalf("register s1: %v", err)
	}
	if err := lb.Register(s2); err != nil {
		t.Fatalf("register s2: %v", err)
	}

	now := time.Now().UTC()
	s1.Input <- player.Command{Kind: player.CommandAction, Action: combat.ActionStrike, Target: combat.ZoneHead, ReceivedAt: now}
	s2.Input <- player.Command{Kind: player.CommandAction, Action: combat.ActionBlock, Target: combat.ZoneTorso, ReceivedAt: now}

	finalizer := newMockM5Finalizer()
	svc := NewInMemoryService(lb, finalizer, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	svc.runMatch(context.Background(), s1, s2, 111, 222)

	turnRows, summaries, flags, _, _ := finalizer.snapshotM5()
	if len(turnRows) != 2 {
		t.Fatalf("turn telemetry rows = %d, want 2", len(turnRows))
	}
	if len(summaries) != 1 {
		t.Fatalf("summary rows = %d, want 1", len(summaries))
	}
	if summaries[0].QueueWaitMSP1 != 111 || summaries[0].QueueWaitMSP2 != 222 {
		t.Fatalf("queue waits = (%d,%d), want (111,222)", summaries[0].QueueWaitMSP1, summaries[0].QueueWaitMSP2)
	}
	if len(flags) != 2 {
		t.Fatalf("anti bot flags = %d, want 2", len(flags))
	}
}

func TestWatchByHandleAttachesAndStreamsFrames(t *testing.T) {
	finalizer := newMockM5Finalizer()
	finalizer.byHandle["alice"] = storage.Player{ID: "p1", Handle: "alice"}

	svc := NewInMemoryService(lobby.NewInMemoryService(), finalizer, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	match := &activeMatch{
		MatchID:  "m1",
		P1ID:     "p1",
		P2ID:     "p2",
		P1Handle: "alice",
		P2Handle: "bob",
		Done:     make(chan struct{}),
	}
	svc.registerActiveMatch(match)

	spectatorSession := makeSession("sp1", "spec")
	errCh := make(chan error, 1)
	go func() {
		errCh <- svc.WatchByHandle(context.Background(), spectatorSession, "alice", 2*time.Second, 20)
	}()

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		_, _, _, _, spectatorEvents := finalizer.snapshotM5()
		if hasSpectatorEvent(spectatorEvents, "watch_attached") {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	svc.spectators.Broadcast("m1", []string{"frame one"})
	svc.unregisterActiveMatch(match)

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("watch did not return after match end")
	}

	if !sessionOutputContains(spectatorSession, "Watching alice...") {
		t.Fatalf("missing watch start frame")
	}
	if !sessionOutputContains(spectatorSession, "frame one") {
		t.Fatalf("missing broadcasted frame")
	}
	_, _, _, _, spectatorEvents := finalizer.snapshotM5()
	if !hasSpectatorEvent(spectatorEvents, "watch_requested") ||
		!hasSpectatorEvent(spectatorEvents, "watch_attached") ||
		!hasSpectatorEvent(spectatorEvents, "watch_ended") {
		t.Fatalf("unexpected spectator event set: %+v", spectatorEvents)
	}
}

func TestWatchByHandleTimeout(t *testing.T) {
	finalizer := newMockM5Finalizer()
	finalizer.byHandle["alice"] = storage.Player{ID: "p1", Handle: "alice"}
	svc := NewInMemoryService(lobby.NewInMemoryService(), finalizer, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	spectatorSession := makeSession("sp2", "spec2")
	err := svc.WatchByHandle(context.Background(), spectatorSession, "alice", 50*time.Millisecond, 20)
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !strings.Contains(err.Error(), "no active pvp match") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _, spectatorEvents := finalizer.snapshotM5()
	if !hasSpectatorEvent(spectatorEvents, "watch_timeout") {
		t.Fatalf("expected watch_timeout event")
	}
}

func TestWatchByHandleRejectsOverCapacity(t *testing.T) {
	finalizer := newMockM5Finalizer()
	finalizer.byHandle["alice"] = storage.Player{ID: "p1", Handle: "alice"}
	svc := NewInMemoryService(lobby.NewInMemoryService(), finalizer, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	match := &activeMatch{
		MatchID:  "m2",
		P1ID:     "p1",
		P2ID:     "p2",
		P1Handle: "alice",
		P2Handle: "bob",
		Done:     make(chan struct{}),
	}
	svc.registerActiveMatch(match)
	defer svc.unregisterActiveMatch(match)

	if err := svc.spectators.AddWatcher("m2", "existing", make(chan []string, 1), 1); err != nil {
		t.Fatalf("seed watcher: %v", err)
	}

	err := svc.WatchByHandle(context.Background(), makeSession("sp3", "spec3"), "alice", 100*time.Millisecond, 1)
	if err == nil {
		t.Fatalf("expected capacity rejection")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "capacity") {
		t.Fatalf("unexpected error: %v", err)
	}
	_, _, _, _, spectatorEvents := finalizer.snapshotM5()
	if !hasSpectatorEvent(spectatorEvents, "watch_rejected") {
		t.Fatalf("expected watch_rejected event")
	}
}

func TestRunTutorialMarksCompletionAndPersistsRun(t *testing.T) {
	finalizer := newMockM5Finalizer()
	svc := NewInMemoryService(lobby.NewInMemoryService(), finalizer, MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  20 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	sess := makeSession("p1", "alice")
	run, err := svc.RunTutorial(context.Background(), sess)
	if err != nil {
		t.Fatalf("RunTutorial returned error: %v", err)
	}
	if run.PlayerID != "p1" {
		t.Fatalf("tutorial run player id = %s, want p1", run.PlayerID)
	}

	finalizer.mu.Lock()
	defer finalizer.mu.Unlock()
	if len(finalizer.tutorial) != 1 {
		t.Fatalf("tutorial runs persisted = %d, want 1", len(finalizer.tutorial))
	}
	profile := finalizer.profiles["p1"]
	if !profile.TutorialCompleted {
		t.Fatalf("tutorial completed = false, want true")
	}
}

func replayInputForPlayer(replay *storage.MatchReplayWrite, turn int, playerID string) (combat.TurnInput, bool) {
	for _, replayTurn := range replay.Turns {
		if replayTurn.Turn != turn {
			continue
		}
		for _, input := range replayTurn.Inputs {
			if input.PlayerID == playerID {
				return input, true
			}
		}
	}
	return combat.TurnInput{}, false
}

func hasSpectatorEvent(events []storage.SpectatorTelemetryEvent, eventType string) bool {
	for _, event := range events {
		if event.EventType == eventType {
			return true
		}
	}
	return false
}

func sessionOutputContains(sess player.Session, needle string) bool {
	for {
		select {
		case frame := <-sess.Output:
			for _, line := range frame.Lines {
				if strings.Contains(line, needle) {
					return true
				}
			}
		default:
			return false
		}
	}
}

func drainSessionOutputLines(sess player.Session) []string {
	lines := make([]string, 0, 8)
	for {
		select {
		case frame := <-sess.Output:
			lines = append(lines, frame.Lines...)
		default:
			return lines
		}
	}
}

func drainSessionFrameCount(sess player.Session) int {
	count := 0
	for {
		select {
		case <-sess.Output:
			count++
		default:
			return count
		}
	}
}

func drainWatcherFrameCount(ch chan []string) int {
	count := 0
	for {
		select {
		case <-ch:
			count++
		default:
			return count
		}
	}
}

func containsLine(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func containsAnyLine(lines []string, needles ...string) bool {
	for _, needle := range needles {
		if containsLine(lines, needle) {
			return true
		}
	}
	return false
}
