package pgs

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

func TestCliAclHttpBasic(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	sesh := &fakeCmdSession{}
	cmd := &Cmd{User: user, Session: sesh, Log: logger, Store: st, Dbpool: dbpool, Write: true, Cfg: cfg}

	for _, bad := range [][]string{
		nil,
		{"alice"},
		{":pw"},
		{"alice:"},
		{"alice:pw1", "alice:pw2"},
		{"alice:" + strings.Repeat("x", 73)},
	} {
		if err := cmd.acl("test", "http-basic", bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}

	if err := cmd.acl("test", "http-basic", []string{"alice:pw1", "bob:p:w2"}); err != nil {
		t.Fatal(err)
	}
	project, err := dbpool.FindProjectByName(user.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	for i, want := range [][2]string{{"alice", "pw1"}, {"bob", "p:w2"}} {
		u, hash, _ := strings.Cut(project.Acl.Data[i], ":")
		if u != want[0] || !validatePassword(hash, want[1]) {
			t.Errorf("entry %d = %q, want %s with a hash of %s", i, project.Acl.Data[i], want[0], want[1])
		}
	}
	if got := strings.Join(project.Acl.DisplayData(), ","); got != "alice,bob" {
		t.Errorf("DisplayData = %q", got)
	}
	if out := sesh.String(); strings.Contains(out, "pw1") || strings.Contains(out, "$2") {
		t.Errorf("credentials printed: %s", out)
	}
}

func TestHttpBasicServesSite(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	project, err := dbpool.FindProjectByName(user.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	creds, err := hashBasicCredentials([]string{"alice:pw1", "bob:pw2"})
	if err != nil {
		t.Fatal(err)
	}
	project.Acl = db.ProjectAcl{Type: "http-basic", Data: creds}
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(user.ID): {"/test/index.html": "team content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	cfg.WebProtocol = "https"
	router := NewWebRouter(cfg)
	url := "https://" + user.Name + "-test.pgs.test/"

	get := func(set func(*http.Request)) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodGet, url, nil)
		if set != nil {
			set(r)
		}
		res := httptest.NewRecorder()
		router.ServeHTTP(res, r)
		if cc := res.Header().Get("cache-control"); cc != "private, no-store" {
			t.Errorf("cache-control = %q", cc)
		}
		return res
	}

	for name, set := range map[string]func(*http.Request){
		"no credentials": nil,
		"wrong password": func(r *http.Request) { r.SetBasicAuth("alice", "pw2") },
		"unknown user":   func(r *http.Request) { r.SetBasicAuth("carol", "pw1") },
		"planted cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: getCookieName("test"), Value: "planted"}) },
	} {
		res := get(set)
		if res.Code != http.StatusUnauthorized || strings.Contains(res.Body.String(), "team content") {
			t.Errorf("%s: %d %q", name, res.Code, res.Body.String())
		}
		if got := res.Header().Get("WWW-Authenticate"); got != `Basic realm="test", charset="UTF-8"` {
			t.Errorf("%s: WWW-Authenticate = %q", name, got)
		}
	}

	res := get(func(r *http.Request) { r.SetBasicAuth("bob", "pw2") })
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "team content") {
		t.Fatalf("valid credentials: %d %q", res.Code, res.Body.String())
	}
	var session *http.Cookie
	for _, c := range res.Result().Cookies() {
		if c.Name == getCookieName("test") {
			session = c
		}
	}
	if session == nil || !session.Secure || !session.HttpOnly {
		t.Fatalf("session cookie = %+v", session)
	}

	if res := get(func(r *http.Request) { r.AddCookie(session) }); res.Code != http.StatusOK {
		t.Fatalf("session cookie alone: %d", res.Code)
	}

	// Changing any credential ends existing sessions, even one after the
	// first entry.
	bob, err := hashBasicCredentials([]string{"bob:new"})
	if err != nil {
		t.Fatal(err)
	}
	project.Acl.Data = []string{creds[0], bob[0]}
	if res := get(func(r *http.Request) { r.AddCookie(session) }); res.Code != http.StatusUnauthorized {
		t.Fatalf("session after a credential change: %d", res.Code)
	}

	// The owner's tunnel serves the site without credentials.
	tunnel := &TunnelWebRouter{newWebRouter(cfg), user.Name + "-test"}
	tunnel.InitRouter()
	tres := httptest.NewRecorder()
	tunnel.ServeHTTP(tres, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if tres.Code != http.StatusOK || !strings.Contains(tres.Body.String(), "team content") {
		t.Fatalf("tunnel = %d %q", tres.Code, tres.Body.String())
	}
}

// Another user's tunnel can't reach an http-basic site.
func TestHttpBasicOwnerOnlyAccess(t *testing.T) {
	owner := &db.User{ID: "owner"}
	other := &db.User{ID: "other"}
	project := &db.Project{Acl: db.ProjectAcl{Type: "http-basic", Data: []string{"alice:x"}}}
	if !HasProjectAccess(project, owner, owner, nil) || HasProjectAccess(project, owner, other, nil) {
		t.Fatal("http-basic access is for the owner only")
	}
}
