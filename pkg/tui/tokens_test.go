package tui

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"go.rockorager.dev/vaxis"
	"go.rockorager.dev/vaxis/vxfw"
)

type tokenTestDB struct {
	db.DB
	err     error
	tokens  []*db.Token
	deleted []string
	created int
}

func (d *tokenTestDB) InsertToken(string, string) (string, error) {
	d.created++
	return "fixture-token", d.err
}

func (d *tokenTestDB) FindTokensByUser(string) ([]*db.Token, error) {
	return d.tokens, nil
}

func (d *tokenTestDB) RemoveToken(id string) error {
	d.deleted = append(d.deleted, id)
	d.tokens = slices.DeleteFunc(d.tokens, func(token *db.Token) bool { return token.ID == id })
	return nil
}

func commandConsumes(cmd vxfw.Command) bool {
	switch cmd := cmd.(type) {
	case vxfw.ConsumeEventCmd:
		return true
	case vxfw.BatchCmd:
		for _, child := range cmd {
			if commandConsumes(child) {
				return true
			}
		}
	}
	return false
}

func TestTokenCreationErrorCanRetry(t *testing.T) {
	pool := &tokenTestDB{err: errors.New("fixture database failure")}
	page := NewAddTokenPage(&SharedModel{Dbpool: pool, User: &db.User{ID: "fixture"}})
	page.input.input.Value = "fixture-name"
	cmd, err := page.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil || !errors.Is(page.err, pool.err) {
		t.Fatalf("creation error should stay on the page: handler=%v, page=%v", err, page.err)
	}
	if !commandConsumes(cmd) || page.input.GetValue() != "fixture-name" || page.focus != "input" || page.token != "" {
		t.Fatal("failed submission did not preserve the form for retry")
	}
	pool.err = nil
	cmd, err = page.CaptureEvent(vaxis.Key{Keycode: vaxis.KeyEnter})
	if err != nil || page.err != nil || page.token != "fixture-token" || !commandConsumes(cmd) || pool.created != 2 {
		t.Fatalf("retry failed: handler=%v, page=%v, token=%q, attempts=%d", err, page.err, page.token, pool.created)
	}
	_, _ = page.HandleEvent(PageOut{}, vxfw.TargetPhase)
	if page.token != "" {
		t.Fatal("token remained visible after leaving the page")
	}
}

func TestTokenDeleteKeepsOriginalTarget(t *testing.T) {
	now := time.Now()
	pool := &tokenTestDB{tokens: []*db.Token{
		{ID: "first", Name: "first", CreatedAt: &now},
		{ID: "second", Name: "second", CreatedAt: &now},
		{ID: "third", Name: "third", CreatedAt: &now},
	}}
	page := NewTokensPage(&SharedModel{Dbpool: pool, User: &db.User{ID: "fixture"}})
	_, _ = page.HandleEvent(PageIn{}, vxfw.TargetPhase)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'x'}, vxfw.BubblePhase)
	page.list.SetCursor(2)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'y'}, vxfw.BubblePhase)
	if !slices.Equal(pool.deleted, []string{"first"}) {
		t.Fatalf("deleted %v, want first", pool.deleted)
	}
	if page.list.Cursor() != 1 || page.getWidget(page.list.Cursor(), page.list.Cursor()) == nil {
		t.Fatal("cursor did not move to the remaining last item")
	}
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'x'}, vxfw.BubblePhase)
	_, _ = page.HandleEvent(PageOut{}, vxfw.TargetPhase)
	_, _ = page.HandleEvent(vaxis.Key{Keycode: 'y'}, vxfw.BubblePhase)
	if len(pool.deleted) != 1 {
		t.Fatal("deletion remained armed after leaving the page")
	}
}
