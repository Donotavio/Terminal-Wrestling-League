package player

import (
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
)

// CommandKind identifies session command categories.
type CommandKind uint8

const (
	CommandUnknown CommandKind = iota
	CommandJoinQueue
	CommandLeaveQueue
	CommandAction
	CommandSnapshot
	CommandQuit
	CommandWatch
	CommandTutorialRetry
)

// Command is one parsed player command from SSH input.
type Command struct {
	Kind       CommandKind
	Action     combat.Action
	Target     combat.Zone
	ReceivedAt time.Time
}

// Frame is one text payload emitted to the SSH client.
type Frame struct {
	Lines     []string
	Timestamp time.Time
}

// Session stores in-memory channels and metadata for one connected player.
type Session struct {
	PlayerID   string
	Handle     string
	RemoteAddr string
	Input      chan Command
	Output     chan Frame
}
