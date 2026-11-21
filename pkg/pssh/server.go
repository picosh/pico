package pssh

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path"
	"runtime/debug"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/antoniomika/syncmap"
	"github.com/go-andiamo/splitter"
	"github.com/picosh/pico/pkg/metrics"
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
	// started is when the session's shell, exec or subsystem began, and zero
	// before that.
	started time.Time

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
	_, err := s.SendRequest("exit-status", false, ssh.Marshal(&status))
	return err
}

func (sesh *SSHServerConnSession) Errorf(f string, v ...interface{}) {
	_, _ = fmt.Fprintf(sesh.Stderr(), f, v...)
}

func (sesh *SSHServerConnSession) Errorln(v ...interface{}) {
	_, _ = fmt.Fprintln(sesh.Stderr(), v...)
}

func (sesh *SSHServerConnSession) Printf(f string, v ...interface{}) {
	_, _ = fmt.Fprintf(sesh, f, v...)
}

func (sesh *SSHServerConnSession) Println(v ...interface{}) {
	_, _ = fmt.Fprintln(sesh, v...)
}

func (s *SSHServerConnSession) Fatal(err error) {
	_, _ = fmt.Fprintln(s.Stderr(), err)
	_, _ = fmt.Fprintf(s.Stderr(), "\r")
	_ = s.Exit(1)
	_ = s.Close()
}

// Pty returns a copy of the session's pty as it is now. Later sizes arrive
// on the channel.
func (s *SSHServerConnSession) Pty() (*Pty, <-chan Window, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pty == nil {
		return nil, nil, false
	}

	pty := *s.pty
	return &pty, s.winch, true
}

// claimCommand reports false when the session already started a shell, exec
// or subsystem, since a channel runs only one.
func (s *SSHServerConnSession) claimCommand() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.started.IsZero() {
		return false
	}
	s.started = time.Now()
	return true
}

// SetPty allocates the session's pty. It reports false when the session
// already has one.
func (s *SSHServerConnSession) SetPty(pty Pty) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pty != nil {
		return false
	}

	s.pty = &pty
	s.winch = make(chan Window, 1)
	s.winch <- pty.Window
	return true
}

// SetWindow resizes the session's pty. It reports false when the session has
// no pty.
func (s *SSHServerConnSession) SetWindow(win Window) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pty == nil {
		return false
	}

	s.pty.Window = win
	// Only the latest size matters, and a handler that never reads sizes
	// mustn't block this send.
	select {
	case <-s.winch:
	default:
	}
	s.winch <- win
	return true
}

// handleRequest runs req's handler before the session's next request is
// read, so a command sees what earlier requests like pty-req set up. It
// reports false when the session is ending and must not run another request.
func (s *SSHServerConnSession) handleRequest(req *ssh.Request) bool {
	var ok, panicked bool
	var then func()
	if h := s.SSHServer.Config.sessionRequestHandler(req.Type); h != nil {
		ok, then, panicked = s.callRequestHandler(h, req)
	}

	if err := req.Reply(ok, nil); err != nil {
		// The client never learned the request succeeded, so it can't be
		// expecting the command to run.
		s.Logger.Error("session request reply", "type", req.Type, "err", err)
		_ = s.Close()
		return false
	}

	if then == nil {
		return true
	}

	go func() {
		// A panic in a handler ends its session and leaves the server running.
		defer func() {
			if r := recover(); r != nil {
				s.Logger.Error("session request panic", "type", req.Type, "err", r, "stack", string(debug.Stack()))
				s.Fatal(fmt.Errorf("internal server error"))
			}
		}()
		then()
	}()
	return !panicked
}

func (s *SSHServerConnSession) callRequestHandler(h SSHServerRequestHandler, req *ssh.Request) (ok bool, then func(), panicked bool) {
	defer func() {
		if r := recover(); r != nil {
			s.Logger.Error("session request panic", "type", req.Type, "err", r, "stack", string(debug.Stack()))
			// Fatal runs after the reply and off the request loop, since its
			// write can block on the client's window.
			ok, then, panicked = false, func() { s.Fatal(fmt.Errorf("internal server error")) }, true
		}
	}()
	ok, then = h(req, s)
	return ok, then, false
}

// PtyRequestHandler is the default pty-req handler.
func PtyRequestHandler(req *ssh.Request, sesh *SSHServerConnSession) (bool, func()) {
	pty, ok := ParsePtyRequest(req.Payload)
	if !ok {
		return false, nil
	}
	return sesh.SetPty(pty), nil
}

// WindowChangeHandler is the default window-change handler.
func WindowChangeHandler(req *ssh.Request, sesh *SSHServerConnSession) (bool, func()) {
	win, ok := ParseWinchRequest(req.Payload)
	if !ok {
		return false, nil
	}
	return sesh.SetWindow(win), nil
}

// ShellHandler is the default shell and exec handler. It runs the server's
// Middleware.
func ShellHandler(req *ssh.Request, sesh *SSHServerConnSession) (bool, func()) {
	if !sesh.claimCommand() {
		return false, nil
	}

	cfg := sesh.SSHServer.Config
	if len(cfg.Middleware) == 0 {
		return false, func() { sesh.Fatal(fmt.Errorf("%s is not supported", req.Type)) }
	}

	command := "shell"

	if len(req.Payload) > 0 {
		var payload = struct{ Value string }{}
		if err := ssh.Unmarshal(req.Payload, &payload); err != nil {
			sesh.Logger.Error("shell/exec unmarshal", "err", err)
			return false, func() { sesh.Fatal(err) }
		}

		commaSplitter, _ := splitter.NewSplitter(
			' ',
			splitter.DoubleQuotes,
			splitter.SingleQuotes,
		)
		command = payload.Value
		cmdSlice, _ := commaSplitter.Split(command, splitter.StripQuotes)
		sesh.SetValue("command", cmdSlice)
	}

	if !utf8.ValidString(command) {
		command = base64.StdEncoding.EncodeToString([]byte(command))
	}

	return true, func() {
		if cfg.PromListenAddr != "" {
			labels := []string{sessionCommand(command), sessionUser(sesh)}
			sesh.SSHServer.SessionsCreated.WithLabelValues(labels...).Inc()
			defer func() {
				sesh.SSHServer.SessionsFinished.WithLabelValues(labels...).Inc()
				sesh.SSHServer.SessionsDuration.WithLabelValues(labels...).Add(time.Since(sesh.started).Seconds())
			}()
		}

		runMiddleware(sesh, cfg.Middleware)
	}
}

// SubsystemHandler is the default subsystem handler. It runs the server's
// SubsystemMiddleware, and closes the connection when there is none.
func SubsystemHandler(req *ssh.Request, sesh *SSHServerConnSession) (bool, func()) {
	if !sesh.claimCommand() {
		return false, nil
	}

	cfg := sesh.SSHServer.Config
	if len(cfg.SubsystemMiddleware) == 0 {
		return false, func() {
			sesh.Fatal(fmt.Errorf("subsystem is not supported"))
			_ = sesh.SSHServerConn.Close()
		}
	}

	return true, func() { runMiddleware(sesh, cfg.SubsystemMiddleware) }
}

func runMiddleware(sesh *SSHServerConnSession, middleware []SSHServerMiddleware) {
	h := func(*SSHServerConnSession) error { return nil }
	for _, m := range middleware {
		h = m(h)
	}

	if err := h(sesh); err != nil && !errors.Is(err, io.EOF) {
		sesh.Logger.Error("session middleware", "err", err)
		sesh.Fatal(err)
		return
	}

	if err := sesh.Exit(0); err != nil {
		sesh.Logger.Error("session exit", "err", err)
	}

	if err := sesh.Close(); err != nil {
		sesh.Logger.Error("session close", "err", err)
	}
}

// Write overrides the embedded Channel's Write to normalize line endings when PTY is allocated.
func (s *SSHServerConnSession) Write(p []byte) (n int, err error) {
	s.mu.Lock()
	hasPty := s.pty != nil
	s.mu.Unlock()

	if !hasPty {
		// No PTY, write as-is
		return s.Channel.Write(p)
	}

	// When PTY is active, ensure every \n is preceded by \r.
	// This ensures the cursor returns to column 0 before the newline.
	var buf bytes.Buffer
	for i := 0; i < len(p); i++ {
		if p[i] == '\n' {
			// Check if preceded by \r
			if i == 0 || p[i-1] != '\r' {
				buf.WriteByte('\r')
			}
			buf.WriteByte('\n')
		} else {
			buf.WriteByte(p[i])
		}
	}

	normalized := buf.Bytes()
	written, err := s.Channel.Write(normalized)

	// Return the count based on original data length, not normalized
	if written > len(p) {
		written = len(p)
	}
	return written, err
}

var _ context.Context = &SSHServerConnSession{}

func (sc *SSHServerConn) Handle(chans <-chan ssh.NewChannel, reqs <-chan *ssh.Request) error {
	defer func() {
		_ = sc.Close()
	}()

	for {
		select {
		case <-sc.Context().Done():
			return nil
		case newChan, ok := <-chans:
			if !ok {
				return nil
			}

			sc.Logger.Info("new channel", "type", newChan.ChannelType(), "extraData", newChan.ExtraData())
			chanFunc := sc.SSHServer.Config.ChannelMiddleware[newChan.ChannelType()]
			if chanFunc == nil {
				sc.Logger.Info("no channel middleware for type", "type", newChan.ChannelType())
				if err := newChan.Reject(ssh.UnknownChannelType, "unsupported channel type"); err != nil {
					sc.Logger.Error("channel reject", "err", err)
				}
				continue
			}

			go func() {
				defer func() {
					if r := recover(); r != nil {
						sc.Logger.Error("channel middleware panic", "err", r, "stack", string(debug.Stack()))
					}
				}()
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
			var replyOK bool
			var payload []byte
			if h := sc.SSHServer.Config.globalRequestHandler(req.Type); h != nil {
				replyOK, payload = sc.callGlobalRequestHandler(h, req)
			}
			// RFC 4254 gives a failure reply no payload.
			if !replyOK {
				payload = nil
			}
			if err := req.Reply(replyOK, payload); err != nil {
				sc.Logger.Error("global request reply", "type", req.Type, "err", err)
			}
		}
	}
}

func (sc *SSHServerConn) callGlobalRequestHandler(h SSHServerGlobalRequestHandler, req *ssh.Request) (ok bool, payload []byte) {
	defer func() {
		if r := recover(); r != nil {
			sc.Logger.Error("global request panic", "type", req.Type, "err", r, "stack", string(debug.Stack()))
			ok, payload = false, nil
		}
	}()
	return h(req, sc)
}

// KeepaliveHandler is the default keepalive@openssh.com handler.
func KeepaliveHandler(*ssh.Request, *SSHServerConn) (bool, []byte) {
	return true, nil
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

// SSHServerRequestHandler handles a request on a session channel. It runs
// before the session's next request is read, so it must not block. pssh
// replies with ok when the client wants a reply, then runs then, if set, in
// its own goroutine.
type SSHServerRequestHandler func(req *ssh.Request, sesh *SSHServerConnSession) (ok bool, then func())

// SSHServerGlobalRequestHandler handles a connection-level request. pssh
// replies with ok when the client wants a reply, and sends payload only with
// a success.
type SSHServerGlobalRequestHandler func(req *ssh.Request, sc *SSHServerConn) (ok bool, payload []byte)

var defaultSessionRequestHandlers = map[string]SSHServerRequestHandler{
	"pty-req":       PtyRequestHandler,
	"window-change": WindowChangeHandler,
	"shell":         ShellHandler,
	"exec":          ShellHandler,
	"subsystem":     SubsystemHandler,
}

var defaultGlobalRequestHandlers = map[string]SSHServerGlobalRequestHandler{
	"keepalive@openssh.com": KeepaliveHandler,
}

type SSHServerConfig struct {
	*ssh.ServerConfig
	App                 string
	ListenAddr          string
	PromListenAddr      string
	Middleware          []SSHServerMiddleware
	SubsystemMiddleware []SSHServerMiddleware
	// A nil entry in any of these maps rejects that type, default included.
	// Types without a handler are rejected.
	ChannelMiddleware map[string]SSHServerChannelMiddleware
	// SessionRequestHandlers and GlobalRequestHandlers are merged over the
	// defaults, keyed by request type.
	SessionRequestHandlers map[string]SSHServerRequestHandler
	GlobalRequestHandlers  map[string]SSHServerGlobalRequestHandler
	// HandshakeTimeout bounds the handshake and auth. Zero means
	// DefaultHandshakeTimeout and a negative value means no limit.
	HandshakeTimeout time.Duration
}

// DefaultHandshakeTimeout keeps a client that never finishes its handshake
// from holding a connection open.
const DefaultHandshakeTimeout = 30 * time.Second

func (c *SSHServerConfig) sessionRequestHandler(reqType string) SSHServerRequestHandler {
	if h, ok := c.SessionRequestHandlers[reqType]; ok {
		return h
	}
	return defaultSessionRequestHandlers[reqType]
}

func (c *SSHServerConfig) globalRequestHandler(reqType string) SSHServerGlobalRequestHandler {
	if h, ok := c.GlobalRequestHandlers[reqType]; ok {
		return h
	}
	return defaultGlobalRequestHandlers[reqType]
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

	Mu sync.Mutex
}

func (s *SSHServer) ListenAndServe() error {
	if s.Config.PromListenAddr != "" {
		s.SessionsCreated = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_created_total",
			Help: "The total number of sessions created",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command", "user"})

		s.SessionsFinished = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_finished_total",
			Help: "The total number of sessions created",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command", "user"})

		s.SessionsDuration = promauto.With(prometheus.DefaultRegisterer).NewCounterVec(prometheus.CounterOpts{
			Name: "pssh_sessions_duration_seconds",
			Help: "The total sessions duration in seconds",
			ConstLabels: prometheus.Labels{
				"app": s.Config.App,
			},
		}, []string{"command", "user"})

		metrics.RegisterServerInfo(prometheus.DefaultRegisterer, s.Config.App)

		// The handler reads the default registerer, so build it before the
		// listener is published rather than in the goroutine.
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())

		go func() {
			srv := &http.Server{Addr: s.Config.PromListenAddr, Handler: mux}

			go func() {
				<-s.Ctx.Done()
				s.Logger.Info("Prometheus server shutting down")
				_ = srv.Close()
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

	s.Mu.Lock()
	s.Listener = listen
	s.Mu.Unlock()
	defer func() {
		_ = listen.Close()
	}()

	go func() {
		<-s.Ctx.Done()
		_ = s.Close()
	}()

	// Errors like running out of file descriptors last a while, so back off
	// instead of spinning on them.
	var delay time.Duration
	for {
		conn, err := listen.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}

			delay = min(max(2*delay, 5*time.Millisecond), time.Second)
			s.Logger.Error("accept", "err", err, "retryIn", delay)
			select {
			case <-time.After(delay):
			case <-s.Ctx.Done():
				return nil
			}
			continue
		}
		delay = 0

		go func() {
			if err := s.HandleConn(conn); err != nil && !errors.Is(err, io.EOF) {
				s.Logger.Error("Error handling connection", "err", err, "remoteAddr", conn.RemoteAddr().String())
			}
		}()
	}
}

func (s *SSHServer) HandleConn(conn net.Conn) error {
	defer func() {
		_ = conn.Close()
	}()

	timeout := s.Config.HandshakeTimeout
	if timeout == 0 {
		timeout = DefaultHandshakeTimeout
	}
	if timeout > 0 {
		if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
			return err
		}
	}

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, s.Config.ServerConfig)
	if err != nil {
		return err
	}

	if timeout > 0 {
		if err := conn.SetDeadline(time.Time{}); err != nil {
			// x/crypto closes the conn when a client hangs up right after
			// auth, which is a normal disconnect.
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
	}

	newLogger := s.Logger.With(
		"remoteAddr", conn.RemoteAddr().String(),
		"sshUser", sshConn.User(),
	)

	if pubKey, ok := sshConn.Permissions.Extensions["pubkey"]; ok {
		newLogger = newLogger.With("pubkey", pubKey)
	}

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

	s.Mu.Lock()
	listener := s.Listener
	s.Mu.Unlock()

	if listener == nil {
		return nil
	}
	return listener.Close()
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
		config.ChannelMiddleware["session"] = handleSessionChannel
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

func handleSessionChannel(newChan ssh.NewChannel, sc *SSHServerConn) error {
	channel, requests, err := newChan.Accept()
	if err != nil {
		sc.Logger.Error("accept session channel", "err", err)
		return err
	}

	ctx, cancelFunc := context.WithCancel(sc.Ctx)
	// The client closing the session ends its command, even while
	// the connection stays open for other sessions.
	defer cancelFunc()

	sesh := &SSHServerConnSession{
		Channel:       channel,
		SSHServerConn: sc,
		Ctx:           ctx,
		CancelFunc:    cancelFunc,
	}

	// Requests left unread once the session ends would back up and stall the
	// connection's other channels.
	defer func() { go ssh.DiscardRequests(requests) }()

	for {
		select {
		case <-sesh.Done():
			return nil
		case req, ok := <-requests:
			if !ok {
				return nil
			}

			// select picks at random when both cases are ready, so a request
			// can arrive here after the session ended.
			if sesh.Err() != nil {
				_ = req.Reply(false, nil)
				return nil
			}

			sc.Logger.Info("new session request", "type", req.Type, "wantReply", req.WantReply, "payload", req.Payload)
			if !sesh.handleRequest(req) {
				return nil
			}
		}
	}
}

// sessionCommand is the subcommand alone, since arguments would add a series
// for every distinct command line.
func sessionCommand(command string) string {
	if fields := strings.Fields(command); len(fields) > 0 {
		return fields[0]
	}
	return command
}

// sessionUser is the name the auth handler confirmed, or "" without one.
func sessionUser(s *SSHServerConnSession) string {
	if perms := s.Permissions(); perms != nil {
		return perms.Extensions["user"]
	}
	return ""
}

type PubKeyAuthHandler func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error)

func NewSSHServerWithConfig(
	ctx context.Context,
	logger *slog.Logger,
	app, host, port, promPort, hostKey string,
	pubKeyAuthHandler PubKeyAuthHandler,
	middleware, subsystemMiddleware []SSHServerMiddleware,
	channelMiddleware map[string]SSHServerChannelMiddleware,
) (*SSHServer, error) {
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

	if hostKey != "" {
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
	}

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
