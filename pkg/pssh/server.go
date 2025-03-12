package pssh

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
	"golang.org/x/crypto/ssh"
)

type SSHServerConn struct {
	Ctx        context.Context
	CancelFunc context.CancelFunc
	Logger     *slog.Logger
	Conn       *ssh.ServerConn
	SSHServer  *SSHServer

	mu sync.Mutex
}

func (sc *SSHServerConn) Close() error {
	sc.CancelFunc()
	return nil
}

type SSHServerConnSession struct {
	ssh.Channel
	*SSHServerConn

	pty   *Pty
	winch chan Window

	mu sync.Mutex
}

// Deadline implements context.Context.
func (s *SSHServerConn) Deadline() (deadline time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Deadline()
}

// Done implements context.Context.
func (s *SSHServerConn) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Done()
}

// Err implements context.Context.
func (s *SSHServerConn) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Err()
}

// Value implements context.Context.
func (s *SSHServerConn) Value(key any) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Value(key)
}

// SetValue implements context.Context.
func (s *SSHServerConn) SetValue(key any, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Ctx = context.WithValue(s.Ctx, key, data)
}

func (s *SSHServerConn) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx
}

func (s *SSHServerConnSession) Permissions() *ssh.Permissions {
	return s.Conn.Permissions
}

func (s *SSHServerConnSession) User() string {
	return s.Conn.User()
}

func (s *SSHServerConnSession) PublicKey() ssh.PublicKey {
	key, ok := s.Conn.Permissions.Extensions["pubkey"]
	if !ok {
		return nil
	}

	pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
	if err != nil {
		return nil
	}
	return pk
}

func (s *SSHServerConnSession) RemoteAddr() net.Addr {
	return s.Conn.RemoteAddr()
}

func (s *SSHServerConnSession) Command() []string {
	cmd, _ := s.Value("command").([]string)
	return cmd
}

func (s *SSHServerConnSession) Close() error {
	return s.Channel.Close()
}

func (s *SSHServerConnSession) Exit(code int) error {
	status := struct{ Status uint32 }{uint32(code)}
	_, err := s.Channel.SendRequest("exit-status", false, ssh.Marshal(&status))
	return err
}

func (s *SSHServerConnSession) Fatal(err error) {
	fmt.Fprintln(s.Stderr(), err)
	fmt.Fprintf(s.Stderr(), "\r")
	_ = s.Exit(1)
	_ = s.Close()
}

func (s *SSHServerConnSession) Pty() (*Pty, <-chan Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pty == nil {
		return nil, nil, false
	}

	return s.pty, s.winch, true
}

var _ context.Context = &SSHServerConnSession{}

func (sc *SSHServerConn) Handle(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) error {
	defer sc.Close()

	for {
		select {
		case <-sc.Done():
			return nil
		case newChan, ok := <-chans:
			if !ok {
				return nil
			}

			sc.Logger.Info("new channel", "type", newChan.ChannelType(), "extraData", newChan.ExtraData())
			chanFunc, ok := sc.SSHServer.Config.ChannelMiddleware[newChan.ChannelType()]
			if !ok {
				sc.Logger.Info("no channel middleware for type", "type", newChan.ChannelType())
				continue
			}

			go chanFunc(newChan, sc)
		case req, ok := <-reqs:
			if !ok {
				return nil
			}
			sc.Logger.Info("new request", "type", req.Type, "wantReply", req.WantReply, "payload", req.Payload)
		}
	}
}

func NewSSHServerConn(
	ctx context.Context,
	logger *slog.Logger,
	conn *ssh.ServerConn,
	server *SSHServer,
) *SSHServerConn {
	if ctx == nil {
		ctx = context.Background()
	}

	cancelCtx, cancelFunc := context.WithCancel(ctx)

	if logger == nil {
		logger = slog.Default()
	}

	return &SSHServerConn{
		Ctx:        cancelCtx,
		CancelFunc: cancelFunc,
		Logger:     logger,
		Conn:       conn,
		SSHServer:  server,
	}
}

type SSHServerHandler func(*SSHServerConnSession) error
type SSHServerMiddleware func(SSHServerHandler) SSHServerHandler
type SSHServerChannelMiddleware func(ssh.NewChannel, *SSHServerConn) error

type SSHServerConfig struct {
	*ssh.ServerConfig
	ListenAddr          string
	Middleware          []SSHServerMiddleware
	SubsystemMiddleware []SSHServerMiddleware
	ChannelMiddleware   map[string]SSHServerChannelMiddleware
}

type SSHServer struct {
	Ctx        context.Context
	CancelFunc context.CancelFunc
	Logger     *slog.Logger
	Config     *SSHServerConfig
	Listener   net.Listener
	Conns      *syncmap.Map[string, *SSHServerConn]
}

func (s *SSHServer) ListenAndServe() error {
	listen, err := net.Listen("tcp", s.Config.ListenAddr)
	if err != nil {
		return err
	}

	s.Listener = listen
	defer s.Listener.Close()

	go func() {
		<-s.Ctx.Done()
		s.Close()
	}()

	var retErr error

	for {
		conn, err := s.Listener.Accept()
		if err != nil {
			s.Logger.Error("accept", "err", err)
			if errors.Is(err, net.ErrClosed) {
				retErr = err
				break
			}
			continue
		}

		go func() {
			if err := s.HandleConn(conn); err != nil {
				s.Logger.Error("handle conn", "err", err)
			}
		}()
	}

	if errors.Is(retErr, net.ErrClosed) {
		return nil
	}

	return retErr
}

func (s *SSHServer) HandleConn(conn net.Conn) error {
	defer conn.Close()

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.Config.ServerConfig)
	if err != nil {
		return err
	}

	newLogger := s.Logger.With(
		"remoteAddr", conn.RemoteAddr().String(),
		"user", sshConn.User(),
		"pubkey", sshConn.Permissions.Extensions["pubkey"],
	)

	newConn := NewSSHServerConn(
		s.Ctx,
		newLogger,
		sshConn,
		s,
	)

	s.Conns.Store(sshConn.RemoteAddr().String(), newConn)

	err = newConn.Handle(chans, reqs)

	s.Conns.Delete(sshConn.RemoteAddr().String())

	return err
}

func (s *SSHServer) Close() error {
	s.CancelFunc()
	return s.Listener.Close()
}

func NewSSHServer(ctx context.Context, logger *slog.Logger, config *SSHServerConfig) *SSHServer {
	if ctx == nil {
		ctx = context.Background()
	}

	cancelCtx, cancelFunc := context.WithCancel(ctx)

	if logger == nil {
		logger = slog.Default()
	}

	if config == nil {
		config = &SSHServerConfig{}
	}

	if config.ChannelMiddleware == nil {
		config.ChannelMiddleware = map[string]SSHServerChannelMiddleware{}
	}

	if _, ok := config.ChannelMiddleware["session"]; !ok {
		config.ChannelMiddleware["session"] = func(newChan ssh.NewChannel, sc *SSHServerConn) error {
			channel, requests, err := newChan.Accept()
			if err != nil {
				sc.Logger.Error("accept session channel", "err", err)
				return err
			}

			sesh := &SSHServerConnSession{
				Channel:       channel,
				SSHServerConn: sc,
			}

			for {
				select {
				case <-sc.Done():
					return nil
				case req, ok := <-requests:
					if !ok {
						return nil
					}

					go func() {
						sc.Logger.Info("new session request", "type", req.Type, "wantReply", req.WantReply, "payload", req.Payload)
						switch req.Type {
						case "subsystem":
							if len(sc.SSHServer.Config.SubsystemMiddleware) == 0 {
								req.Reply(false, nil)
								sc.Close()
								return
							}

							h := func(*SSHServerConnSession) error { return nil }
							for _, m := range sc.SSHServer.Config.SubsystemMiddleware {
								h = m(h)
							}

							req.Reply(true, nil)

							if err := h(sesh); err != nil {
								sc.Logger.Error("subsystem middleware", "err", err)
								sesh.Fatal(err)
								return
							}

							sesh.Exit(0)
							sesh.Close()
						case "shell", "exec":
							if len(sc.SSHServer.Config.Middleware) == 0 {
								req.Reply(false, nil)
							}

							var payload = struct{ Value string }{}
							ssh.Unmarshal(req.Payload, &payload)

							sesh.SetValue("command", strings.Fields(payload.Value))

							h := func(*SSHServerConnSession) error { return nil }
							for _, m := range sc.SSHServer.Config.Middleware {
								h = m(h)
							}

							req.Reply(true, nil)

							if err := h(sesh); err != nil {
								sc.Logger.Error("exec middleware", "err", err)
								sesh.Fatal(err)
								return
							}

							sesh.Exit(0)
							sesh.Close()
						case "pty-req":
							if sesh.pty != nil {
								req.Reply(false, nil)
								return
							}

							ptyReq, ok := parsePtyRequest(req.Payload)
							if !ok {
								req.Reply(false, nil)
								return
							}

							sesh.mu.Lock()
							sesh.pty = &ptyReq
							sesh.winch = make(chan Window, 1)
							sesh.mu.Unlock()

							sesh.winch <- ptyReq.Window
							req.Reply(ok, nil)
						case "window-change":
							if sesh.pty == nil {
								req.Reply(false, nil)
								return
							}
							win, ok := parseWinchRequest(req.Payload)
							if ok {
								sesh.mu.Lock()
								sesh.pty.Window = win
								sesh.winch <- win
								sesh.mu.Unlock()
							}
							req.Reply(ok, nil)
						}
					}()
				}
			}
		}
	}

	server := &SSHServer{
		Ctx:        cancelCtx,
		CancelFunc: cancelFunc,
		Logger:     logger,
		Config:     config,
		Conns:      syncmap.New[string, *SSHServerConn](),
	}

	return server
}

func KeysEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}

	am := a.Marshal()
	bm := b.Marshal()
	return (len(am) == len(bm) && subtle.ConstantTimeCompare(am, bm) == 1)
}
