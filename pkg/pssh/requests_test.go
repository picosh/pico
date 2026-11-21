package pssh_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/pssh"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
)

func dialTestServer(t *testing.T, addr string) *ssh.Client {
	t.Helper()
	client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "user", HostKeyCallback: ssh.InsecureIgnoreHostKey()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// within fails the test when fn doesn't return in time, which is how a
// request that never gets a reply shows up.
func within(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("no reply from server")
	}
}

type envKey struct{}

func TestSessionRequestHandlerRunsBeforeCommand(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{
			"env": func(req *ssh.Request, sesh *pssh.SSHServerConnSession) (bool, func()) {
				var kv struct{ Name, Value string }
				if err := ssh.Unmarshal(req.Payload, &kv); err != nil {
					return false, nil
				}
				sesh.SetValue(envKey{}, kv.Value)
				return true, nil
			},
		},
		Middleware: []pssh.SSHServerMiddleware{
			func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
				return func(sesh *pssh.SSHServerConnSession) error {
					value, _ := sesh.Value(envKey{}).(string)
					_, _ = sesh.Write([]byte(value))
					return next(sesh)
				}
			},
		},
	})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Setenv("FOO", "bar"); err != nil {
		t.Fatalf("env request rejected: %v", err)
	}
	out, err := session.Output("cmd")
	if err != nil || string(out) != "bar" {
		t.Fatalf("output = %q, %v", out, err)
	}
}

func TestNilSessionRequestHandlerDisablesDefault(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{"pty-req": nil},
	})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		if err := session.RequestPty("xterm", 24, 80, ssh.TerminalModes{}); err == nil {
			t.Error("pty-req accepted with its handler disabled")
		}
	})
}

func TestUnhandledSessionRequestIsRejected(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		ok, err := session.SendRequest("unknown@example.com", true, nil)
		if err != nil || ok {
			t.Errorf("reply = %v, %v", ok, err)
		}
	})
}

func TestGlobalRequests(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		GlobalRequestHandlers: map[string]pssh.SSHServerGlobalRequestHandler{
			"ping@example.com": func(*ssh.Request, *pssh.SSHServerConn) (bool, []byte) {
				return true, []byte("pong")
			},
		},
	})
	client := dialTestServer(t, addr)

	within(t, func() {
		ok, payload, err := client.SendRequest("ping@example.com", true, nil)
		if err != nil || !ok || string(payload) != "pong" {
			t.Errorf("ping reply = %v, %q, %v", ok, payload, err)
		}

		ok, _, err = client.SendRequest("keepalive@openssh.com", true, nil)
		if err != nil || !ok {
			t.Errorf("keepalive reply = %v, %v", ok, err)
		}

		ok, _, err = client.SendRequest("unknown@example.com", true, nil)
		if err != nil || ok {
			t.Errorf("unknown request reply = %v, %v", ok, err)
		}
	})
}

func TestUnhandledChannelIsRejected(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{})
	client := dialTestServer(t, addr)

	within(t, func() {
		_, _, err := client.OpenChannel("unknown@example.com", nil)
		var openErr *ssh.OpenChannelError
		if !errors.As(err, &openErr) || openErr.Reason != ssh.UnknownChannelType {
			t.Errorf("open error = %v", err)
		}
	})
}

func TestAgentForwarding(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{
			pssh.AgentRequestType: pssh.AgentRequestHandler,
		},
		Middleware: []pssh.SSHServerMiddleware{
			func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
				return func(sesh *pssh.SSHServerConnSession) error {
					ch, err := sesh.OpenAgentChannel()
					if err != nil {
						_, _ = sesh.Write([]byte("no agent"))
						return next(sesh)
					}
					defer func() { _ = ch.Close() }()
					keys, err := agent.NewClient(ch).List()
					if err != nil {
						return err
					}
					_, _ = fmt.Fprintf(sesh, "%d keys", len(keys))
					return next(sesh)
				}
			},
		},
	})
	client := dialTestServer(t, addr)

	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: key}); err != nil {
		t.Fatal(err)
	}
	if err := agent.ForwardToAgent(client, keyring); err != nil {
		t.Fatal(err)
	}

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := agent.RequestAgentForwarding(session); err != nil {
		t.Fatal(err)
	}
	out, err := session.Output("cmd")
	if err != nil || string(out) != "1 keys" {
		t.Fatalf("forwarded session output = %q, %v", out, err)
	}

	session, err = client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	out, err = session.Output("cmd")
	if err != nil || string(out) != "no agent" {
		t.Fatalf("unforwarded session output = %q, %v", out, err)
	}
}

func TestHandshakeTimeout(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{HandshakeTimeout: 100 * time.Millisecond})

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}

	// The server sends its banner and then has to hang up on a client that
	// never answers.
	if _, err := io.ReadAll(conn); err != nil {
		t.Fatalf("server kept a stalled handshake open: %v", err)
	}
}

func TestPtyIsACopy(t *testing.T) {
	sesh := &pssh.SSHServerConnSession{}
	sesh.SetPty(pssh.Pty{Term: "xterm", Window: pssh.Window{Width: 80, Height: 24}})

	before, _, _ := sesh.Pty()
	sesh.SetWindow(pssh.Window{Width: 100, Height: 30})
	after, _, _ := sesh.Pty()

	if before.Window.Width != 80 || after.Window.Width != 100 {
		t.Fatalf("widths = %d then %d, want 80 then 100", before.Window.Width, after.Window.Width)
	}
}

func TestSecondExecIsRejected(t *testing.T) {
	var runs atomic.Int32
	release := make(chan struct{})
	addr := startTestServer(t, func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			runs.Add(1)
			<-release
			return next(sesh)
		}
	})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start("first"); err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		ok, err := session.SendRequest("exec", true, ssh.Marshal(struct{ Value string }{"second"}))
		if err != nil || ok {
			t.Errorf("second exec reply = %v, %v", ok, err)
		}
	})
	close(release)
	_ = session.Wait()

	if n := runs.Load(); n != 1 {
		t.Fatalf("middleware ran %d times, want 1", n)
	}
}

func TestCloseBeforeListen(t *testing.T) {
	server := pssh.NewSSHServer(context.Background(), slog.New(slog.DiscardHandler), nil)
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestPanickingRequestHandlerReplies(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{
			"boom@example.com": func(*ssh.Request, *pssh.SSHServerConnSession) (bool, func()) {
				panic("boom")
			},
		},
	})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		ok, err := session.SendRequest("boom@example.com", true, nil)
		if err != nil || ok {
			t.Errorf("reply = %v, %v, want a failure reply", ok, err)
		}
	})
}

func TestRepeatAgentRequestIsRejected(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{
			pssh.AgentRequestType: pssh.AgentRequestHandler,
		},
	})
	client := dialTestServer(t, addr)

	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	within(t, func() {
		first, err := session.SendRequest(pssh.AgentRequestType, true, nil)
		if err != nil || !first {
			t.Fatalf("first reply = %v, %v", first, err)
		}
		second, err := session.SendRequest(pssh.AgentRequestType, true, nil)
		if err != nil || second {
			t.Errorf("second reply = %v, %v", second, err)
		}
	})
}

func TestFailedGlobalRequestHasNoPayload(t *testing.T) {
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		GlobalRequestHandlers: map[string]pssh.SSHServerGlobalRequestHandler{
			"fail@example.com": func(*ssh.Request, *pssh.SSHServerConn) (bool, []byte) {
				return false, []byte("ignored")
			},
		},
	})
	client := dialTestServer(t, addr)

	within(t, func() {
		ok, payload, err := client.SendRequest("fail@example.com", true, nil)
		if err != nil || ok || len(payload) != 0 {
			t.Errorf("reply = %v, %q, %v", ok, payload, err)
		}
	})
}

func TestParsePtyRequestLengthOverflow(t *testing.T) {
	if _, ok := pssh.ParsePtyRequest([]byte{0xff, 0xff, 0xff, 0xfc}); ok {
		t.Fatal("parsed a term length longer than the payload")
	}
}

func TestNoRequestRunsAfterPanic(t *testing.T) {
	var runs atomic.Int32
	addr := startTestServerWithConfig(t, &pssh.SSHServerConfig{
		SessionRequestHandlers: map[string]pssh.SSHServerRequestHandler{
			"boom@example.com": func(*ssh.Request, *pssh.SSHServerConnSession) (bool, func()) {
				panic("boom")
			},
		},
		Middleware: []pssh.SSHServerMiddleware{
			func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
				return func(sesh *pssh.SSHServerConnSession) error {
					runs.Add(1)
					return next(sesh)
				}
			},
		},
	})
	client := dialTestServer(t, addr)

	for range 50 {
		session, err := client.NewSession()
		if err != nil {
			t.Fatal(err)
		}
		// Pipelined like OpenSSH does, so exec is queued before the panic.
		if _, err := session.SendRequest("boom@example.com", false, nil); err != nil {
			t.Fatal(err)
		}
		within(t, func() {
			if ok, _ := session.SendRequest("exec", true, ssh.Marshal(struct{ Value string }{"cmd"})); ok {
				t.Error("exec accepted after a panic")
			}
		})
		_ = session.Close()
	}

	if n := runs.Load(); n != 0 {
		t.Fatalf("middleware ran %d times after a panic", n)
	}
}
