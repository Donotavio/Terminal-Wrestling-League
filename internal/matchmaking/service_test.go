package matchmaking

import (
	"context"
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

func TestWaitForTurnInputTimeoutReturnsNone(t *testing.T) {
	sess := makeSession("p1", "alice")
	input := waitForTurnInput(sess, 10*time.Millisecond)
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

	svc.runMatch(s1, s2)

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
