package pico

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

type cliTestDB struct {
	*certsDB
	features map[string]*db.FeatureFlag
}

func (d *cliTestDB) FindUserByName(name string) (*db.User, error) {
	for _, u := range d.users {
		if u.Name == name {
			user := *u
			return &user, nil
		}
	}
	return nil, errors.New("user not found")
}

func (d *cliTestDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	if ff, ok := d.features[userID+":"+name]; ok {
		return ff, nil
	}
	return nil, errors.New("feature not found")
}

func TestUserCommandImpersonation(t *testing.T) {
	now := time.Now()
	expires := now.Add(time.Hour)
	logger := slog.New(slog.DiscardHandler)
	adminKey := newSigner(t)
	aliceKey := newSigner(t)
	bobKey := newSigner(t)

	cdb := &certsDB{
		StubDB: stub.NewStubDB(logger),
		users: map[string]*db.User{
			"admin-id": {ID: "admin-id", Name: "root", CreatedAt: &now},
			"alice-id": {ID: "alice-id", Name: "alice", CreatedAt: &now},
			"bob-id":   {ID: "bob-id", Name: "bob", CreatedAt: &now},
		},
		keys: []*db.PublicKey{
			{ID: "k-admin", UserID: "admin-id", Name: "root-key", Key: shared.KeyForKeyText(adminKey.PublicKey()), CreatedAt: &now},
			{ID: "k-alice", UserID: "alice-id", Name: "alice-key", Key: shared.KeyForKeyText(aliceKey.PublicKey()), CreatedAt: &now},
			{ID: "k-bob", UserID: "bob-id", Name: "bob-key", Key: shared.KeyForKeyText(bobKey.PublicKey()), CreatedAt: &now},
		},
	}

	testDB := &cliTestDB{
		certsDB: cdb,
		features: map[string]*db.FeatureFlag{
			"admin-id:admin": {ID: "ff-admin", UserID: "admin-id", Name: "admin", ExpiresAt: &expires},
		},
	}

	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)

	auth := shared.NewSshAuthHandler(testDB, logger, "pico")
	server, err := pssh.NewSSHServerWithConfig(ctx, logger, "pico", "127.0.0.1", "0", "", filepath.Join(t.TempDir(), "host_key"),
		auth.OpenPubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			Middleware(&CliHandler{DBPool: testDB, Logger: logger}),
			pssh.LogMiddleware(NewUploadHandler(testDB, &Config{}), testDB),
		}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ListenAndServe() }()

	addr := ""
	for deadline := time.Now().Add(5 * time.Second); addr == "" && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		server.Mu.Lock()
		if server.Listener != nil {
			addr = server.Listener.Addr().String()
		}
		server.Mu.Unlock()
	}

	run := func(sshUser string, signer ssh.Signer, command string) string {
		t.Helper()
		client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{
			User:            sshUser,
			Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
			HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		})
		if err != nil {
			t.Fatalf("ssh dial %s: %v", sshUser, err)
		}
		defer func() { _ = client.Close() }()
		session, err := client.NewSession()
		if err != nil {
			t.Fatalf("new session: %v", err)
		}
		out, err := session.CombinedOutput(command)
		if missing := (*ssh.ExitMissingError)(nil); errors.As(err, &missing) {
			err = nil
		}
		if err != nil {
			t.Fatalf("command %q as %s failed: %v (%s)", command, sshUser, err, out)
		}
		return string(out)
	}

	tests := []struct {
		name     string
		sshUser  string
		signer   ssh.Signer
		wantName string
		wantID   string
	}{
		{
			name:     "admin impersonates alice",
			sshUser:  "admin__alice",
			signer:   adminKey,
			wantName: "alice",
			wantID:   "alice-id",
		},
		{
			name:     "non-admin cannot impersonate alice",
			sshUser:  "admin__alice",
			signer:   bobKey,
			wantName: "bob",
			wantID:   "bob-id",
		},
		{
			name:     "normal login",
			sshUser:  "alice",
			signer:   aliceKey,
			wantName: "alice",
			wantID:   "alice-id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := run(tt.sshUser, tt.signer, "user")
			lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n")
			if len(lines) < 2 {
				t.Fatalf("unexpected output: %q", out)
			}
			if lines[0] != tt.wantName || lines[1] != tt.wantID {
				t.Errorf("got user (%q, %q), want (%q, %q)", lines[0], lines[1], tt.wantName, tt.wantID)
			}
		})
	}
}
