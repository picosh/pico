package pgs

import (
	"bytes"
	"io"
	"log/slog"
	"testing"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

type fakeCmdSession struct{ bytes.Buffer }

func (s *fakeCmdSession) Exit(int) error        { return nil }
func (s *fakeCmdSession) Close() error          { return nil }
func (s *fakeCmdSession) Stderr() io.ReadWriter { return &s.Buffer }

// Commands that change how a project is served purge its cached pages and
// records.
func TestCliPurges(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	user := dbpool.Users[0]
	if _, err := dbpool.InsertProject(user.ID, "other", "other"); err != nil {
		t.Fatal(err)
	}
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(user.ID): {"/test/index.html": "hello"},
	})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewPgsConfig(logger, dbpool, st, discardPubsub{})
	cmd := &Cmd{User: user, Session: &fakeCmdSession{}, Log: logger, Store: st, Dbpool: dbpool, Write: true, Cfg: cfg}

	tests := []struct {
		name string
		run  func() error
	}{
		{"acl", func() error { return cmd.acl("test", "private", nil) }},
		{"link", func() error { return cmd.link("test", "other") }},
		{"unlink", func() error { return cmd.unlink("test") }},
		{"rm", func() error { return cmd.rm("test") }},
	}
	want := getSurrogateKey(user.Name, "test")
	for _, tt := range tests {
		if err := tt.run(); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		select {
		case got := <-cfg.CacheClearingQueue:
			if got != want {
				t.Fatalf("%s purged %q, want %q", tt.name, got, want)
			}
		default:
			t.Fatalf("%s purged nothing", tt.name)
		}
		for len(cfg.CacheClearingQueue) > 0 {
			<-cfg.CacheClearingQueue
		}
	}
}
