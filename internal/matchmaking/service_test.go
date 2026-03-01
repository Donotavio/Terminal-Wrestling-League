package matchmaking

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

	svc.runMatch(context.Background(), s1, s2)

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

	svc.runMatch(context.Background(), s1, s2)

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

	svc.runMatch(context.Background(), s1, s2)

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
