package pssh_test

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

func TestNewSSHServer(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	if server == nil { //nolint:all
		t.Fatal("expected non-nil server")
	}

	if server.Ctx == nil { //nolint:all
		t.Error("expected non-nil context")
	}

	if server.Logger == nil { //nolint:all
		t.Error("expected non-nil logger")
	}

	if server.Config == nil { //nolint:all
		t.Error("expected non-nil config")
	}

	if server.Conns == nil { //nolint:all
		t.Error("expected non-nil connections map")
	}
}

func TestNewSSHServerConn(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	conn := &ssh.ServerConn{}

	serverConn := pssh.NewSSHServerConn(ctx, logger, conn, server)

	if serverConn == nil { //nolint:all
		t.Fatal("expected non-nil server connection")
	}

	if serverConn.Ctx == nil { //nolint:all
		t.Error("expected non-nil context")
	}

	if serverConn.Logger == nil { //nolint:all
		t.Error("expected non-nil logger")
	}

	if serverConn.Conn != conn { //nolint:all
		t.Error("expected conn to match")
	}

	if serverConn.SSHServer != server { //nolint:all
		t.Error("expected server to match")
	}
}

func TestSSHServerConnClose(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	conn := &ssh.ServerConn{}

	serverConn := pssh.NewSSHServerConn(ctx, logger, conn, server)
	err := serverConn.Close()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should be canceled after close
	select {
	case <-serverConn.Ctx.Done():
		// Context was canceled as expected
	default:
		t.Error("context was not canceled after Close()")
	}
}

func TestSSHServerClose(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	// Create a mock listener to test Close()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	server.Listener = listener
	err = server.Close()

	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	// Should be canceled after close
	select {
	case <-server.Ctx.Done():
		// Context was canceled as expected
	default:
		t.Error("context was not canceled after Close()")
	}
}

func TestSSHServerNilParams(t *testing.T) {
	// Test with nil context and logger
	//nolint:staticcheck // SA1012 ignores nil check
	//lint:ignore SA1012 ignores nil check
	server := pssh.NewSSHServer(nil, nil, nil)

	if server == nil { //nolint:all
		t.Fatal("expected non-nil server")
	}

	if server.Ctx == nil { //nolint:all
		t.Error("expected non-nil context even when nil is passed")
	}

	if server.Logger == nil { //nolint:all
		t.Error("expected non-nil logger even when nil is passed")
	}

	// Test with nil context and logger for connection
	//nolint:staticcheck // SA1012 ignores nil check
	//lint:ignore SA1012 ignores nil check
	conn := pssh.NewSSHServerConn(nil, nil, &ssh.ServerConn{}, server)

	if conn == nil { //nolint:all
		t.Fatal("expected non-nil server connection")
	}

	if conn.Ctx == nil { //nolint:all
		t.Error("expected non-nil context even when nil is passed")
	}

	if conn.Logger == nil { //nolint:all
		t.Error("expected non-nil logger even when nil is passed")
	}
}

func TestSSHServerHandleConn(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	// Setup a basic SSH server config
	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}

	server.Config.ServerConfig = config

	// Create a mock connection
	client, server_conn := net.Pipe()
	defer func() {
		_ = client.Close()
	}()

	// Start HandleConn in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- server.HandleConn(server_conn)
	}()

	// Configure SSH client
	clientConfig := &ssh.ClientConfig{
		User:            "testuser",
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	// Try to establish SSH connection
	_, _, _, err := ssh.NewClientConn(client, "", clientConfig)

	// It should fail since we're using a pipe and not a proper SSH handshake
	if err == nil {
		t.Error("expected SSH handshake to fail with test pipe")
	}

	// Close connections to ensure HandleConn returns
	_ = client.Close()
	_ = server_conn.Close()

	// Wait for HandleConn to return
	select {
	case <-errChan:
		// Expected HandleConn to return
	case <-time.After(2 * time.Second):
		t.Error("HandleConn did not return after connection closed")
	}
}

func TestSSHServerListenAndServe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	config := &ssh.ServerConfig{
		NoClientAuth: true,
	}

	// Set a random port
	port := "127.0.0.1:0"
	server.Config.ListenAddr = port
	server.Config.ServerConfig = config

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		errChan <- err
	}()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Trigger cancellation to stop the server
	cancel()

	// Wait for server to stop
	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not shut down in time")
	}
}

func TestSSHServerConnHandle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	conn := &ssh.ServerConn{}

	serverConn := pssh.NewSSHServerConn(ctx, logger, conn, server)

	// Create channels for testing
	chans := make(chan ssh.NewChannel)
	reqs := make(chan *ssh.Request)

	// Start handle in a goroutine
	errChan := make(chan error, 1)
	go func() {
		errChan <- serverConn.Handle(chans, reqs)
	}()

	// Ensure handle returns when context is canceled
	cancel()

	// Wait for handle to return
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Handle did not return after context canceled")
	}
}

func TestSSHServerCommandParsing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := slog.Default()
	var capturedCommand []string

	user := GenerateKey()

	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{
		ListenAddr: "localhost:2222",
		Middleware: []pssh.SSHServerMiddleware{
			func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
				return func(sesh *pssh.SSHServerConnSession) error {
					capturedCommand = sesh.Command()
					return next(sesh)
				}
			},
		},
		ServerConfig: &ssh.ServerConfig{
			NoClientAuth: true,
			NoClientAuthCallback: func(ssh.ConnMetadata) (*ssh.Permissions, error) {
				return &ssh.Permissions{
					Extensions: map[string]string{
						"pubkey": shared.KeyForKeyText(user.signer.PublicKey()),
					},
				}, nil
			},
		},
	})
	server.Config.AddHostKey(user.signer)

	// Start server in a goroutine
	errChan := make(chan error, 1)
	go func() {
		err := server.ListenAndServe()
		errChan <- err
	}()

	// Wait a bit for the server to start
	time.Sleep(100 * time.Millisecond)

	// Send command to server
	_, _ = user.Cmd(nil, "accept --comment 'here we go' 101")

	time.Sleep(100 * time.Millisecond)

	expectedCommand := []string{"accept", "--comment", "'here we go'", "101"}
	if !slices.Equal(expectedCommand, capturedCommand) {
		t.Error("command not exected", capturedCommand, len(capturedCommand), expectedCommand, len(expectedCommand))
	}

	// Trigger cancellation to stop the server
	cancel()

	// Wait for server to stop
	select {
	case err := <-errChan:
		if err != nil && !errors.Is(err, net.ErrClosed) {
			t.Errorf("unexpected error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("server did not shut down in time")
	}
}

type UserSSH struct {
	username string
	signer   ssh.Signer
}

func NewUserSSH(username string, signer ssh.Signer) *UserSSH {
	return &UserSSH{
		username: username,
		signer:   signer,
	}
}

func (s UserSSH) Public() string {
	pubkey := s.signer.PublicKey()
	return string(ssh.MarshalAuthorizedKey(pubkey))
}

func (s UserSSH) MustCmd(patch []byte, cmd string) string {
	res, err := s.Cmd(patch, cmd)
	if err != nil {
		panic(err)
	}
	return res
}

func (s UserSSH) Cmd(patch []byte, cmd string) (string, error) {
	host := "localhost:2222"

	config := &ssh.ClientConfig{
		User: s.username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(s.signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", host, config)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = client.Close()
	}()

	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return "", err
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}

	if err := session.Start(cmd); err != nil {
		return "", err
	}

	if patch != nil {
		_, err = stdinPipe.Write(patch)
		if err != nil {
			return "", err
		}
	}

	_ = stdinPipe.Close()

	if err := session.Wait(); err != nil {
		return "", err
	}

	buf := new(strings.Builder)
	_, err = io.Copy(buf, stdoutPipe)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func GenerateKey() UserSSH {
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}

	userSigner, err := ssh.NewSignerFromKey(userKey)
	if err != nil {
		panic(err)
	}

	return UserSSH{
		username: "user",
		signer:   userSigner,
	}
}
