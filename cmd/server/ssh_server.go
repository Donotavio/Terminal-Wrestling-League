package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Donotavio/Terminal-Wrestling-League/internal/combat"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/lobby"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/matchmaking"
	"github.com/Donotavio/Terminal-Wrestling-League/internal/player"
	"golang.org/x/crypto/ssh"
)

type metricRecorder interface {
	IncCounter(name string)
	ObserveDuration(name string, d time.Duration)
}

type playerEnsurer interface {
	EnsurePlayer(ctx context.Context, handle string) (playerID string, err error)
}

type sshServer struct {
	cfg       Config
	lobby     *lobby.InMemoryService
	matcher   *matchmaking.InMemoryService
	ensurer   playerEnsurer
	telemetry metricRecorder
	logger    *log.Logger

	loginLimiter  *tokenBucketLimiter
	queueLimiter  *tokenBucketLimiter
	actionLimiter *tokenBucketLimiter

	sshConfig *ssh.ServerConfig
	listener  net.Listener

	sessionsMu sync.Mutex
	sessions   map[string]player.Session
}

func newSSHServer(
	cfg Config,
	lobbySvc *lobby.InMemoryService,
	matcher *matchmaking.InMemoryService,
	ensurer playerEnsurer,
	telemetry metricRecorder,
	logger *log.Logger,
) (*sshServer, error) {
	if logger == nil {
		logger = log.Default()
	}
	if lobbySvc == nil || matcher == nil || ensurer == nil {
		return nil, fmt.Errorf("lobby, matcher and player ensurer are required")
	}

	hostKey, err := generateHostSigner()
	if err != nil {
		return nil, err
	}

	s := &sshServer{
		cfg:           cfg,
		lobby:         lobbySvc,
		matcher:       matcher,
		ensurer:       ensurer,
		telemetry:     telemetry,
		logger:        logger,
		loginLimiter:  newTokenBucketLimiter(5, 3),
		queueLimiter:  newTokenBucketLimiter(10, 10),
		actionLimiter: newTokenBucketLimiter(30, 30),
		sessions:      map[string]player.Session{},
	}

	sshCfg := &ssh.ServerConfig{
		PasswordCallback: s.passwordCallback,
	}
	sshCfg.AddHostKey(hostKey)
	s.sshConfig = sshCfg
	return s, nil
}

func (s *sshServer) Start(ctx context.Context) error {
	listener, err := net.Listen("tcp", s.cfg.SSHAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", s.cfg.SSHAddr, err)
	}
	s.listener = listener
	stopAccept := make(chan struct{})
	defer close(stopAccept)
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-stopAccept:
		}
	}()

	s.matcher.Start(ctx)
	defer s.matcher.Stop()
	s.logger.Printf("ssh server listening on %s", s.cfg.SSHAddr)

	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			if ne, ok := err.(net.Error); ok && ne.Temporary() {
				continue
			}
			return fmt.Errorf("accept ssh conn: %w", err)
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *sshServer) Stop() error {
	s.matcher.Stop()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *sshServer) handleConn(ctx context.Context, raw net.Conn) {
	defer raw.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(raw, s.sshConfig)
	if err != nil {
		return
	}
	defer sshConn.Close()

	go ssh.DiscardRequests(reqs)
	for ch := range chans {
		if ch.ChannelType() != "session" {
			_ = ch.Reject(ssh.UnknownChannelType, "unknown channel type")
			continue
		}

		channel, requests, err := ch.Accept()
		if err != nil {
			continue
		}

		go s.handleSessionChannel(ctx, sshConn, channel, requests)
	}
}

func (s *sshServer) handleSessionChannel(
	ctx context.Context,
	conn *ssh.ServerConn,
	channel ssh.Channel,
	requests <-chan *ssh.Request,
) {
	defer channel.Close()

	shellReady := false
	for req := range requests {
		switch req.Type {
		case "pty-req":
			_ = req.Reply(true, nil)
		case "shell":
			_ = req.Reply(true, nil)
			shellReady = true
			goto RUN
		default:
			_ = req.Reply(false, nil)
		}
	}

RUN:
	if !shellReady {
		return
	}
	s.runShell(ctx, conn.User(), conn.RemoteAddr().String(), channel)
}

func (s *sshServer) runShell(ctx context.Context, handle, remoteAddr string, rw io.ReadWriteCloser) {
	playerID, err := s.ensurer.EnsurePlayer(ctx, handle)
	if err != nil {
		_, _ = io.WriteString(rw, "failed to initialize player\n")
		return
	}

	sess := player.Session{
		PlayerID:   playerID,
		Handle:     handle,
		RemoteAddr: remoteAddr,
		Input:      make(chan player.Command, 64),
		Output:     make(chan player.Frame, 64),
	}

	if err := s.lobby.Register(sess); err != nil {
		_, _ = io.WriteString(rw, "failed to register session\n")
		return
	}
	s.trackSession(sess)

	sessionDone := make(chan struct{})
	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		for {
			select {
			case <-sessionDone:
				return
			case frame, ok := <-sess.Output:
				if !ok {
					return
				}
				for _, line := range frame.Lines {
					_, _ = io.WriteString(rw, line+"\r\n")
				}
			}
		}
	}()
	defer func() {
		close(sessionDone)
		<-writerDone
		s.matcher.Dequeue(sess.PlayerID)
		s.lobby.Unregister(sess.PlayerID)
		s.untrackSession(sess.PlayerID)
		close(sess.Input)
	}()

	s.sendSessionFrame(sess, "Welcome to Terminal Wrestling League!", "Commands: q=join queue, l=leave queue, s=lobby snapshot, a <action> <zone>, quit")

	scanner := bufio.NewScanner(rw)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		if !s.handleUserInput(sess, line) {
			break
		}
	}
}

func (s *sshServer) handleUserInput(sess player.Session, line string) bool {
	fields := strings.Fields(strings.ToLower(line))
	if len(fields) == 0 {
		return true
	}

	switch fields[0] {
	case "q":
		if !s.queueLimiter.Allow(sess.PlayerID) {
			s.sendSessionFrame(sess, "queue rate limit reached")
			return true
		}
		if err := s.matcher.Enqueue(sess.PlayerID); err != nil {
			s.sendSessionFrame(sess, "queue error: "+err.Error())
		}
		return true
	case "l":
		s.matcher.Dequeue(sess.PlayerID)
		return true
	case "s":
		snap := s.lobby.Snapshot()
		s.sendSessionFrame(sess,
			fmt.Sprintf("online=%d in_queue=%d", snap.Online, snap.InQueue),
			fmt.Sprintf("players=%s", strings.Join(snap.Players, ",")),
		)
		return true
	case "help":
		s.sendSessionFrame(sess, "Commands: q, l, s, a <action> <zone>, quit")
		return true
	case "quit", "exit":
		now := time.Now().UTC()
		select {
		case sess.Input <- player.Command{Kind: player.CommandQuit, ReceivedAt: now}:
		default:
		}
		s.sendSessionFrame(sess, "bye")
		return false
	case "a":
		if !s.matcher.IsPlayerInMatch(sess.PlayerID) {
			s.sendSessionFrame(sess, "you are not in an active match")
			return true
		}
		if !s.actionLimiter.Allow(sess.PlayerID) {
			s.sendSessionFrame(sess, "action rate limit reached")
			return true
		}
		cmd, err := parseActionCommand(fields)
		if err != nil {
			s.sendSessionFrame(sess, err.Error())
			return true
		}
		cmd.ReceivedAt = time.Now().UTC()
		select {
		case sess.Input <- cmd:
			// accepted
		default:
			s.sendSessionFrame(sess, "input buffer full, action ignored")
		}
		return true
	default:
		s.sendSessionFrame(sess, "unknown command, type help")
		return true
	}
}

func parseActionCommand(fields []string) (player.Command, error) {
	if len(fields) < 3 {
		return player.Command{}, fmt.Errorf("usage: a <strike|grapple|block|dodge|counter|feint|break> <head|torso|legs>")
	}
	action, err := parseAction(fields[1])
	if err != nil {
		return player.Command{}, err
	}
	target, err := parseZone(fields[2])
	if err != nil {
		return player.Command{}, err
	}
	return player.Command{Kind: player.CommandAction, Action: action, Target: target}, nil
}

func parseAction(v string) (combat.Action, error) {
	switch v {
	case "strike":
		return combat.ActionStrike, nil
	case "grapple":
		return combat.ActionGrapple, nil
	case "block":
		return combat.ActionBlock, nil
	case "dodge":
		return combat.ActionDodge, nil
	case "counter":
		return combat.ActionCounter, nil
	case "feint":
		return combat.ActionFeint, nil
	case "break":
		return combat.ActionBreak, nil
	default:
		return combat.ActionNone, fmt.Errorf("invalid action %q", v)
	}
}

func parseZone(v string) (combat.Zone, error) {
	switch v {
	case "head":
		return combat.ZoneHead, nil
	case "torso":
		return combat.ZoneTorso, nil
	case "legs":
		return combat.ZoneLegs, nil
	default:
		return combat.ZoneTorso, fmt.Errorf("invalid zone %q", v)
	}
}

func generateHostSigner() (ssh.Signer, error) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("generate host key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privateKey)
	if err != nil {
		return nil, fmt.Errorf("create host signer: %w", err)
	}
	return signer, nil
}

func (s *sshServer) passwordCallback(conn ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
	remoteHost, _, err := net.SplitHostPort(conn.RemoteAddr().String())
	if err != nil {
		remoteHost = conn.RemoteAddr().String()
	}
	if s.telemetry != nil {
		s.telemetry.IncCounter("login_attempts")
	}
	if !s.loginLimiter.Allow(remoteHost) {
		if s.telemetry != nil {
			s.telemetry.IncCounter("login_rate_limited")
		}
		return nil, fmt.Errorf("login rate limit reached")
	}

	expected, ok := s.cfg.SSHUsers[conn.User()]
	if !ok || expected != string(password) {
		if s.telemetry != nil {
			s.telemetry.IncCounter("login_denied")
		}
		return nil, fmt.Errorf("invalid username or password")
	}
	return &ssh.Permissions{}, nil
}

func (s *sshServer) trackSession(sess player.Session) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	s.sessions[sess.PlayerID] = sess
}

func (s *sshServer) untrackSession(playerID string) {
	s.sessionsMu.Lock()
	defer s.sessionsMu.Unlock()
	delete(s.sessions, playerID)
}

func (s *sshServer) sendSessionFrame(sess player.Session, lines ...string) {
	if len(lines) == 0 {
		return
	}
	frame := player.Frame{Lines: lines, Timestamp: time.Now().UTC()}
	select {
	case sess.Output <- frame:
	default:
	}
}
