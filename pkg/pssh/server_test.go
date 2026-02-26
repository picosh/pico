package pssh_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ed25519"
	"golang.org/x/crypto/ssh"
)

// MockChannel implements ssh.Channel for testing PTY line-ending normalization.
type MockChannel struct {
	data []byte
}

func (m *MockChannel) Read(data []byte) (n int, err error) {
	return 0, io.EOF
}

func (m *MockChannel) Write(data []byte) (n int, err error) {
	m.data = append(m.data, data...)
	return len(data), nil
}

func (m *MockChannel) Close() error {
	return nil
}

func (m *MockChannel) CloseWrite() error {
	return nil
}

func (m *MockChannel) SendRequest(name string, wantReply bool, data []byte) (bool, error) {
	return false, nil
}

func (m *MockChannel) Stderr() io.ReadWriter {
	return &bytes.Buffer{}
}

func (m *MockChannel) Data() []byte {
	return m.data
}

// setPtyField sets the private pty field on a session (test only).
func setPtyField(session *pssh.SSHServerConnSession, pty *pssh.Pty) {
	field := reflect.ValueOf(session).Elem().FieldByName("pty")
	reflect.NewAt(field.Type(), unsafe.Pointer(field.UnsafeAddr())).Elem().Set(reflect.ValueOf(pty))
}

// TestSSHServerConnSessionWritePtyLineEnding verifies that line-ending normalization works correctly.
func TestSSHServerConnSessionWritePtyLineEnding(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	// Create a mock SSH connection
	sshConn := &ssh.ServerConn{}
	serverConn := pssh.NewSSHServerConn(ctx, logger, sshConn, server)

	// Create session with mock channel
	mockChannel := &MockChannel{}

	createSession := func() *pssh.SSHServerConnSession {
		return &pssh.SSHServerConnSession{
			Channel:       mockChannel,
			SSHServerConn: serverConn,
			Ctx:           ctx,
		}
	}

	t.Run("no PTY - write as-is", func(t *testing.T) {
		mockChannel.data = nil
		session := createSession()
		// No PTY is allocated, so behavior should write as-is

		// Write text with just \n (no \r)
		input := []byte("line1\nline2\nline3")
		n, err := session.Write(input)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if n != len(input) {
			t.Errorf("expected %d bytes written, got %d", len(input), n)
		}
		if !slices.Equal(mockChannel.data, input) {
			t.Errorf("expected %q, got %q", string(input), string(mockChannel.data))
		}
	})

	t.Run("with PTY - normalize bare newlines to CRLF", func(t *testing.T) {
		mockChannel.data = nil
		session := createSession()
		// Set PTY on the session
		pty := &pssh.Pty{Term: "xterm", Window: pssh.Window{Width: 80, Height: 24}}
		setPtyField(session, pty)

		// Write text with just \n (no \r)
		input := []byte("line1\nline2\nline3")
		expected := []byte("line1\r\nline2\r\nline3")

		n, err := session.Write(input)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		// Should return original length
		if n != len(input) {
			t.Errorf("expected %d bytes written, got %d", len(input), n)
		}
		// Should write normalized data
		if !slices.Equal(mockChannel.data, expected) {
			t.Errorf("expected %q, got %q", string(expected), string(mockChannel.data))
		}
	})

	t.Run("with PTY - preserve existing CRLF", func(t *testing.T) {
		mockChannel.data = nil
		session := createSession()
		pty := &pssh.Pty{Term: "xterm", Window: pssh.Window{Width: 80, Height: 24}}
		setPtyField(session, pty)

		// Write text that already has proper \r\n
		input := []byte("line1\r\nline2\r\nline3")
		expected := []byte("line1\r\nline2\r\nline3") // Should not duplicate

		n, err := session.Write(input)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if n != len(input) {
			t.Errorf("expected %d bytes written, got %d", len(input), n)
		}
		if !slices.Equal(mockChannel.data, expected) {
			t.Errorf("expected %q, got %q", string(expected), string(mockChannel.data))
		}
	})

	t.Run("with PTY - mixed newlines normalized correctly", func(t *testing.T) {
		mockChannel.data = nil
		session := createSession()
		pty := &pssh.Pty{Term: "xterm", Window: pssh.Window{Width: 80, Height: 24}}
		setPtyField(session, pty)

		// Mix of \n and \r\n
		input := []byte("line1\nline2\r\nline3\nline4")
		expected := []byte("line1\r\nline2\r\nline3\r\nline4")

		n, err := session.Write(input)

		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if n != len(input) {
			t.Errorf("expected %d bytes written, got %d", len(input), n)
		}
		if !slices.Equal(mockChannel.data, expected) {
			t.Errorf("expected %q, got %q", string(expected), string(mockChannel.data))
		}
	})

	t.Run("staircase bug regression - sequential writes maintain formatting", func(t *testing.T) {
		// This test simulates the staircase bug where multiple writes
		// without proper CRLF would cause progressive indentation
		mockChannel.data = nil
		session := createSession()
		pty := &pssh.Pty{Term: "xterm", Window: pssh.Window{Width: 80, Height: 24}}
		setPtyField(session, pty)

		// Simulate help text being written in multiple chunks
		writes := []string{
			"NAME:\n",
			"\tssh - A tool\n",
			"\n",
			"USAGE:\n",
			"\tssh [options]\n",
		}

		for _, w := range writes {
			_, err := session.Write([]byte(w))
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		}

		// Check that every \n is preceded by \r to prevent staircase
		output := mockChannel.data
		for i := 0; i < len(output); i++ {
			if output[i] == '\n' {
				if i == 0 {
					t.Errorf("newline at position 0 not preceded by carriage return")
				} else if output[i-1] != '\r' {
					t.Errorf("newline at position %d not preceded by carriage return, got %q before it",
						i, string(output[i-1]))
				}
			}
		}
	})
}

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

	// Use dynamic port (0) to avoid port conflicts
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{
		ListenAddr: "127.0.0.1:0",
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

	// Wait for server to be ready and get the actual listening address
	var actualAddr string
	for i := 0; i < 50; i++ {
		server.Mu.Lock()
		listener := server.Listener
		server.Mu.Unlock()
		if listener != nil {
			actualAddr = listener.Addr().String()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	if actualAddr == "" {
		t.Fatal("server listener not ready")
	}

	// Send command to server
	_, _ = user.CmdAddr(nil, actualAddr, "accept --comment 'here we go' 101")

	time.Sleep(100 * time.Millisecond)

	expectedCommand := []string{"accept", "--comment", "here we go", "101"}
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

func (s UserSSH) CmdAddr(patch []byte, addr string, cmd string) (string, error) {
	config := &ssh.ClientConfig{
		User: s.username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(s.signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", addr, config)
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
