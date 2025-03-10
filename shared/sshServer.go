package shared

import (
	"context"
	"errors"
	"log/slog"
	"net"
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

var _ context.Context = &SSHServerConn{}

func (sc *SSHServerConn) Handle(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) error {
	defer sc.Close()

	for {
		select {
		case <-sc.Done():
			return nil
		case newChan := <-chans:
			sc.Logger.Info("new channel", "type", newChan.ChannelType(), "extraData", newChan.ExtraData())
			switch newChan.ChannelType() {
			case "session":
				channel, requests, err := newChan.Accept()
				if err != nil {
					sc.Logger.Error("accept session channel", "err", err)
					return err
				}

				go func() {
					for {
						select {
						case <-sc.Done():
							return
						case req := <-requests:
							if req == nil {
								continue
							}
							sc.Logger.Info("new session request", "type", req.Type, "wantReply", req.WantReply, "payload", req.Payload)
						}
					}
				}()

				h := func(*SSHServerConnSession) error { return nil }
				for _, m := range sc.SSHServer.Config.Middleware {
					h = m(h)
				}

				return h(&SSHServerConnSession{
					Channel:       channel,
					SSHServerConn: sc,
				})
			}
		case req := <-reqs:
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

type SSHServerMiddleware func(func(*SSHServerConnSession) error) func(*SSHServerConnSession) error

type SSHServerConfig struct {
	*ssh.ServerConfig
	ListenAddr string
	Middleware []SSHServerMiddleware
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

	server := &SSHServer{
		Ctx:        cancelCtx,
		CancelFunc: cancelFunc,
		Logger:     logger,
		Config:     config,
		Conns:      syncmap.New[string, *SSHServerConn](),
	}

	return server
}
