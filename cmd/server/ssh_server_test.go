package main

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/matchmaking"
	"golang.org/x/crypto/ssh"
)

type fakeEnsurer struct{}

func (f *fakeEnsurer) EnsurePlayer(_ context.Context, handle string) (string, error) {
	return "player-" + handle, nil
}

type fakeConnMeta struct {
	user string
	addr net.Addr
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
	server, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, log.New(io.Discard, "", 0))
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

	srv, err := newSSHServer(cfg, lb, matchSvc, &fakeEnsurer{}, nil, log.New(io.Discard, "", 0))
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
