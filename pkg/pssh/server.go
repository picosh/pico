package pssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"golang.org/x/crypto/ssh"
)

type SSHServerConn struct {
	Ctx        context.Context
	CancelFunc context.CancelFunc
	Logger     *slog.Logger
	Conn       *ssh.ServerConn
	SSHServer  *SSHServer
	Start      time.Time

	mu sync.Mutex
}

func (s *SSHServerConn) Context() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx
}

func (sc *SSHServerConn) Close() error {
	sc.CancelFunc()
	return nil
}

type SSHServerConnSession struct {
	ssh.Channel
	*SSHServerConn

	Ctx        context.Context
	CancelFunc context.CancelFunc

	pty   *Pty
	winch chan Window

	mu sync.Mutex
}

// Deadline implements context.Context.
func (s *SSHServerConnSession) Deadline() (deadline time.Time, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Deadline()
}

// Done implements context.Context.
func (s *SSHServerConnSession) Done() <-chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Done()
}

// Err implements context.Context.
func (s *SSHServerConnSession) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Err()
}

// Value implements context.Context.
func (s *SSHServerConnSession) Value(key any) any {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Ctx.Value(key)
}

// SetValue implements context.Context.
func (s *SSHServerConnSession) SetValue(key any, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.Ctx = context.WithValue(s.Ctx, key, data)
}

func (s *SSHServerConnSession) Context() context.Context {
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
	s.CancelFunc()
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
		case <-sc.Context().Done():
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

			go func() {
				err := chanFunc(newChan, sc)
				if err != nil {
					sc.Logger.Error("channel middleware", "err", err)
				}
			}()
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
		Start:      time.Now(),
	}
}

type SSHServerHandler func(*SSHServerConnSession) error
type SSHServerMiddleware func(SSHServerHandler) SSHServerHandler
type SSHServerChannelMiddleware func(ssh.NewChannel, *SSHServerConn) error

type SSHServerConfig struct {
	*ssh.ServerConfig
	App                 string
	ListenAddr          string
	PromListenAddr      string
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

	SessionsCreated  *prometheus.CounterVec
	SessionsFinished *prometheus.CounterVec
	SessionsDuration *prometheus.CounterVec
}

func (s *SSHServer) ListenAndServe() error {
	if s.Config.PromListenAddr != "" {
		s.SessionsCreated = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_created_total",
			Help: "The total number of sessions created",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command"})

		s.SessionsFinished = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_finished_total",
			Help: "The total number of sessions created",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command"})

		s.SessionsDuration = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_duration_seconds",
			Help: "The total sessions duration in seconds",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command"})

		go func() {
			mux := http.NewServeMux()
			mux.Handle("/metrics", promhttp.Handler())

			srv := &http.Server{Addr: s.Config.PromListenAddr, Handler: mux}

			go func() {
				<-s.Ctx.Done()
				s.Logger.Info("Prometheus server shutting down")
				srv.Close()
			}()

			s.Logger.Info("Starting Prometheus server", "addr", s.Config.PromListenAddr)

			err := srv.ListenAndServe()
			if err != nil {
				if errors.Is(err, http.ErrServerClosed) {
					s.Logger.Info("Prometheus server shut down")
					return
				}

				s.Logger.Error("Prometheus serve error", "err", err)
				panic(err)
			}
		}()
	}

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
			if err := s.HandleConn(conn); err != nil && !errors.Is(err, io.EOF) {
				s.Logger.Error("Error handling connection", "err", err, "remoteAddr", conn.RemoteAddr().String())
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

			ctx, cancelFunc := context.WithCancel(sc.Ctx)

			sesh := &SSHServerConnSession{
				Channel:       channel,
				SSHServerConn: sc,
				Ctx:           ctx,
				CancelFunc:    cancelFunc,
			}

			for {
				select {
				case <-sesh.Done():
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
								err := req.Reply(false, nil)
								if err != nil {
									sc.Logger.Error("subsystem reply", "err", err)
								}

								err = sc.Close()
								if err != nil {
									sc.Logger.Error("subsystem close", "err", err)
								}

								sesh.Fatal(err)
								return
							}

							h := func(*SSHServerConnSession) error { return nil }
							for _, m := range sc.SSHServer.Config.SubsystemMiddleware {
								h = m(h)
							}

							err := req.Reply(true, nil)
							if err != nil {
								sc.Logger.Error("subsystem reply", "err", err)
								sesh.Fatal(err)
								return
							}

							if err := h(sesh); err != nil && !errors.Is(err, io.EOF) {
								sc.Logger.Error("subsystem middleware", "err", err)
								sesh.Fatal(err)
								return
							}

							err = sesh.Exit(0)
							if err != nil {
								sc.Logger.Error("subsystem exit", "err", err)
							}

							err = sesh.Close()
							if err != nil {
								sc.Logger.Error("subsystem close", "err", err)
							}
						case "shell", "exec":
							if len(sc.SSHServer.Config.Middleware) == 0 {
								err := req.Reply(false, nil)
								if err != nil {
									sc.Logger.Error("shell/exec reply", "err", err)
								}
								sesh.Fatal(err)
								return
							}

							if len(req.Payload) > 0 {
								var payload = struct{ Value string }{}
								err := ssh.Unmarshal(req.Payload, &payload)
								if err != nil {
									sc.Logger.Error("shell/exec unmarshal", "err", err)
									sesh.Fatal(err)
									return
								}

								if sc.SSHServer.Config.PromListenAddr != "" {
									sc.SSHServer.SessionsCreated.WithLabelValues(payload.Value).Inc()
									defer func() {
										sc.SSHServer.SessionsFinished.WithLabelValues(payload.Value).Inc()
										sc.SSHServer.SessionsDuration.WithLabelValues(payload.Value).Add(time.Since(sc.Start).Seconds())
									}()
								}

								sesh.SetValue("command", strings.Fields(payload.Value))
							}

							h := func(*SSHServerConnSession) error { return nil }
							for _, m := range sc.SSHServer.Config.Middleware {
								h = m(h)
							}

							err = req.Reply(true, nil)
							if err != nil {
								sc.Logger.Error("shell/exec reply", "err", err)
								sesh.Fatal(err)
								return
							}

							if err := h(sesh); err != nil && !errors.Is(err, io.EOF) {
								sc.Logger.Error("exec middleware", "err", err)
								sesh.Fatal(err)
								return
							}

							err = sesh.Exit(0)
							if err != nil {
								sc.Logger.Error("subsystem exit", "err", err)
							}

							err = sesh.Close()
							if err != nil {
								sc.Logger.Error("subsystem close", "err", err)
							}
						case "pty-req":
							sesh.mu.Lock()
							found := sesh.pty != nil
							sesh.mu.Unlock()
							if found {
								err := req.Reply(false, nil)
								if err != nil {
									sc.Logger.Error("pty-req reply", "err", err)
								}
								return
							}

							ptyReq, ok := parsePtyRequest(req.Payload)
							if !ok {
								err := req.Reply(false, nil)
								if err != nil {
									sc.Logger.Error("pty-req reply", "err", err)
								}
								return
							}

							sesh.mu.Lock()
							sesh.pty = &ptyReq
							sesh.winch = make(chan Window, 1)
							sesh.mu.Unlock()

							sesh.winch <- ptyReq.Window
							err := req.Reply(ok, nil)
							if err != nil {
								sc.Logger.Error("pty-req reply", "err", err)
							}
						case "window-change":
							sesh.mu.Lock()
							found := sesh.pty != nil
							sesh.mu.Unlock()

							if !found {
								err := req.Reply(false, nil)
								if err != nil {
									sc.Logger.Error("pty-req reply", "err", err)
								}
								return
							}

							win, ok := parseWinchRequest(req.Payload)
							if ok {
								sesh.mu.Lock()
								sesh.pty.Window = win
								sesh.winch <- win
								sesh.mu.Unlock()
							}

							err := req.Reply(ok, nil)
							if err != nil {
								sc.Logger.Error("window-change reply", "err", err)
							}
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

type PubKeyAuthHandler func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error)

func NewSSHServerWithConfig(
	ctx context.Context,
	logger *slog.Logger,
	app, host, port, promPort, hostKey string,
	pubKeyAuthHandler PubKeyAuthHandler,
	middleware, subsystemMiddleware []SSHServerMiddleware,
	channelMiddleware map[string]SSHServerChannelMiddleware) (*SSHServer, error) {
	server := NewSSHServer(ctx, logger, &SSHServerConfig{
		App:        app,
		ListenAddr: fmt.Sprintf("%s:%s", host, port),
		ServerConfig: &ssh.ServerConfig{
			PublicKeyCallback: pubKeyAuthHandler,
		},
		Middleware:          middleware,
		SubsystemMiddleware: subsystemMiddleware,
		ChannelMiddleware:   channelMiddleware,
	})

	if promPort != "" {
		server.Config.PromListenAddr = fmt.Sprintf("%s:%s", host, promPort)
	}

	pemBytes, err := os.ReadFile(hostKey)
	if err != nil {
		logger.Error("failed to read private key file", "error", err)
		if !os.IsNotExist(err) {
			return nil, err
		}

		logger.Info("generating new private key")

		pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			logger.Error("failed to generate private key", "error", err)
			return nil, err
		}

		privb, err := ssh.MarshalPrivateKey(privKey, "")
		if err != nil {
			logger.Error("failed to marshal private key", "error", err)
			return nil, err
		}

		block := &pem.Block{
			Type:  "OPENSSH PRIVATE KEY",
			Bytes: privb.Bytes,
		}

		if err = os.MkdirAll(path.Dir(hostKey), 0700); err != nil {
			logger.Error("failed to create ssh_data directory", "error", err)
			return nil, err
		}

		pemBytes = pem.EncodeToMemory(block)

		if err = os.WriteFile(hostKey, pemBytes, 0600); err != nil {
			logger.Error("failed to write private key", "error", err)
			return nil, err
		}

		sshPubKey, err := ssh.NewPublicKey(pubKey)
		if err != nil {
			logger.Error("failed to create public key", "error", err)
			return nil, err
		}

		pubb := ssh.MarshalAuthorizedKey(sshPubKey)
		if err = os.WriteFile(fmt.Sprintf("%s.pub", hostKey), pubb, 0600); err != nil {
			logger.Error("failed to write public key", "error", err)
			return nil, err
		}
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		logger.Error("failed to parse private key", "error", err)
		return nil, err
	}

	server.Config.AddHostKey(signer)

	return server, nil
}

func KeysEqual(a, b ssh.PublicKey) bool {
	if a == nil || b == nil {
		return false
	}

	am := a.Marshal()
	bm := b.Marshal()
	return (len(am) == len(bm) && subtle.ConstantTimeCompare(am, bm) == 1)
}
