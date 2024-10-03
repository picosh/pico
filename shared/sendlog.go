package shared

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

type MultiHandler struct {
	Handlers []slog.Handler
	mu       sync.Mutex
}

func (m *MultiHandler) Enabled(ctx context.Context, l slog.Level) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	for _, h := range m.Handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}

	return false
}

func (m *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	var errs []error
	for _, h := range m.Handlers {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}

	return errors.Join(errs...)
}

func (m *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	m.mu.Lock()
	defer m.mu.Unlock()

	var handlers []slog.Handler

	for _, h := range m.Handlers {
		handlers = append(handlers, h.WithAttrs(slices.Clone(attrs)))
	}

	return &MultiHandler{
		Handlers: handlers,
	}
}

func (m *MultiHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return m
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var handlers []slog.Handler

	for _, h := range m.Handlers {
		handlers = append(handlers, h.WithGroup(name))
	}

	return &MultiHandler{
		Handlers: handlers,
	}
}

type SendLogWriter struct {
	SSHClient        *ssh.Client
	Session          *ssh.Session
	StdinPipe        io.WriteCloser
	Done             chan struct{}
	Messages         chan []byte
	Timeout          time.Duration
	BufferSize       int
	closeOnce        sync.Once
	closeMessageOnce sync.Once
	startOnce        sync.Once
	connecMu         sync.Mutex
}

func (c *SendLogWriter) Close() error {
	c.connecMu.Lock()
	defer c.connecMu.Unlock()

	if c.Done != nil {
		c.closeOnce.Do(func() {
			close(c.Done)
		})
	}

	if c.Messages != nil {
		c.closeMessageOnce.Do(func() {
			close(c.Messages)
		})
	}

	var errs []error

	if c.StdinPipe != nil {
		errs = append(errs, c.StdinPipe.Close())
	}

	if c.Session != nil {
		errs = append(errs, c.Session.Close())
	}

	if c.SSHClient != nil {
		errs = append(errs, c.SSHClient.Close())
	}

	return errors.Join(errs...)
}

func (c *SendLogWriter) Open() error {
	c.Close()

	c.connecMu.Lock()

	c.Done = make(chan struct{})
	c.Messages = make(chan []byte, c.BufferSize)

	sshClient, err := createSSHClient(
		GetEnv("PICO_SENDLOG_ENDPOINT", "send.pico.sh:22"),
		GetEnv("PICO_SENDLOG_KEY", "ssh_data/term_info_ed25519"),
		GetEnv("PICO_SENDLOG_PASSPHRASE", ""),
		GetEnv("PICO_SENDLOG_REMOTE_HOST", "send.pico.sh"),
		GetEnv("PICO_SENDLOG_USER", "pico"),
	)
	if err != nil {
		c.connecMu.Unlock()
		return err
	}

	session, err := sshClient.NewSession()
	if err != nil {
		c.connecMu.Unlock()
		return err
	}

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		c.connecMu.Unlock()
		return err
	}

	err = session.Start("pub log-sink -b=false")
	if err != nil {
		c.connecMu.Unlock()
		return err
	}

	c.SSHClient = sshClient
	c.Session = session
	c.StdinPipe = stdinPipe

	c.closeOnce = sync.Once{}
	c.startOnce = sync.Once{}

	c.connecMu.Unlock()

	c.Start()

	return nil
}

func (c *SendLogWriter) Start() {
	c.startOnce.Do(func() {
		go func() {
			defer c.Reconnect()

			for {
				select {
				case data, ok := <-c.Messages:
					_, err := c.StdinPipe.Write(data)
					if !ok || err != nil {
						slog.Error("received error on write, reopening logger", "error", err)
						return
					}
				case <-c.Done:
					return
				}
			}
		}()
	})
}

func (c *SendLogWriter) Write(data []byte) (int, error) {
	c.connecMu.Lock()
	defer c.connecMu.Unlock()

	var (
		n   int
		err error
	)

	if c.Messages == nil || c.Done == nil {
		return n, fmt.Errorf("logger not viable")
	}

	select {
	case c.Messages <- data:
		n = len(data)
	case <-time.After(c.Timeout):
		err = fmt.Errorf("unable to send data within timeout")
	case <-c.Done:
		break
	}

	return n, err
}

func (c *SendLogWriter) Reconnect() {
	go func() {
		for {
			err := c.Open()
			if err != nil {
				slog.Error("unable to open send logger. retrying in 10 seconds", "error", err)
			} else {
				return
			}

			<-time.After(10 * time.Second)
		}
	}()
}

func createSSHClient(remoteHost string, keyLocation string, keyPassphrase string, remoteHostname string, remoteUser string) (*ssh.Client, error) {
	if !strings.Contains(remoteHost, ":") {
		remoteHost += ":22"
	}

	rawConn, err := net.Dial("tcp", remoteHost)
	if err != nil {
		return nil, err
	}

	keyPath, err := filepath.Abs(keyLocation)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(keyPath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}

	var signer ssh.Signer

	if keyPassphrase != "" {
		signer, err = ssh.ParsePrivateKeyWithPassphrase(data, []byte(keyPassphrase))
	} else {
		signer, err = ssh.ParsePrivateKey(data)
	}

	if err != nil {
		return nil, err
	}

	sshConn, chans, reqs, err := ssh.NewClientConn(rawConn, remoteHostname, &ssh.ClientConfig{
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		User:            remoteUser,
	})

	if err != nil {
		return nil, err
	}

	sshClient := ssh.NewClient(sshConn, chans, reqs)

	return sshClient, nil
}

func SendLogRegister(logger *slog.Logger, buffer int) (*slog.Logger, error) {
	if buffer < 0 {
		buffer = 0
	}

	currentHandler := logger.Handler()

	logWriter := &SendLogWriter{
		Timeout:    10 * time.Millisecond,
		BufferSize: buffer,
	}

	logWriter.Reconnect()

	return slog.New(
		&MultiHandler{
			Handlers: []slog.Handler{
				currentHandler,
				slog.NewJSONHandler(logWriter, &slog.HandlerOptions{
					AddSource: true,
					Level:     slog.LevelDebug,
				}),
			},
		},
	), nil
}

var _ io.Writer = (*SendLogWriter)(nil)
var _ slog.Handler = (*MultiHandler)(nil)
