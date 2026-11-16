package pipe

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/picosh/utils/pipe"
)

// newWebTest runs pipe-web against a test pipe server.
func newWebTest(t *testing.T) (*TestSSHServer, *httptest.Server) {
	t.Helper()
	server := NewTestSSHServer(t)
	t.Cleanup(server.Shutdown)

	// pipe-web's key gets the admin flag, which its plain login must not use.
	user := GenerateUser("pico")
	RegisterUserWithServer(server, user)
	grantAdmin(server, "pico-id")
	keyPath := filepath.Join(t.TempDir(), "key")
	key := pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: user.privateKey})
	if err := os.WriteFile(keyPath, key, 0o600); err != nil {
		t.Fatal(err)
	}

	conn := newPipeConn(&pipe.SSHClientInfo{
		RemoteHost:     "localhost:" + server.Cfg.Port,
		RemoteHostname: "localhost",
		RemoteUser:     user.username,
		KeyLocation:    keyPath,
	})
	web := &webRouter{cfg: server.Cfg, db: server.DBPool, conn: conn}
	ts := httptest.NewServer(web.mainMux())
	t.Cleanup(ts.Close)
	return server, ts
}

func TestWebSubEndsWithRequest(t *testing.T) {
	server, ts := newWebTest(t)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/topic/quiet", nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		res, err := http.DefaultClient.Do(req)
		if err == nil {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
		}
	}()
	server.waitForClients(t, 1)

	cancel()
	server.waitForClients(t, 0)
}

func TestWebPubReachesSub(t *testing.T) {
	server, ts := newWebTest(t)

	received := make(chan string, 1)
	go func() {
		res, err := http.Get(ts.URL + "/topic/news")
		if err != nil {
			received <- err.Error()
			return
		}
		defer func() { _ = res.Body.Close() }()
		body, _ := io.ReadAll(res.Body)
		received <- string(body)
	}()
	server.waitForClients(t, 1)

	res, err := http.Post(ts.URL+"/topic/news", "text/plain", strings.NewReader("hello"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Errorf("pub status = %d", res.StatusCode)
	}
	if got := <-received; got != "hello" {
		t.Errorf("sub got %q, want %q", got, "hello")
	}
	server.waitForClients(t, 0)
}

func TestWebPipeEndsWhenClientLeaves(t *testing.T) {
	server, ts := newWebTest(t)

	c, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/pipe/chat", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	server.waitForClients(t, 1)

	// Drop the connection without a close frame, as a client that crashed.
	_ = c.NetConn().Close()
	server.waitForClients(t, 0)
}

// A sub served as HTML can't run scripts on the pipe origin.
func TestWebSubSandboxesContent(t *testing.T) {
	server, ts := newWebTest(t)

	headers := make(chan http.Header, 1)
	go func() {
		res, err := http.Get(ts.URL + "/topic/page?mime=text/html")
		if err != nil {
			headers <- nil
			return
		}
		defer func() { _ = res.Body.Close() }()
		headers <- res.Header
		_, _ = io.Copy(io.Discard, res.Body)
	}()
	server.waitForClients(t, 1)

	res, err := http.Post(ts.URL+"/topic/page", "text/plain", strings.NewReader("<script>alert(1)</script>"))
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()

	h := <-headers
	if h == nil {
		t.Fatal("sub request failed")
	}
	if got := h.Get("Content-Type"); got != "text/html" {
		t.Errorf("content-type = %q, want text/html", got)
	}
	if got := h.Get("Content-Security-Policy"); got != "sandbox" {
		t.Errorf("content-security-policy = %q, want sandbox", got)
	}
	if got := h.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("x-content-type-options = %q, want nosniff", got)
	}
}

func TestWebTopicSkipsAdminPath(t *testing.T) {
	server, ts := newWebTest(t)
	alice := GenerateUser("alice")
	RegisterUserWithServer(server, alice)
	client, err := alice.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()

	for _, topic := range []string{"%2Falice%2Fsecret", "%252Falice%252Fsecret"} {
		body := make(chan string, 1)
		go func() {
			c := http.Client{Timeout: time.Second}
			res, err := c.Get(ts.URL + "/topic/" + topic)
			if err != nil {
				body <- ""
				return
			}
			defer func() { _ = res.Body.Close() }()
			b, _ := io.ReadAll(res.Body)
			body <- string(b)
		}()
		server.waitForClients(t, 1)
		if out, err := alice.RunCommandWithStdin(client, "pub secret -b=false -c", "private\n"); err != nil {
			t.Fatalf("pub: %q %v", out, err)
		}
		if got := <-body; strings.Contains(got, "private") {
			t.Fatalf("%s read alice's private topic: %q", topic, got)
		}
		server.waitForClients(t, 0)
	}
}

// A message just under the ?prefix= limit, followed by the next one, arrives
// whole.
func TestWebPipePrefixLimitAppliesPerMessage(t *testing.T) {
	server, ts := newWebTest(t)
	alice := GenerateUser("alice")
	RegisterUserWithServer(server, alice)

	c, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/pipe/big?prefix=MSG", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	defer func() { _ = c.Close() }()
	server.waitForClients(t, 1)

	// The next prefix ends past the limit, so the buffer holds more than
	// maxPrefixMessage before the first message can be split off.
	first := "MSG" + strings.Repeat("a", maxPrefixMessage-5)
	second := "MSG" + strings.Repeat("b", 30000)
	client, err := alice.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if out, err := alice.RunCommandWithStdin(client, "pub big -p -b=false -c", first+second+"MSG"); err != nil {
		t.Fatalf("pub: %q %v", out, err)
	}

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	for _, want := range []string{first, second} {
		_, msg, err := c.ReadMessage()
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		if string(msg) != want {
			t.Fatalf("got a %d byte message, want %d bytes", len(msg), len(want))
		}
	}
}

// A complete message over the limit isn't delivered, even when its closing
// prefix arrives in the same read.
func TestWebPipePrefixLimitRefusesLargeMessage(t *testing.T) {
	server, ts := newWebTest(t)
	alice := GenerateUser("alice")
	RegisterUserWithServer(server, alice)

	c, res, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(ts.URL, "http")+"/pipe/big?prefix=MSG", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = res.Body.Close()
	defer func() { _ = c.Close() }()
	server.waitForClients(t, 1)

	large := "MSG" + strings.Repeat("a", maxPrefixMessage+100)
	client, err := alice.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	if out, err := alice.RunCommandWithStdin(client, "pub big -p -b=false -c", large+"MSG"+"small"+"MSG"); err != nil {
		t.Fatalf("pub: %q %v", out, err)
	}

	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	if _, msg, err := c.ReadMessage(); err == nil && len(msg) > maxPrefixMessage {
		t.Fatalf("got a %d byte message over the %d byte limit", len(msg), maxPrefixMessage)
	}
}
