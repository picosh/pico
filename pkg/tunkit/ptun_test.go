package tunkit

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"golang.org/x/crypto/ssh"
)

type fakeTunnel struct {
	closed chan struct{}
}

func (f *fakeTunnel) GetLogger() *slog.Logger { return slog.New(slog.DiscardHandler) }

func (f *fakeTunnel) CreateConn(*pssh.SSHServerConnSession) (net.Conn, error) {
	server, client := net.Pipe()
	go func() {
		_, _ = server.Write([]byte("hi"))
		_ = server.Close()
	}()
	return client, nil
}

func (f *fakeTunnel) Close(*pssh.SSHServerConnSession) error {
	close(f.closed)
	return nil
}

// A forwarded channel's handler is closed once the forward ends, while the
// SSH connection stays open.
func TestLocalForwardClosesHandler(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	tunnel := &fakeTunnel{closed: make(chan struct{})}
	server := pssh.NewSSHServer(ctx, slog.New(slog.DiscardHandler), &pssh.SSHServerConfig{
		ListenAddr: "127.0.0.1:0",
		ServerConfig: &ssh.ServerConfig{
			NoClientAuth: true,
			NoClientAuthCallback: func(ssh.ConnMetadata) (*ssh.Permissions, error) {
				return &ssh.Permissions{Extensions: map[string]string{}}, nil
			},
		},
		ChannelMiddleware: map[string]pssh.SSHServerChannelMiddleware{
			"direct-tcpip": LocalForwardHandler(tunnel),
		},
	})
	server.Config.AddHostKey(signer)
	go func() { _ = server.ListenAndServe() }()

	var addr string
	for range 50 {
		server.Mu.Lock()
		if server.Listener != nil {
			addr = server.Listener.Addr().String()
		}
		server.Mu.Unlock()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("server listener not ready")
	}

	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	conn, err := client.Dial("tcp", "localhost:80")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 2)
	_, _ = conn.Read(buf)
	_ = conn.Close()

	select {
	case <-tunnel.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("tunnel handler was not closed after the forward ended")
	}
}
