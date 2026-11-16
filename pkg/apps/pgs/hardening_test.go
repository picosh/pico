package pgs

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

func newHardeningCmd(t *testing.T) (*Cmd, *PgsDb, storage.StorageServe) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	st, err := storage.NewStorageFS(logger, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := st.UpsertBucket(shared.GetAssetBucketName(user.ID))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"test/index.html", "other/index.html"} {
		if _, _, err := st.PutObject(bucket, f, strings.NewReader("hi"), &storage.ObjectInfo{}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := dbpool.InsertProject(user.ID, "other", "other"); err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cmd := &Cmd{User: user, Session: &fakeCmdSession{}, Log: logger, Store: st, Dbpool: dbpool, Write: true, Cfg: cfg}
	return cmd, dbpool, st
}

// Project commands can't reach past a single project in the bucket.
func TestCliRejectsNonProjectNames(t *testing.T) {
	cmd, dbpool, st := newHardeningCmd(t)
	for _, name := range []string{"..", ".", "/", ""} {
		if err := cmd.rm(name); err == nil {
			t.Errorf("rm %q: want an error", name)
		}
	}
	for _, prefix := range []string{"%", "_", "", "*"} {
		if err := cmd.prune(prefix, 0); err == nil {
			t.Errorf("prune %q: want an error", prefix)
		}
	}
	bucket, _ := st.GetBucket(shared.GetAssetBucketName(dbpool.Users[0].ID))
	for _, f := range []string{"test/index.html", "other/index.html"} {
		if _, err := st.StatObject(bucket, f); err != nil {
			t.Errorf("%s was removed: %v", f, err)
		}
	}
}

func TestCliLinkRules(t *testing.T) {
	cmd, dbpool, st := newHardeningCmd(t)
	user := dbpool.Users[0]

	if err := cmd.link("test", "test"); err == nil {
		t.Error("linking a project to itself was accepted")
	}
	bucket, _ := st.GetBucket(shared.GetAssetBucketName(user.ID))
	if _, err := st.StatObject(bucket, "test/index.html"); err != nil {
		t.Fatalf("self link removed the project's files: %v", err)
	}

	other, err := dbpool.FindProjectByName(user.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	other.Blocked = "abuse"
	if err := cmd.link("mirror", "other"); err == nil {
		t.Error("linking to a blocked project was accepted")
	}
}

// A project that links to a blocked project is served as blocked.
func TestLinkToBlockedProjectIsBlocked(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	if _, err := dbpool.InsertProject(user.ID, "mirror", "test"); err != nil {
		t.Fatal(err)
	}
	target, err := dbpool.FindProjectByName(user.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	target.Blocked = "abuse"
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(user.ID): {"/test/index.html": "blocked content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://"+user.Name+"-mirror.pgs.test/", nil))
	if res.Code != http.StatusForbidden || strings.Contains(res.Body.String(), "blocked content") {
		t.Fatalf("linked project served: %d %q", res.Code, res.Body.String())
	}
}

type rssPgsDb struct {
	*PgsDb
	projects []*db.Project
}

func (d rssPgsDb) FindProjects(string) ([]*db.Project, error) { return d.projects, nil }

// The public discovery feed lists only sites anyone can visit.
func TestRssListsOnlyPublicProjects(t *testing.T) {
	t.Chdir("../../..")
	logger := slog.New(slog.DiscardHandler)
	mk := func(name, acl, blocked string) *db.Project {
		created := time.Now()
		return &db.Project{Name: name, Username: "u", Acl: db.ProjectAcl{Type: acl}, Blocked: blocked, CreatedAt: &created, UpdatedAt: &created}
	}
	dbpool := rssPgsDb{NewPgsDb(logger), []*db.Project{
		mk("open", "public", ""),
		mk("legacy", "", ""),
		mk("private-notes", "private", ""),
		mk("secret", "http-pass", ""),
		mk("team", "pico", ""),
		mk("keys", "pubkeys", ""),
		mk("abuse", "public", "phishing"),
	}}
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)

	res := httptest.NewRecorder()
	router.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://pgs.test/rss", nil))
	body := res.Body.String()
	for _, name := range []string{"u-open", "u-legacy"} {
		if !strings.Contains(body, name) {
			t.Errorf("feed is missing %s", name)
		}
	}
	for _, name := range []string{"private-notes", "secret", "team", "keys", "abuse"} {
		if strings.Contains(body, "u-"+name) {
			t.Errorf("feed lists %s", name)
		}
	}
}

// A bad cookie set by another site under the domain doesn't hide a valid one.
func TestHttpPassAcceptsAnyValidCookie(t *testing.T) {
	hash, err := hashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	project := &db.Project{ID: "p", Name: "secret", Acl: db.ProjectAcl{Type: "http-pass", Data: []string{hash}}}
	r := httptest.NewRequest(http.MethodGet, "https://u-secret.pgs.test/", nil)
	r.AddCookie(&http.Cookie{Name: getCookieName("secret"), Value: "planted"})
	if hasValidSession(r, project) {
		t.Fatal("planted cookie accepted")
	}
	r.AddCookie(&http.Cookie{Name: getCookieName("secret"), Value: sessionToken(project, time.Now().Add(time.Hour))})
	if !hasValidSession(r, project) {
		t.Fatal("valid cookie after a planted one rejected")
	}
}

// The owner's tunnel to a password-protected site serves the site, since the
// tunnel checked access and can't post the login form.
func TestTunnelServesHttpPassSite(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	project, err := dbpool.FindProjectByName(user.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	hash, err := hashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	project.Acl = db.ProjectAcl{Type: "http-pass", Data: []string{hash}}
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(user.ID): {"/test/index.html": "owner content"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"

	tunnel := &TunnelWebRouter{newWebRouter(cfg), user.Name + "-test"}
	tunnel.InitRouter()
	res := httptest.NewRecorder()
	tunnel.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "http://localhost/", nil))
	if res.Code != http.StatusOK || !strings.Contains(res.Body.String(), "owner content") {
		t.Fatalf("tunnel = %d %q", res.Code, res.Body.String())
	}
	if cc := res.Header().Get("cache-control"); cc != "private, no-store" {
		t.Errorf("cache-control = %q", cc)
	}

	web := NewWebRouter(cfg)
	res = httptest.NewRecorder()
	web.ServeHTTP(res, httptest.NewRequest(http.MethodGet, "https://"+user.Name+"-test.pgs.test/", nil))
	if res.Code != http.StatusForbidden || strings.Contains(res.Body.String(), "owner content") {
		t.Fatalf("web without a session = %d", res.Code)
	}
}
