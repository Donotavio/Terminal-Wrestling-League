package lobby

import (
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
)

// LobbySnapshot is a stable view of the in-memory lobby.
type LobbySnapshot struct {
	Online  int
	InQueue int
	Players []string
}

// Service exposes lobby and queue operations.
type Service interface {
	Register(session player.Session) error
	Unregister(playerID string)
	JoinQueue(playerID string) error
	LeaveQueue(playerID string) error
	Snapshot() LobbySnapshot
}

// InMemoryService stores active sessions and queue state.
type InMemoryService struct {
	mu       sync.RWMutex
	sessions map[string]player.Session
	queuedAt map[string]time.Time
	queue    []string
}

func NewInMemoryService() *InMemoryService {
	return &InMemoryService{
		sessions: map[string]player.Session{},
		queuedAt: map[string]time.Time{},
		queue:    make([]string, 0, 16),
	}
}

func (s *InMemoryService) Register(session player.Session) error {
	if session.PlayerID == "" {
		return fmt.Errorf("player id is required")
	}
	if session.Handle == "" {
		return fmt.Errorf("handle is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[session.PlayerID]; exists {
		return fmt.Errorf("player %s already registered", session.PlayerID)
	}
	s.sessions[session.PlayerID] = session
	return nil
}

func (s *InMemoryService) Unregister(playerID string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, playerID)
	s.removeQueueLocked(playerID)
}

func (s *InMemoryService) JoinQueue(playerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[playerID]; !exists {
		return fmt.Errorf("player %s is not registered", playerID)
	}
	if _, exists := s.queuedAt[playerID]; exists {
		return nil
	}
	s.queue = append(s.queue, playerID)
	s.queuedAt[playerID] = time.Now().UTC()
	return nil
}

func (s *InMemoryService) LeaveQueue(playerID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.sessions[playerID]; !exists {
		return fmt.Errorf("player %s is not registered", playerID)
	}
	s.removeQueueLocked(playerID)
	return nil
}

func (s *InMemoryService) Snapshot() LobbySnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	players := make([]string, 0, len(s.sessions))
	for _, sess := range s.sessions {
		players = append(players, sess.Handle)
	}
	sort.Strings(players)

	return LobbySnapshot{
		Online:  len(s.sessions),
		InQueue: len(s.queuedAt),
		Players: players,
	}
}

// PopNextPair returns the next FIFO pair after removing timed-out queue entries.
func (s *InMemoryService) PopNextPair(now time.Time, queueTimeout time.Duration) (pair [2]string, ok bool, timedOut []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if now.IsZero() {
		now = time.Now().UTC()
	}

	if queueTimeout > 0 {
		filtered := s.queue[:0]
		for _, playerID := range s.queue {
			enqueuedAt, exists := s.queuedAt[playerID]
			if !exists {
				continue
			}
			if now.Sub(enqueuedAt) >= queueTimeout {
				delete(s.queuedAt, playerID)
				timedOut = append(timedOut, playerID)
				continue
			}
			filtered = append(filtered, playerID)
		}
		s.queue = filtered
	}

	if len(s.queue) < 2 {
		return pair, false, timedOut
	}
	pair[0] = s.queue[0]
	pair[1] = s.queue[1]
	s.queue = s.queue[2:]
	delete(s.queuedAt, pair[0])
	delete(s.queuedAt, pair[1])
	return pair, true, timedOut
}

func (s *InMemoryService) GetSession(playerID string) (player.Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.sessions[playerID]
	return sess, ok
}

// QueueStatus returns whether the player is queued plus queue position and wait duration.
func (s *InMemoryService) QueueStatus(playerID string, now time.Time) (inQueue bool, position int, wait time.Duration) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	joinedAt, exists := s.queuedAt[playerID]
	if !exists {
		return false, 0, 0
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	position = 0
	for idx, queuedID := range s.queue {
		if queuedID == playerID {
			position = idx + 1
			break
		}
	}
	wait = now.Sub(joinedAt)
	if wait < 0 {
		wait = 0
	}
	return true, position, wait
}

func (s *InMemoryService) removeQueueLocked(playerID string) {
	if _, exists := s.queuedAt[playerID]; !exists {
		return
	}
	delete(s.queuedAt, playerID)

	filtered := s.queue[:0]
	for _, queuedID := range s.queue {
		if queuedID == playerID {
			continue
		}
		filtered = append(filtered, queuedID)
	}
	s.queue = filtered
}
