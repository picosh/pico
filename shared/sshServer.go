package shared

import (
	"context"
	"errors"
	"log/slog"
	"net"

	"github.com/antoniomika/syncmap"
	"golang.org/x/crypto/ssh"
)

type SSHServerConn struct {
	Ctx        context.Context
	CancelFunc context.CancelFunc
	Logger     *slog.Logger
	Conn       *ssh.ServerConn
	SSHServer  *SSHServer
}

func (sc *SSHServerConn) Close() error {
	sc.CancelFunc()
	return nil
}

func (sc *SSHServerConn) Handle(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) error {
	defer sc.Close()

	for {
		select {
		case <-sc.Ctx.Done():
			return nil
		case newChan := <-chans:
			sc.Logger.Info("new channel", "type", newChan.ChannelType(), "extraData", newChan.ExtraData())
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

type SSHServerMiddleware func(func(ssh.Session) error) func(ssh.Session) error

type SSHServerConfig struct {
	*ssh.ServerConfig
	ListenAddr          string
	SessionMiddleware   []SSHServerMiddleware
	SubsystemMiddleware []SSHServerMiddleware
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
