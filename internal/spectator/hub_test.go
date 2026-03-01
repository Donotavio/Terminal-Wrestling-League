package spectator

import (
	"testing"
	"time"
)

func TestHubAddBroadcastRemove(t *testing.T) {
	hub := NewHub()
	hub.RegisterMatch("m1")

	ch := make(chan []string, 1)
	if err := hub.AddWatcher("m1", "w1", ch, 20); err != nil {
		t.Fatalf("add watcher: %v", err)
	}
	if hub.Count("m1") != 1 {
		t.Fatalf("count = %d, want 1", hub.Count("m1"))
	}

	hub.Broadcast("m1", []string{"hello"})
	select {
	case payload := <-ch:
		if len(payload) != 1 || payload[0] != "hello" {
			t.Fatalf("payload = %v", payload)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("did not receive broadcast")
	}

	hub.RemoveWatcher("m1", "w1")
	if hub.Count("m1") != 0 {
		t.Fatalf("count = %d, want 0", hub.Count("m1"))
	}
}

func TestHubCapacityLimit(t *testing.T) {
	hub := NewHub()
	hub.RegisterMatch("m2")
	if err := hub.AddWatcher("m2", "w1", make(chan []string, 1), 1); err != nil {
		t.Fatalf("add first watcher: %v", err)
	}
	if err := hub.AddWatcher("m2", "w2", make(chan []string, 1), 1); err == nil {
		t.Fatalf("expected capacity error")
	}
}
