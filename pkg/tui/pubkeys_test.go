package tui

import (
	"slices"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

type pubkeyTestDB struct {
	db.DB
	keys    []*db.PublicKey
	deleted []string
}

func (d *pubkeyTestDB) FindKeysByUser(*db.User) ([]*db.PublicKey, error) {
	return d.keys, nil
}

func (d *pubkeyTestDB) RemoveKeys(ids []string) error {
	d.deleted = append(d.deleted, ids...)
	d.keys = slices.DeleteFunc(d.keys, func(key *db.PublicKey) bool { return slices.Contains(ids, key.ID) })
	return nil
}

func TestPubkeyDeleteKeepsOriginalTarget(t *testing.T) {
	pool := &pubkeyTestDB{keys: []*db.PublicKey{{ID: "first"}, {ID: "second"}}}
	page := NewPubkeysPage(&SharedModel{Dbpool: pool, User: &db.User{ID: "fixture"}})
	_, _ = page.HandleEvent(PageIn{}, vxfw.TargetPhase)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'x'}, vxfw.BubblePhase)
	page.list.SetCursor(1)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'y'}, vxfw.BubblePhase)
	if !slices.Equal(pool.deleted, []string{"first"}) || page.list.Cursor() != 0 {
		t.Fatalf("deleted %v, cursor %d", pool.deleted, page.list.Cursor())
	}
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'x'}, vxfw.BubblePhase)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'y'}, vxfw.BubblePhase)
	if len(pool.deleted) != 1 || page.err == nil {
		t.Fatal("last public key was not protected")
	}
}
