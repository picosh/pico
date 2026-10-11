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

	"github.com/gorilla/websocket"
	"github.com/picosh/utils/pipe"
)

// newWebTest runs pipe-web against a test pipe server.
func newWebTest(t *testing.T) (*TestSSHServer, *httptest.Server) {
	t.Helper()
	server := NewTestSSHServer(t)
	t.Cleanup(server.Shutdown)

	user := GenerateUser("pico")
	RegisterUserWithServer(server, user)
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
