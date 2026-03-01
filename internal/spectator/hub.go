package spectator

import (
	"fmt"
	"sync"
)

// Hub stores read-only watchers for active matches.
type Hub struct {
	mu      sync.RWMutex
	matches map[string]map[string]chan []string
}

func NewHub() *Hub {
	return &Hub{matches: make(map[string]map[string]chan []string)}
}

func (h *Hub) RegisterMatch(matchID string) {
	if matchID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.matches[matchID]; !exists {
		h.matches[matchID] = make(map[string]chan []string)
	}
}

func (h *Hub) UnregisterMatch(matchID string) {
	if matchID == "" {
		return
	}
	h.mu.Lock()
	watchers := h.matches[matchID]
	delete(h.matches, matchID)
	h.mu.Unlock()

	for _, ch := range watchers {
		close(ch)
	}
}

func (h *Hub) AddWatcher(matchID, watcherID string, ch chan []string, maxPerMatch int) error {
	if matchID == "" || watcherID == "" || ch == nil {
		return fmt.Errorf("matchID, watcherID and channel are required")
	}
	if maxPerMatch <= 0 {
		maxPerMatch = 20
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	watchers, exists := h.matches[matchID]
	if !exists {
		return fmt.Errorf("match not active")
	}
	if _, exists := watchers[watcherID]; exists {
		return fmt.Errorf("watcher already attached")
	}
	if len(watchers) >= maxPerMatch {
		return fmt.Errorf("spectator capacity reached")
	}
	watchers[watcherID] = ch
	return nil
}

func (h *Hub) RemoveWatcher(matchID, watcherID string) {
	if matchID == "" || watcherID == "" {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	watchers, exists := h.matches[matchID]
	if !exists {
		return
	}
	ch, exists := watchers[watcherID]
	if !exists {
		return
	}
	delete(watchers, watcherID)
	close(ch)
}

func (h *Hub) Broadcast(matchID string, lines []string) {
	if matchID == "" || len(lines) == 0 {
		return
	}

	h.mu.RLock()
	watchers, exists := h.matches[matchID]
	if !exists {
		h.mu.RUnlock()
		return
	}
	clones := make([]chan []string, 0, len(watchers))
	for _, ch := range watchers {
		clones = append(clones, ch)
	}
	h.mu.RUnlock()

	payload := append([]string(nil), lines...)
	for _, ch := range clones {
		select {
		case ch <- payload:
		default:
		}
	}
}

func (h *Hub) Count(matchID string) int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.matches[matchID])
}
