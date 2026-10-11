package pastes

import (
	"context"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/filehandlers"
	"github.com/picosh/pico/pkg/pssh"
)

func TestFileMetaKeepsSettings(t *testing.T) {
	soon := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)
	day := 24 * time.Hour
	tests := []struct {
		name   string
		cmd    []string
		cur    *db.Post
		hidden bool
		// expires is how long until the paste expires, or 0 for never.
		expires time.Duration
	}{
		{name: "new", expires: 90 * day},
		{name: "new with arguments", cmd: []string{"hidden=true", "expires=2h"}, hidden: true, expires: 2 * time.Hour},
		{name: "existing", cur: &db.Post{Hidden: true, ExpiresAt: &soon}, hidden: true, expires: time.Hour},
		{name: "existing without expiry", cur: &db.Post{Hidden: true}, hidden: true},
		{name: "existing past its expiry", cur: &db.Post{Hidden: true, ExpiresAt: &past}, hidden: true, expires: 90 * day},
		{name: "existing with arguments", cmd: []string{"hidden=false", "expires=true"}, cur: &db.Post{Hidden: true}, expires: 90 * day},
		{name: "existing made permanent", cmd: []string{"expires=false"}, cur: &db.Post{ExpiresAt: &soon}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &pssh.SSHServerConnSession{Ctx: context.Background()}
			s.SetValue("command", tt.cmd)
			data := &filehandlers.PostMetaData{Post: &db.Post{Filename: "a.txt", Slug: "a"}, Cur: tt.cur}
			if err := (&FileHooks{}).FileMeta(s, data); err != nil {
				t.Fatal(err)
			}
			if data.Hidden != tt.hidden {
				t.Errorf("hidden = %v, want %v", data.Hidden, tt.hidden)
			}
			switch {
			case tt.expires == 0 && data.ExpiresAt != nil:
				t.Errorf("expires at %v, want never", data.ExpiresAt)
			case tt.expires != 0 && (data.ExpiresAt == nil || time.Until(*data.ExpiresAt).Round(time.Minute) != tt.expires):
				t.Errorf("expires at %v, want in %v", data.ExpiresAt, tt.expires)
			}
		})
	}
}
