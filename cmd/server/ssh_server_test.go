package main

import (
	"bytes"
	"context"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/matchmaking"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/storage"
	"golang.org/x/crypto/ssh"
)

type fakeEnsurer struct{}

func (f *fakeEnsurer) EnsurePlayerSession(_ context.Context, handle string) (string, storage.PlayerProfile, error) {
	return "player-" + handle, storage.PlayerProfile{
		PlayerID:          "player-" + handle,
		TutorialCompleted: true,
	}, nil
}

type fakeTutorialEnsurer struct {
	completed bool
}

func (f *fakeTutorialEnsurer) EnsurePlayerSession(_ context.Context, handle string) (string, storage.PlayerProfile, error) {
	return "player-" + handle, storage.PlayerProfile{
		PlayerID:          "player-" + handle,
		TutorialCompleted: f.completed,
	}, nil
}

type caseSensitiveWatchFinalizer struct {
	players map[string]storage.Player
}

func (f *caseSensitiveWatchFinalizer) FinalizeMatch(_ context.Context, _ storage.FinalizeMatchParams) (storage.FinalizedMatch, error) {
	return storage.FinalizedMatch{}, nil
}

func (f *caseSensitiveWatchFinalizer) GetByHandle(_ context.Context, handle string) (storage.Player, error) {
	playerEntity, ok := f.players[strings.TrimSpace(handle)]
	if !ok {
		return storage.Player{}, storage.ErrNotFound
	}
	return playerEntity, nil
}

func (f *caseSensitiveWatchFinalizer) LoadAntiBotConfig(_ context.Context) (storage.AntiBotConfig, error) {
	return storage.DefaultAntiBotConfig(), nil
}

func (f *caseSensitiveWatchFinalizer) CreateAntiBotFlag(_ context.Context, flag storage.AntiBotFlag) (storage.AntiBotFlag, error) {
	return flag, nil
}

func (f *caseSensitiveWatchFinalizer) InsertTurnTelemetryBatch(_ context.Context, _ []storage.MatchTurnTelemetry) error {
	return nil
}

func (f *caseSensitiveWatchFinalizer) InsertMatchSummaryTelemetry(_ context.Context, summary storage.MatchSummaryTelemetry) (storage.MatchSummaryTelemetry, error) {
	return summary, nil
}

func (f *caseSensitiveWatchFinalizer) CreateQueueTelemetryEvent(_ context.Context, event storage.QueueTelemetryEvent) (storage.QueueTelemetryEvent, error) {
	return event, nil
}

func (f *caseSensitiveWatchFinalizer) CreateSpectatorTelemetryEvent(_ context.Context, event storage.SpectatorTelemetryEvent) (storage.SpectatorTelemetryEvent, error) {
	return event, nil
}

func (f *caseSensitiveWatchFinalizer) CreateTutorialRun(_ context.Context, run storage.TutorialRun) (storage.TutorialRun, error) {
	return run, nil
}

func (f *caseSensitiveWatchFinalizer) MarkTutorialCompleted(_ context.Context, playerID string, now time.Time) (storage.PlayerProfile, error) {
	return storage.PlayerProfile{
		PlayerID:            playerID,
		TutorialCompleted:   true,
		TutorialCompletedAt: &now,
		UpdatedAt:           now,
	}, nil
}

type fakeConnMeta struct {
	user string
	addr net.Addr
}

type scriptedReadWriteCloser struct {
	reader *strings.Reader
	mu     sync.Mutex
	writer bytes.Buffer
}

func newScriptedReadWriteCloser(input string) *scriptedReadWriteCloser {
	return &scriptedReadWriteCloser{
		reader: strings.NewReader(input),
	}
}

func (s *scriptedReadWriteCloser) Read(p []byte) (int, error) {
	return s.reader.Read(p)
}

func (s *scriptedReadWriteCloser) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer.Write(p)
}

func (s *scriptedReadWriteCloser) Close() error { return nil }

func (s *scriptedReadWriteCloser) Output() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writer.String()
}

func (f fakeConnMeta) User() string          { return f.user }
func (f fakeConnMeta) SessionID() []byte     { return []byte("sid") }
func (f fakeConnMeta) ClientVersion() []byte { return []byte("c") }
func (f fakeConnMeta) ServerVersion() []byte { return []byte("s") }
func (f fakeConnMeta) RemoteAddr() net.Addr  { return f.addr }
func (f fakeConnMeta) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 2222}
}
func (f fakeConnMeta) Permissions() *ssh.Permissions { return nil }

func TestSSHAuthIntegration(t *testing.T) {
	if os.Getenv("RUN_SSH_IT") != "1" {
		t.Skip("RUN_SSH_IT != 1")
	}

	srv, addr, cleanup := startTestSSHServer(t)
	defer cleanup()
	_ = srv

	badCfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{ssh.Password("wrong")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}
	if _, err := ssh.Dial("tcp", addr, badCfg); err == nil {
		t.Fatalf("expected invalid password to fail")
	}

	goodCfg := &ssh.ClientConfig{
		User:            "alice",
		Auth:            []ssh.AuthMethod{ssh.Password("secret")},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         3 * time.Second,
	}
	client, err := ssh.Dial("tcp", addr, goodCfg)
	if err != nil {
		t.Fatalf("dial with valid credentials: %v", err)
	}
	defer client.Close()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer session.Close()

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("stdin pipe: %v", err)
	}
	if err := session.RequestPty("xterm", 80, 24, ssh.TerminalModes{}); err != nil {
		t.Fatalf("request pty: %v", err)
	}
	if err := session.Shell(); err != nil {
		t.Fatalf("shell: %v", err)
	}

	_, _ = io.WriteString(stdin, "foo\n")
	_, _ = io.WriteString(stdin, "quit\n")
	_ = stdin.Close()

	waitDone := make(chan error, 1)
	go func() { waitDone <- session.Wait() }()
	select {
	case <-time.After(2 * time.Second):
		// The test objective is auth and command path stability.
	case <-waitDone:
	}
}

func TestLoginRateLimitCallback(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	meta := fakeConnMeta{
		user: "alice",
		addr: &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 50000},
	}

	for i := 0; i < 3; i++ {
		if _, err := server.passwordCallback(meta, []byte("secret")); err != nil {
			t.Fatalf("expected auth to pass in initial burst at i=%d: %v", i, err)
		}
	}
	if _, err := server.passwordCallback(meta, []byte("secret")); err == nil {
		t.Fatalf("expected rate limit error on 4th attempt")
	} else if !strings.Contains(strings.ToLower(err.Error()), "rate limit") {
		t.Fatalf("expected rate limit error, got %v", err)
	}
}

func TestRunShellReturnsAfterQuitCommand(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	rw := newScriptedReadWriteCloser("quit\n")
	done := make(chan struct{})
	go func() {
		server.runShell(context.Background(), "alice", "127.0.0.1:12345", rw)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("runShell did not return after quit command")
	}
}

func TestActionCommandRequiresActiveMatch(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	sess := player.Session{
		PlayerID: "p1",
		Handle:   "alice",
		Input:    make(chan player.Command, 8),
		Output:   make(chan player.Frame, 8),
	}
	profile := storage.PlayerProfile{PlayerID: "p1", TutorialCompleted: true}
	if ok := server.handleUserInput(context.Background(), sess, &profile, "a strike head"); !ok {
		t.Fatalf("action command should not terminate session")
	}

	select {
	case cmd := <-sess.Input:
		t.Fatalf("expected action to be rejected outside active match, got %+v", cmd)
	default:
	}

	select {
	case frame := <-sess.Output:
		if len(frame.Lines) == 0 || !strings.Contains(strings.ToLower(frame.Lines[0]), "not in an active match") {
			t.Fatalf("unexpected feedback frame: %+v", frame.Lines)
		}
	default:
		t.Fatalf("expected feedback frame when action is rejected")
	}
}

func TestQueueCommandRequiresTutorialCompletion(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	sess := player.Session{
		PlayerID: "p1",
		Handle:   "alice",
		Input:    make(chan player.Command, 8),
		Output:   make(chan player.Frame, 8),
	}
	profile := storage.PlayerProfile{PlayerID: "p1", TutorialCompleted: false}
	if ok := server.handleUserInput(context.Background(), sess, &profile, "q"); !ok {
		t.Fatalf("queue command should not terminate session")
	}
	if len(sess.Input) != 0 {
		t.Fatalf("queue command should not push direct input")
	}
	select {
	case frame := <-sess.Output:
		if len(frame.Lines) == 0 || !strings.Contains(strings.ToLower(frame.Lines[0]), "tutorial required") {
			t.Fatalf("unexpected feedback frame: %+v", frame.Lines)
		}
	default:
		t.Fatalf("expected feedback frame for tutorial gate")
	}
}

func TestWatchCommandPreservesTargetHandleCasing(t *testing.T) {
	cfg := Config{
		SSHAddr:              "127.0.0.1:0",
		SSHUsers:             map[string]string{"alice": "secret"},
		WatchWaitTimeout:     1 * time.Millisecond,
		SpectatorMaxPerMatch: 20,
	}
	lb := lobby.NewInMemoryService()
	finalizer := &caseSensitiveWatchFinalizer{
		players: map[string]storage.Player{
			"Alice": {ID: "p-target", Handle: "Alice"},
		},
	}
	matchSvc := matchmaking.NewInMemoryService(lb, finalizer, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  5 * time.Second,
		MaxTurns:     120,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	sess := player.Session{
		PlayerID: "spectator-1",
		Handle:   "viewer",
		Input:    make(chan player.Command, 8),
		Output:   make(chan player.Frame, 8),
	}
	profile := storage.PlayerProfile{PlayerID: sess.PlayerID, TutorialCompleted: true}
	if ok := server.handleUserInput(context.Background(), sess, &profile, "watch Alice"); !ok {
		t.Fatalf("watch command should not terminate session")
	}

	select {
	case frame := <-sess.Output:
		if len(frame.Lines) == 0 {
			t.Fatalf("expected feedback frame")
		}
		msg := strings.ToLower(frame.Lines[0])
		if !strings.Contains(msg, "no active pvp match") {
			t.Fatalf("unexpected watch response: %q", frame.Lines[0])
		}
		if strings.Contains(msg, "not found") {
			t.Fatalf("watch lookup should preserve target handle casing, got: %q", frame.Lines[0])
		}
	default:
		t.Fatalf("expected watch feedback frame")
	}
}

func TestRunShellFirstLoginForcesTutorial(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     8,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeTutorialEnsurer{completed: false}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	rw := newScriptedReadWriteCloser("help\na strike head\nq\nquit\n")
	done := make(chan struct{})
	go func() {
		server.runShell(context.Background(), "alice", "127.0.0.1:12345", rw)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runShell did not return")
	}

	output := strings.ToLower(rw.Output())
	if !strings.Contains(output, "tutorial is required") {
		t.Fatalf("expected mandatory tutorial text, got: %s", output)
	}
	if !strings.Contains(output, "tutorial phase 2/2") {
		t.Fatalf("expected tutorial combat phase text, got: %s", output)
	}
}

func TestTutorialRetryRunsTutorialAgain(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 45 * time.Second,
		TurnTimeout:  10 * time.Millisecond,
		MaxTurns:     8,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeTutorialEnsurer{completed: true}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	rw := newScriptedReadWriteCloser("tutorial retry\nhelp\na strike head\nq\nquit\n")
	done := make(chan struct{})
	go func() {
		server.runShell(context.Background(), "alice", "127.0.0.1:12345", rw)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("runShell did not return")
	}

	output := strings.ToLower(rw.Output())
	if !strings.Contains(output, "tutorial retry started") {
		t.Fatalf("expected tutorial retry start text, got: %s", output)
	}
	if !strings.Contains(output, "tutorial phase 2/2") {
		t.Fatalf("expected tutorial combat phase text, got: %s", output)
	}
}

func TestStartReturnsOnContextCancel(t *testing.T) {
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  100 * time.Millisecond,
		MaxTurns:     1,
	}, nil)
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Start(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for server.listener == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if server.listener == nil {
		t.Fatalf("ssh listener did not start")
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("start returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Start did not return after context cancellation")
	}
}

func startTestSSHServer(t *testing.T) (*sshServer, string, func()) {
	t.Helper()
	cfg := Config{
		SSHAddr:  "127.0.0.1:0",
		SSHUsers: map[string]string{"alice": "secret"},
	}
	lb := lobby.NewInMemoryService()
	matchSvc := matchmaking.NewInMemoryService(lb, nil, matchmaking.MatchConfig{
		QueueTimeout: 5 * time.Second,
		TurnTimeout:  100 * time.Millisecond,
		MaxTurns:     1,
	}, nil)

	srv, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, nil, log.New(io.Discard, "", 0))
	if err != nil {
		t.Fatalf("new ssh server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Start(ctx)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for srv.listener == nil && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if srv.listener == nil {
		cancel()
		t.Fatalf("ssh listener did not start")
	}

	cleanup := func() {
		_ = srv.Stop()
		cancel()
		select {
		case <-errCh:
		case <-time.After(2 * time.Second):
		}
	}
	return srv, srv.listener.Addr().String(), cleanup
}
