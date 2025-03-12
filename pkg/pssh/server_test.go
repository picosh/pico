package pssh_test

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"golang.org/x/crypto/ssh"
)

func TestNewSSHServer(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})

	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.Ctx == nil {
		t.Error("expected non-nil context")
	}

	if server.Logger == nil {
		t.Error("expected non-nil logger")
	}

	if server.Config == nil {
		t.Error("expected non-nil config")
	}

	if server.Conns == nil {
		t.Error("expected non-nil connections map")
	}
}

func TestNewSSHServerConn(t *testing.T) {
	ctx := context.Background()
	logger := slog.Default()
	server := pssh.NewSSHServer(ctx, logger, &pssh.SSHServerConfig{})
	conn := &ssh.ServerConn{}

	serverConn := pssh.NewSSHServerConn(ctx, logger, conn, server)

	if serverConn == nil {
		t.Fatal("expected non-nil server connection")
	}

	if serverConn.Ctx == nil {
		t.Error("expected non-nil context")
	}

	if serverConn.Logger == nil {
		t.Error("expected non-nil logger")
	}

	if serverConn.Conn != conn {
		t.Error("expected conn to match")
	}

	if serverConn.SSHServer != server {
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

	if server == nil {
		t.Fatal("expected non-nil server")
	}

	if server.Ctx == nil {
		t.Error("expected non-nil context even when nil is passed")
	}

	if server.Logger == nil {
		t.Error("expected non-nil logger even when nil is passed")
	}

	// Test with nil context and logger for connection
	//nolint:staticcheck // SA1012 ignores nil check
	//lint:ignore SA1012 ignores nil check
	conn := pssh.NewSSHServerConn(nil, nil, &ssh.ServerConn{}, server)

	if conn == nil {
		t.Fatal("expected non-nil server connection")
	}

	if conn.Ctx == nil {
		t.Error("expected non-nil context even when nil is passed")
	}

	if conn.Logger == nil {
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
	defer client.Close()

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
	client.Close()
	server_conn.Close()

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
