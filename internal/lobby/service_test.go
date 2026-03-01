package lobby

import (
	"testing"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
)

func newSession(id, handle string) player.Session {
	return player.Session{
		PlayerID: id,
		Handle:   handle,
		Input:    make(chan player.Command, 8),
		Output:   make(chan player.Frame, 8),
	}
}

func TestQueueFIFOOrder(t *testing.T) {
	svc := NewInMemoryService()
	for i, p := range []struct{ id, handle string }{{"p1", "a"}, {"p2", "b"}, {"p3", "c"}, {"p4", "d"}} {
		if err := svc.Register(newSession(p.id, p.handle)); err != nil {
			t.Fatalf("register %d: %v", i, err)
		}
		if err := svc.JoinQueue(p.id); err != nil {
			t.Fatalf("join queue %d: %v", i, err)
		}
	}

	pair1, ok, _ := svc.PopNextPair(time.Now().UTC(), 10*time.Second)
	if !ok || pair1[0] != "p1" || pair1[1] != "p2" {
		t.Fatalf("first pair = %v ok=%t, want [p1 p2]", pair1, ok)
	}
	pair2, ok, _ := svc.PopNextPair(time.Now().UTC(), 10*time.Second)
	if !ok || pair2[0] != "p3" || pair2[1] != "p4" {
		t.Fatalf("second pair = %v ok=%t, want [p3 p4]", pair2, ok)
	}
}

func TestLeaveQueueRemovesPlayer(t *testing.T) {
	svc := NewInMemoryService()
	_ = svc.Register(newSession("p1", "a"))
	_ = svc.Register(newSession("p2", "b"))
	_ = svc.Register(newSession("p3", "c"))
	_ = svc.JoinQueue("p1")
	_ = svc.JoinQueue("p2")
	_ = svc.JoinQueue("p3")

	if err := svc.LeaveQueue("p2"); err != nil {
		t.Fatalf("leave queue: %v", err)
	}

	pair, ok, _ := svc.PopNextPair(time.Now().UTC(), 10*time.Second)
	if !ok || pair[0] != "p1" || pair[1] != "p3" {
		t.Fatalf("pair after leave = %v ok=%t, want [p1 p3]", pair, ok)
	}
	if snap := svc.Snapshot(); snap.InQueue != 0 {
		t.Fatalf("in queue = %d, want 0", snap.InQueue)
	}
}

func TestQueueTimeoutRemovesExpiredPlayers(t *testing.T) {
	svc := NewInMemoryService()
	_ = svc.Register(newSession("p1", "a"))
	_ = svc.Register(newSession("p2", "b"))
	_ = svc.JoinQueue("p1")
	_ = svc.JoinQueue("p2")

	future := time.Now().UTC().Add(2 * time.Minute)
	_, ok, timedOut := svc.PopNextPair(future, 30*time.Second)
	if ok {
		t.Fatalf("did not expect pair after timeout")
	}
	if len(timedOut) != 2 {
		t.Fatalf("timed out count = %d, want 2", len(timedOut))
	}
	if snap := svc.Snapshot(); snap.InQueue != 0 {
		t.Fatalf("in queue = %d, want 0", snap.InQueue)
	}
}

func TestUnregisterCleansQueueAndSession(t *testing.T) {
	svc := NewInMemoryService()
	_ = svc.Register(newSession("p1", "a"))
	_ = svc.JoinQueue("p1")

	svc.Unregister("p1")

	if _, ok := svc.GetSession("p1"); ok {
		t.Fatalf("expected session to be removed")
	}
	if snap := svc.Snapshot(); snap.Online != 0 || snap.InQueue != 0 {
		t.Fatalf("snapshot = %+v, want online=0 inqueue=0", snap)
	}
}
