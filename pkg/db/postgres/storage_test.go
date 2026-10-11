package postgres

import (
	"errors"
	"log/slog"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres/pgtest"
)

var testDB *PsqlDB

func TestMain(m *testing.M) {
	pgtest.Main(m)
}

func cleanupTestData(t *testing.T) {
	t.Helper()
	if testDB == nil {
		testDB = NewDB(pgtest.URL(t), slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError})))
	}
	pgtest.Clean(t, testDB.Db)
}

func mustInsertPost(t *testing.T, post *db.Post) *db.Post {
	t.Helper()
	return pgtest.InsertPost(t, testDB, post)
}

func TestRegisterUser_Success(t *testing.T) {
	cleanupTestData(t)

	user, err := testDB.RegisterUser("testuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI testkey", "test comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
		return
	}
	if user == nil {
		t.Fatal("expected user, got nil")
		return
	}
	if user.Name != "testuser" {
		t.Errorf("expected name 'testuser', got '%s'", user.Name)
	}
	if user.PublicKey == nil {
		t.Fatal("expected public key, got nil")
	}
}

func TestRegisterUser_DuplicateName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("duplicateuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI key1", "comment1", "")
	if err != nil {
		t.Fatalf("first RegisterUser failed: %v", err)
	}

	_, err = testDB.RegisterUser("duplicateuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI key2", "comment2", "")
	if err == nil {
		t.Error("expected error for duplicate name, got nil")
	}
}

func TestRegisterUser_DeniedName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("admin", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI adminkey", "comment", "")
	if err == nil {
		t.Error("expected error for denied name 'admin', got nil")
	}
}

func TestRegisterUser_InvalidName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("user@invalid", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invalidkey", "comment", "")
	if err == nil {
		t.Error("expected error for invalid name, got nil")
	}
}

func TestFindUser(t *testing.T) {
	cleanupTestData(t)

	created, err := testDB.RegisterUser("findme", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findmekey", "comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	found, err := testDB.FindUser(created.ID)
	if err != nil {
		t.Fatalf("FindUser failed: %v", err)
	}
	if found.Name != "findme" {
		t.Errorf("expected name 'findme', got '%s'", found.Name)
	}
}

func TestFindUserByName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("nameduser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI namedkey", "comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	found, err := testDB.FindUserByName("nameduser")
	if err != nil {
		t.Fatalf("FindUserByName failed: %v", err)
	}
	if found.Name != "nameduser" {
		t.Errorf("expected name 'nameduser', got '%s'", found.Name)
	}
}

func TestFindUserByPubkey(t *testing.T) {
	cleanupTestData(t)

	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pubkeyuser"
	_, err := testDB.RegisterUser("pubkeyuser", pubkey, "comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	found, err := testDB.FindUserByPubkey(pubkey)
	if err != nil {
		t.Fatalf("FindUserByPubkey failed: %v", err)
	}
	if found.Name != "pubkeyuser" {
		t.Errorf("expected name 'pubkeyuser', got '%s'", found.Name)
	}
}

func TestFindUserByKey(t *testing.T) {
	cleanupTestData(t)

	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI forkeyfind"
	_, err := testDB.RegisterUser("forkeyfind", pubkey, "comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	found, err := testDB.FindUserByKey("forkeyfind", pubkey)
	if err != nil {
		t.Fatalf("FindUserByKey failed: %v", err)
	}
	if found.Name != "forkeyfind" {
		t.Errorf("expected name 'forkeyfind', got '%s'", found.Name)
	}
}

func TestFindUsers(t *testing.T) {
	cleanupTestData(t)

	_, _ = testDB.RegisterUser("user1", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI user1key", "comment", "")
	_, _ = testDB.RegisterUser("user2", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI user2key", "comment", "")

	users, err := testDB.FindUsers()
	if err != nil {
		t.Fatalf("FindUsers failed: %v", err)
	}
	if len(users) != 2 {
		t.Errorf("expected 2 users, got %d", len(users))
	}
}

// ============ Public Key Management Tests ============

func TestInsertPublicKey_Success(t *testing.T) {
	cleanupTestData(t)

	user, err := testDB.RegisterUser("keyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI keyowner1", "comment", "")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}

	err = testDB.InsertPublicKey(user.ID, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI secondkey", "second key")
	if err != nil {
		t.Fatalf("InsertPublicKey failed: %v", err)
	}

	keys, err := testDB.FindKeysByUser(user)
	if err != nil {
		t.Fatalf("FindKeysByUser failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestInsertPublicKey_Duplicate(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("dupkeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI dupkey", "comment", "")

	err := testDB.InsertPublicKey(user.ID, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI dupkey", "same key")
	if err == nil {
		t.Error("expected error for duplicate key, got nil")
	}
}

func TestUpdatePublicKey(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("updatekeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI updatekeyowner", "original", "")

	updated, err := testDB.UpdatePublicKey(user.PublicKey.ID, "new-name")
	if err != nil {
		t.Fatalf("UpdatePublicKey failed: %v", err)
	}
	if updated.Name != "new-name" {
		t.Errorf("expected name 'new-name', got '%s'", updated.Name)
	}
}

func TestFindKeysByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("multikeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI multikeyowner1", "key1", "")
	_ = testDB.InsertPublicKey(user.ID, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI multikeyowner2", "key2")

	keys, err := testDB.FindKeysByUser(user)
	if err != nil {
		t.Fatalf("FindKeysByUser failed: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys, got %d", len(keys))
	}
}

func TestRemoveKeys(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removekeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removekeyowner", "key1", "")
	_ = testDB.InsertPublicKey(user.ID, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removekeyowner2", "key2")

	keys, _ := testDB.FindKeysByUser(user)
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys before removal, got %d", len(keys))
	}

	err := testDB.RemoveKeys([]string{keys[1].ID})
	if err != nil {
		t.Fatalf("RemoveKeys failed: %v", err)
	}

	keys, _ = testDB.FindKeysByUser(user)
	if len(keys) != 1 {
		t.Errorf("expected 1 key after removal, got %d", len(keys))
	}
}

// ============ Token Management Tests ============

func TestInsertToken(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tokenowner", "comment", "")

	token, err := testDB.InsertToken(user.ID, "my-token")
	if err != nil {
		t.Fatalf("InsertToken failed: %v", err)
	}
	if token == "" {
		t.Error("expected token string, got empty")
	}
}

func TestUpsertToken(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("upserttokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI upserttokenowner", "comment", "")

	token1, err := testDB.UpsertToken(user.ID, "upsert-token")
	if err != nil {
		t.Fatalf("first UpsertToken failed: %v", err)
	}

	token2, err := testDB.UpsertToken(user.ID, "upsert-token")
	if err != nil {
		t.Fatalf("second UpsertToken failed: %v", err)
	}

	if token1 != token2 {
		t.Errorf("expected same token, got different: %s vs %s", token1, token2)
	}
}

func TestFindTokensByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tokensowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tokensowner", "comment", "")
	_, _ = testDB.InsertToken(user.ID, "token1")
	_, _ = testDB.InsertToken(user.ID, "token2")

	tokens, err := testDB.FindTokensByUser(user.ID)
	if err != nil {
		t.Fatalf("FindTokensByUser failed: %v", err)
	}
	if len(tokens) != 2 {
		t.Errorf("expected 2 tokens, got %d", len(tokens))
	}
}

func TestFindUserByToken_Valid(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("validtokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI validtokenowner", "comment", "")
	token, _ := testDB.InsertToken(user.ID, "valid-token")

	found, err := testDB.FindUserByToken(token)
	if err != nil {
		t.Fatalf("FindUserByToken failed: %v", err)
	}
	if found.Name != "validtokenowner" {
		t.Errorf("expected name 'validtokenowner', got '%s'", found.Name)
	}
}

func TestFindUserByToken_Expired(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("expiredtokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI expiredtokenowner", "comment", "")
	token, _ := testDB.InsertToken(user.ID, "expired-token")

	_, err := testDB.Db.Exec("UPDATE tokens SET expires_at = NOW() - INTERVAL '1 day' WHERE token = $1", token)
	if err != nil {
		t.Fatalf("failed to expire token: %v", err)
	}

	_, err = testDB.FindUserByToken(token)
	if err == nil {
		t.Error("expected error for expired token, got nil")
	}
}

func TestRemoveToken(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removetokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removetokenowner", "comment", "")
	_, _ = testDB.InsertToken(user.ID, "remove-token")

	tokens, _ := testDB.FindTokensByUser(user.ID)
	if len(tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(tokens))
	}

	err := testDB.RemoveToken(tokens[0].ID)
	if err != nil {
		t.Fatalf("RemoveToken failed: %v", err)
	}

	tokens, _ = testDB.FindTokensByUser(user.ID)
	if len(tokens) != 0 {
		t.Errorf("expected 0 tokens after removal, got %d", len(tokens))
	}
}

// ============ Post CRUD Tests ============

func TestInsertPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("postowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postowner", "comment", "")

	now := time.Now()
	post := &db.Post{
		UserID:      user.ID,
		Filename:    "test.md",
		Slug:        "test-post",
		Title:       "Test Post",
		Text:        "Post content",
		Description: "A test post",
		PublishAt:   &now,
		UpdatedAt:   &now,
		Space:       "prose",
		MimeType:    "text/markdown",
	}

	created, err := testDB.InsertPost(post)
	if err != nil {
		t.Fatalf("InsertPost failed: %v", err)
	}
	if created.ID == "" {
		t.Error("expected post ID, got empty")
	}
	if created.Title != "Test Post" {
		t.Errorf("expected title 'Test Post', got '%s'", created.Title)
	}
}

func TestUpdatePost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("updatepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI updatepostowner", "comment", "")

	now := time.Now()
	post := mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "update.md",
		Slug:     "update-post",
		Title:    "Original Title",
		Text:     "Original content",
		Space:    "prose",
	})

	post.Title = "Updated Title"
	post.Text = "Updated content"
	post.UpdatedAt = &now

	updated, err := testDB.UpdatePost(post)
	if err != nil {
		t.Fatalf("UpdatePost failed: %v", err)
	}
	if updated.Title != "Updated Title" {
		t.Errorf("expected title 'Updated Title', got '%s'", updated.Title)
	}
}

func TestRenamePost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("renamepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI renamepostowner", "comment", "")
	mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "a.md", Slug: "a", Text: "a", Space: "prose", Hidden: true})
	mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "b.md", Slug: "b", Text: "b", Space: "prose"})
	third := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "c.md", Slug: "c", Text: "c", Space: "prose"})
	// Posts made before data was written on insert have the column default.
	if _, err := testDB.Db.Exec(`UPDATE posts SET data = '{}'::jsonb`); err != nil {
		t.Fatal(err)
	}
	find := func(filename string) *db.Post {
		t.Helper()
		p, err := testDB.FindPostWithFilename(filename, user.ID, "prose")
		if err != nil {
			t.Fatalf("%s: %v", filename, err)
		}
		return p
	}
	renamed := func(cur *db.Post, filename, slug string) *db.Post {
		next := *cur
		next.Filename, next.Slug, next.MimeType = filename, slug, "text/plain"
		return &next
	}

	a := find("a.md")
	got, err := testDB.RenamePost(a, renamed(a, "a.lxt", "a"), nil, []string{"x", "y"}, []string{"old-a"})
	if err != nil {
		t.Fatalf("RenamePost failed: %v", err)
	}
	if got.ID != a.ID || got.Filename != "a.lxt" || got.MimeType != "text/plain" || !got.Hidden {
		t.Errorf("unexpected post after rename: %+v", got)
	}
	a = find("a.lxt")
	if slices.Sort(a.Tags); !slices.Equal(a.Tags, []string{"x", "y"}) {
		t.Errorf("tags = %q", a.Tags)
	}
	if p, err := testDB.FindPostWithSlug("old-a", user.ID, "prose"); err != nil || p.ID != a.ID {
		t.Errorf("alias old-a = %+v, %v", p, err)
	}

	// A save after the post was read stops the rename and is kept.
	stale := *a
	saved := *a
	saved.Text = "saved"
	if _, err := testDB.UpdatePost(&saved); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.RenamePost(&stale, renamed(&stale, "d.md", "d"), nil, nil, nil); !errors.Is(err, db.ErrPostChanged) {
		t.Fatalf("rename of a changed post: %v", err)
	}
	if p := find("a.lxt"); p.Text != "saved" {
		t.Errorf("a.lxt text = %q, want the save", p.Text)
	}

	// So does a save to the post it replaces.
	a = find("a.lxt")
	b := find("b.md")
	bSaved := *b
	bSaved.Text = "b saved"
	if _, err := testDB.UpdatePost(&bSaved); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.RenamePost(a, renamed(a, "b.md", "b"), b, nil, nil); !errors.Is(err, db.ErrPostChanged) {
		t.Fatalf("rename over a changed post: %v", err)
	}
	if p := find("b.md"); p.Text != "b saved" {
		t.Errorf("b.md text = %q, want the save", p.Text)
	}
	if p := find("a.lxt"); p.ID != a.ID {
		t.Errorf("a.lxt moved by a failed rename")
	}

	// Taking another post's slug fails and keeps the post it would replace.
	b = find("b.md")
	if _, err := testDB.RenamePost(a, renamed(a, "c2.md", "c"), b, nil, nil); err == nil {
		t.Fatal("expected a slug conflict")
	}
	find("b.md")

	if _, err := testDB.RenamePost(a, renamed(a, "b.md", "b"), b, nil, nil); err != nil {
		t.Fatalf("RenamePost over b.md failed: %v", err)
	}
	if p := find("b.md"); p.ID != a.ID || p.Text != "saved" {
		t.Errorf("b.md = %+v", p)
	}
	if _, err := testDB.FindPost(b.ID); err == nil {
		t.Error("replaced post still exists")
	}
	if _, err := testDB.FindPost(third.ID); err != nil {
		t.Errorf("unrelated post removed: %v", err)
	}

	// Data written after the post was read, like a feed digest, is kept.
	moved := find("b.md")
	if _, err := testDB.Db.Exec(`UPDATE posts SET data = '{"attempts": 2}'::jsonb WHERE id = $1`, moved.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.RenamePost(moved, renamed(moved, "e.md", "e"), nil, nil, nil); err != nil {
		t.Fatalf("rename after a data change: %v", err)
	}
	if p := find("e.md"); p.Data.Attempts != 2 {
		t.Errorf("data = %+v, want the later write", p.Data)
	}

	// A post moved to another name after it was read isn't replaced.
	e := find("e.md")
	c := find("c.md")
	if _, err := testDB.Db.Exec(`UPDATE posts SET filename = 'f.md' WHERE id = $1`, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testDB.RenamePost(e, renamed(e, "c.md", "c"), c, nil, nil); !errors.Is(err, db.ErrPostChanged) {
		t.Fatalf("rename over a moved post: %v", err)
	}
	if p := find("f.md"); p.ID != c.ID {
		t.Errorf("f.md = %+v", p)
	}
}

func TestRemovePosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removepostowner", "comment", "")

	post := mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "remove.md",
		Slug:     "remove-post",
		Title:    "To Remove",
		Space:    "prose",
	})

	err := testDB.RemovePosts([]string{post.ID})
	if err != nil {
		t.Fatalf("RemovePosts failed: %v", err)
	}

	_, err = testDB.FindPost(post.ID)
	if err == nil {
		t.Error("expected error finding removed post, got nil")
	}
}

func TestFindPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findpostowner", "comment", "")

	created := mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "find.md",
		Slug:     "find-post",
		Title:    "Find Me",
		Space:    "prose",
	})

	found, err := testDB.FindPost(created.ID)
	if err != nil {
		t.Fatalf("FindPost failed: %v", err)
	}
	if found.Title != "Find Me" {
		t.Errorf("expected title 'Find Me', got '%s'", found.Title)
	}
}

func TestFindPostWithFilename(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("filenamepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI filenamepostowner", "comment", "")

	_ = mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "byfilename.md",
		Slug:     "byfilename-post",
		Title:    "By Filename",
		Space:    "prose",
	})

	found, err := testDB.FindPostWithFilename("byfilename.md", user.ID, "prose")
	if err != nil {
		t.Fatalf("FindPostWithFilename failed: %v", err)
	}
	if found.Title != "By Filename" {
		t.Errorf("expected title 'By Filename', got '%s'", found.Title)
	}
}

func TestFindPostWithSlug(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("slugpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI slugpostowner", "comment", "")

	_ = mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "byslug.md",
		Slug:     "byslug-post",
		Title:    "By Slug",
		Space:    "prose",
	})

	found, err := testDB.FindPostWithSlug("byslug-post", user.ID, "prose")
	if err != nil {
		t.Fatalf("FindPostWithSlug failed: %v", err)
	}
	if found.Title != "By Slug" {
		t.Errorf("expected title 'By Slug', got '%s'", found.Title)
	}
}

func TestFindPosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("postsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postsowner", "comment", "")

	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "post1.md", Slug: "post1", Title: "Post 1", Space: "prose"})
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "post2.md", Slug: "post2", Title: "Post 2", Space: "prose"})

	posts, err := testDB.FindPosts()
	if err != nil {
		t.Fatalf("FindPosts failed: %v", err)
	}
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}
}

func TestFindPostsByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("userpostsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI userpostsowner", "comment", "")
	now := time.Now()

	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "userpost1.md", Slug: "userpost1", Title: "User Post 1", Space: "prose", PublishAt: &now})
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "userpost2.md", Slug: "userpost2", Title: "User Post 2", Space: "prose", PublishAt: &now})

	pager := &db.Pager{Num: 10, Page: 0}
	result, err := testDB.FindPostsByUser(pager, user.ID, "prose")
	if err != nil {
		t.Fatalf("FindPostsByUser failed: %v", err)
	}
	if len(result.Data) != 2 {
		t.Errorf("expected 2 posts, got %d", len(result.Data))
	}
}

func TestFindAllPostsByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("allpostsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI allpostsowner", "comment", "")

	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "allpost1.md", Slug: "allpost1", Title: "All Post 1", Space: "prose"})
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "allpost2.md", Slug: "allpost2", Title: "All Post 2", Space: "prose"})

	posts, err := testDB.FindAllPostsByUser(user.ID, "prose")
	if err != nil {
		t.Fatalf("FindAllPostsByUser failed: %v", err)
	}
	if len(posts) != 2 {
		t.Errorf("expected 2 posts, got %d", len(posts))
	}
}

func TestReplaceTagsByPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tagspostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tagspostowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "tagged.md", Slug: "tagged", Title: "Tagged", Space: "prose"})

	err := testDB.ReplaceTagsByPost([]string{"tag1", "tag2"}, post.ID)
	if err != nil {
		t.Fatalf("ReplaceTagsByPost failed: %v", err)
	}

	found, _ := testDB.FindPostWithFilename("tagged.md", user.ID, "prose")
	if len(found.Tags) != 2 {
		t.Errorf("expected 2 tags, got %d", len(found.Tags))
	}
}

func TestReplaceAliasesByPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("aliasowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aliasowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "aliased.md", Slug: "aliased", Title: "Aliased", Space: "prose"})

	err := testDB.ReplaceAliasesByPost([]string{"alias1", "alias2"}, post.ID)
	if err != nil {
		t.Fatalf("ReplaceAliasesByPost failed: %v", err)
	}
}

func TestFindPostWithSlug_Alias(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("aliassearchowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aliassearchowner", "comment", "")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "original.md", Slug: "original", Title: "Original", Space: "prose"})
	_ = testDB.ReplaceAliasesByPost([]string{"my-alias"}, post.ID)

	found, err := testDB.FindPostWithSlug("my-alias", user.ID, "prose")
	if err != nil {
		t.Fatalf("FindPostWithSlug for alias failed: %v", err)
	}
	if found.Title != "Original" {
		t.Errorf("expected title 'Original', got '%s'", found.Title)
	}
}

// ============ Analytics Tests ============

func TestInsertVisit(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("visitowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI visitowner", "comment", "")

	visit := &db.AnalyticsVisits{
		UserID:    user.ID,
		Host:      "example.com",
		Path:      "/test",
		IpAddress: "192.168.1.1",
		UserAgent: "TestAgent/1.0",
		Referer:   "https://referrer.com",
		Status:    200,
	}

	err := testDB.InsertVisit(visit)
	if err != nil {
		t.Fatalf("InsertVisit failed: %v", err)
	}
}

func TestVisitSummary(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("summaryowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI summaryowner", "comment", "")

	visit := &db.AnalyticsVisits{
		UserID:    user.ID,
		Host:      "summary.com",
		Path:      "/page",
		IpAddress: "192.168.1.2",
		Status:    200,
	}
	_ = testDB.InsertVisit(visit)

	opts := &db.SummaryOpts{
		Interval: "day",
		Origin:   time.Now().Add(-24 * time.Hour),
		Host:     "summary.com",
		UserID:   user.ID,
	}

	summary, err := testDB.VisitSummary(opts)
	if err != nil {
		t.Fatalf("VisitSummary failed: %v", err)
	}
	if summary == nil {
		t.Error("expected summary, got nil")
	}
}

func TestFindVisitSiteList(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("sitelistowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI sitelistowner", "comment", "")

	_ = testDB.InsertVisit(&db.AnalyticsVisits{
		UserID:    user.ID,
		Host:      "site1.com",
		Path:      "/",
		IpAddress: "192.168.1.3",
		Status:    200,
	})

	opts := &db.SummaryOpts{UserID: user.ID}
	sites, err := testDB.FindVisitSiteList(opts)
	if err != nil {
		t.Fatalf("FindVisitSiteList failed: %v", err)
	}
	if len(sites) < 1 {
		t.Errorf("expected at least 1 site, got %d", len(sites))
	}
}

func TestVisitUrlNotFound(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("notfoundowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI notfoundowner", "comment", "")

	_ = testDB.InsertVisit(&db.AnalyticsVisits{
		UserID:    user.ID,
		Host:      "notfound.com",
		Path:      "/missing",
		IpAddress: "192.168.1.4",
		Status:    404,
	})

	opts := &db.SummaryOpts{
		Origin: time.Now().Add(-24 * time.Hour),
		Host:   "notfound.com",
		UserID: user.ID,
	}
	notFound, err := testDB.VisitUrlNotFound(opts)
	if err != nil {
		t.Fatalf("VisitUrlNotFound failed: %v", err)
	}
	if len(notFound) < 1 {
		t.Errorf("expected at least 1 not found URL, got %d", len(notFound))
	}
}

// ============ Features Tests ============

func TestInsertFeature(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("featureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featureowner", "comment", "")

	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	feature, err := testDB.InsertFeature(user.ID, "plus", expiresAt)
	if err != nil {
		t.Fatalf("InsertFeature failed: %v", err)
	}
	if feature.Name != "plus" {
		t.Errorf("expected feature name 'plus', got '%s'", feature.Name)
	}
}

func TestFindFeature(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findfeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findfeatureowner", "comment", "")
	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	_, _ = testDB.InsertFeature(user.ID, "plus", expiresAt)

	feature, err := testDB.FindFeature(user.ID, "plus")
	if err != nil {
		t.Fatalf("FindFeature failed: %v", err)
	}
	if feature.Name != "plus" {
		t.Errorf("expected feature name 'plus', got '%s'", feature.Name)
	}
}

func TestFindFeaturesByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("featuresowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featuresowner", "comment", "")
	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	_, _ = testDB.InsertFeature(user.ID, "plus", expiresAt)
	_, _ = testDB.InsertFeature(user.ID, "pro", expiresAt)

	features, err := testDB.FindFeaturesByUser(user.ID)
	if err != nil {
		t.Fatalf("FindFeaturesByUser failed: %v", err)
	}
	if len(features) != 2 {
		t.Errorf("expected 2 features, got %d", len(features))
	}
}

func TestHasFeature(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("hasfeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI hasfeatureowner", "comment", "")
	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	_, _ = testDB.InsertFeature(user.ID, "plus", expiresAt)

	has := db.HasFeature(testDB, user.ID, "plus")
	if !has {
		t.Error("expected HasFeature to return true")
	}

	hasNot := db.HasFeature(testDB, user.ID, "nonexistent")
	if hasNot {
		t.Error("expected HasFeature to return false for nonexistent feature")
	}
}

func TestRemoveFeature(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removefeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removefeatureowner", "comment", "")
	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	_, _ = testDB.InsertFeature(user.ID, "plus", expiresAt)

	err := testDB.RemoveFeature(user.ID, "plus")
	if err != nil {
		t.Fatalf("RemoveFeature failed: %v", err)
	}

	_, err = testDB.FindFeature(user.ID, "plus")
	if err == nil {
		t.Error("expected error finding removed feature, got nil")
	}
}

func TestAddPicoPlusUser(t *testing.T) {
	cleanupTestData(t)

	_, _ = testDB.RegisterUser("picoplusowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI picoplusowner", "comment", "")

	err := testDB.AddPicoPlusUser("picoplusowner", "test@example.com", "stripe", "tx123")
	if err != nil {
		t.Fatalf("AddPicoPlusUser failed: %v", err)
	}
}

func TestAddFeatureUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("featureuserowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featureuserowner", "comment", "")

	err := testDB.AddFeatureUser("featureuserowner", "pgs")
	if err != nil {
		t.Fatalf("AddFeatureUser failed: %v", err)
	}

	ff, err := testDB.FindFeature(user.ID, "pgs")
	if err != nil {
		t.Fatalf("FindFeature failed: %v", err)
	}
	if ff.Name != "pgs" {
		t.Errorf("expected feature name pgs, got %s", ff.Name)
	}
	if !ff.IsValid() {
		t.Errorf("expected feature to be valid")
	}
}

func TestUpsertProject(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("projectowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI projectowner", "comment", "")

	project, err := testDB.UpsertProject(user.ID, "my-project", "my-project")
	if err != nil {
		t.Fatalf("UpsertProject failed: %v", err)
	}
	if project.Name != "my-project" {
		t.Errorf("expected project name 'my-project', got '%s'", project.Name)
	}

	project2, err := testDB.UpsertProject(user.ID, "my-project", "my-project")
	if err != nil {
		t.Fatalf("UpsertProject (update) failed: %v", err)
	}
	if project2.ID != project.ID {
		t.Error("expected same project ID on upsert")
	}
}

func TestFindProjectByName(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findprojectowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findprojectowner", "comment", "")
	_, _ = testDB.UpsertProject(user.ID, "findme-project", "findme-project")

	project, err := testDB.FindProjectByName(user.ID, "findme-project")
	if err != nil {
		t.Fatalf("FindProjectByName failed: %v", err)
	}
	if project.Name != "findme-project" {
		t.Errorf("expected project name 'findme-project', got '%s'", project.Name)
	}
}

// ============ User Stats Tests ============

func TestFindUserStats(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("statsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI statsowner", "comment", "")
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "stat.md", Slug: "stat", Title: "Stat", Space: "prose"})
	_, _ = testDB.UpsertProject(user.ID, "stat-project", "stat-project")

	stats, err := testDB.FindUserStats(user.ID)
	if err != nil {
		t.Fatalf("FindUserStats failed: %v", err)
	}
	if stats.Prose.Num != 1 {
		t.Errorf("expected 1 prose post, got %d", stats.Prose.Num)
	}
	if stats.Pages.Num != 1 {
		t.Errorf("expected 1 project, got %d", stats.Pages.Num)
	}
}

func TestFindUserStats_NoProjects(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("statsempty", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI statsempty", "comment", "")

	stats, err := testDB.FindUserStats(user.ID)
	if err != nil {
		t.Fatalf("FindUserStats failed: %v", err)
	}
	if stats.Pages.Num != 0 {
		t.Errorf("expected 0 projects, got %d", stats.Pages.Num)
	}
	if !stats.Pages.FirstCreatedAt.IsZero() {
		t.Errorf("expected zero first created at, got %s", stats.Pages.FirstCreatedAt)
	}
}

// ============ Tuns Event Logs Tests ============

func TestInsertTunsEventLog(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tunsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tunsowner", "comment", "")

	log := &db.TunsEventLog{
		UserId:         user.ID,
		ServerID:       "server-1",
		RemoteAddr:     "192.168.1.1:1234",
		EventType:      "connect",
		TunnelType:     "http",
		ConnectionType: "tcp",
		TunnelID:       "tunnel-123",
	}

	err := testDB.InsertTunsEventLog(log)
	if err != nil {
		t.Fatalf("InsertTunsEventLog failed: %v", err)
	}
}

func TestFindTunsEventLogs(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findtunsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findtunsowner", "comment", "")
	_ = testDB.InsertTunsEventLog(&db.TunsEventLog{
		UserId:         user.ID,
		ServerID:       "server-1",
		RemoteAddr:     "192.168.1.1:1234",
		EventType:      "connect",
		TunnelType:     "http",
		ConnectionType: "tcp",
		TunnelID:       "tunnel-456",
	})

	logs, err := testDB.FindTunsEventLogs(user.ID)
	if err != nil {
		t.Fatalf("FindTunsEventLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(logs))
	}
}

func TestFindTunsEventLogsByAddr(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tunsaddrowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tunsaddrowner", "comment", "")
	_ = testDB.InsertTunsEventLog(&db.TunsEventLog{
		UserId:         user.ID,
		ServerID:       "server-1",
		RemoteAddr:     "192.168.1.1:1234",
		EventType:      "connect",
		TunnelType:     "http",
		ConnectionType: "tcp",
		TunnelID:       "tunnel-789",
	})

	logs, err := testDB.FindTunsEventLogsByAddr(user.ID, "tunnel-789")
	if err != nil {
		t.Fatalf("FindTunsEventLogsByAddr failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(logs))
	}
}

// ============ Access Logs Tests ============

func TestInsertAccessLog(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("accesslogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accesslogowner", "comment", "")

	log := &db.AccessLog{
		UserID:   user.ID,
		Service:  "pgs",
		Pubkey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accesslogowner",
		Identity: "accesslogowner",
	}

	err := testDB.InsertAccessLog(log)
	if err != nil {
		t.Fatalf("InsertAccessLog failed: %v", err)
	}
}

func TestFindAccessLogs(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findaccesslogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findaccesslogowner", "comment", "")
	_ = testDB.InsertAccessLog(&db.AccessLog{
		UserID:   user.ID,
		Service:  "pgs",
		Pubkey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findaccesslogowner",
		Identity: "findaccesslogowner",
	})

	fromDate := time.Now().Add(-24 * time.Hour)
	logs, err := testDB.FindAccessLogs(user.ID, &fromDate)
	if err != nil {
		t.Fatalf("FindAccessLogs failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(logs))
	}
}

func TestFindPubkeysInAccessLogs(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("pubkeylogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pubkeylogowner", "comment", "")
	_ = testDB.InsertAccessLog(&db.AccessLog{
		UserID:   user.ID,
		Service:  "pgs",
		Pubkey:   "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pubkeylogowner",
		Identity: "pubkeylogowner",
	})

	pubkeys, err := testDB.FindPubkeysInAccessLogs(user.ID)
	if err != nil {
		t.Fatalf("FindPubkeysInAccessLogs failed: %v", err)
	}
	if len(pubkeys) != 1 {
		t.Errorf("expected 1 pubkey, got %d", len(pubkeys))
	}
}

func TestFindAccessLogsByPubkey(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("accessbypubkeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accessbypubkeyowner", "comment", "")
	pubkey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accessbypubkeyowner"
	_ = testDB.InsertAccessLog(&db.AccessLog{
		UserID:   user.ID,
		Service:  "pgs",
		Pubkey:   pubkey,
		Identity: "accessbypubkeyowner",
	})

	fromDate := time.Now().Add(-24 * time.Hour)
	logs, err := testDB.FindAccessLogsByPubkey(pubkey, &fromDate)
	if err != nil {
		t.Fatalf("FindAccessLogsByPubkey failed: %v", err)
	}
	if len(logs) != 1 {
		t.Errorf("expected 1 log, got %d", len(logs))
	}
}

// ============ JSONB Roundtrip Tests ============

func TestPostData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("postdataowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postdataowner", "comment", "")

	now := time.Now().Truncate(time.Second)
	postData := db.PostData{
		ImgPath:    "/images/test.png",
		LastDigest: &now,
		Attempts:   5,
	}

	post := mustInsertPost(t, &db.Post{
		UserID:   user.ID,
		Filename: "jsonb.md",
		Slug:     "jsonb",
		Title:    "JSONB Test",
		Space:    "prose",
		Data:     postData,
	})

	found, err := testDB.FindPost(post.ID)
	if err != nil {
		t.Fatalf("FindPost failed: %v", err)
	}
	if found.Data.ImgPath != "/images/test.png" {
		t.Errorf("expected ImgPath '/images/test.png', got '%s'", found.Data.ImgPath)
	}
	if found.Data.Attempts != 5 {
		t.Errorf("expected Attempts 5, got %d", found.Data.Attempts)
	}
}

func TestProjectAcl_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("aclowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aclowner", "comment", "")

	project, err := testDB.UpsertProject(user.ID, "acl-project", "acl-project")
	if err != nil {
		t.Fatalf("UpsertProject failed: %v", err)
	}

	found, err := testDB.FindProjectByName(user.ID, "acl-project")
	if err != nil {
		t.Fatalf("FindProjectByName failed: %v", err)
	}
	if found.Acl.Type != "public" {
		t.Errorf("expected Acl.Type 'public', got '%s'", found.Acl.Type)
	}
	_ = project
}

func TestFeatureFlagData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("featurejsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featurejsonbowner", "comment", "")

	err := testDB.AddPicoPlusUser("featurejsonbowner", "test@example.com", "stripe", "tx456")
	if err != nil {
		t.Fatalf("AddPicoPlusUser failed: %v", err)
	}

	feature, err := testDB.FindFeature(user.ID, "plus")
	if err != nil {
		t.Fatalf("FindFeature failed: %v", err)
	}
	if feature.Data.StorageMax != 5000000000 {
		t.Errorf("expected StorageMax 5000000000, got %d", feature.Data.StorageMax)
	}
	if feature.Data.FileMax != 100000000 {
		t.Errorf("expected FileMax 100000000, got %d", feature.Data.FileMax)
	}
}

func TestPaymentHistoryData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("paymentjsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI paymentjsonbowner", "comment", "")

	err := testDB.AddPicoPlusUser("paymentjsonbowner", "payment@example.com", "stripe", "tx789")
	if err != nil {
		t.Fatalf("AddPicoPlusUser failed: %v", err)
	}

	var txId string
	err = testDB.Db.QueryRow("SELECT data->>'tx_id' FROM payment_history WHERE user_id = $1", user.ID).Scan(&txId)
	if err != nil {
		t.Fatalf("failed to query payment history: %v", err)
	}
	if txId != "tx789" {
		t.Errorf("expected tx_id 'tx789', got '%s'", txId)
	}
}

func TestInviteUser_PlusMember(t *testing.T) {
	cleanupTestData(t)

	from, _ := testDB.RegisterUser("inviteplus", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI inviteplus", "comment", "")
	to, _ := testDB.RegisterUser("inviteplusto", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI inviteplusto", "comment", "")
	_, _ = testDB.InsertFeature(from.ID, "plus", time.Now().Add(365*24*time.Hour))

	if err := testDB.InviteUser(from.ID, to.ID); err != nil {
		t.Fatalf("InviteUser failed: %v", err)
	}

	for _, name := range []string{"prose", "pgs", "pastes"} {
		if !db.HasFeature(testDB, to.ID, name) {
			t.Errorf("expected invitee to have %s", name)
		}
	}

	inviter, err := testDB.FindWhoInvitedUser(to.ID)
	if err != nil {
		t.Fatalf("FindWhoInvitedUser failed: %v", err)
	}
	if inviter != "inviteplus" {
		t.Errorf("expected inviter inviteplus, got %s", inviter)
	}
}

func TestInviteUser_InvitedUserCanInvite(t *testing.T) {
	cleanupTestData(t)

	plus, _ := testDB.RegisterUser("invitechaina", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invitechaina", "comment", "")
	mid, _ := testDB.RegisterUser("invitechainb", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invitechainb", "comment", "")
	last, _ := testDB.RegisterUser("invitechainc", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invitechainc", "comment", "")
	_, _ = testDB.InsertFeature(plus.ID, "plus", time.Now().Add(365*24*time.Hour))

	if err := testDB.InviteUser(plus.ID, mid.ID); err != nil {
		t.Fatalf("InviteUser from plus failed: %v", err)
	}
	if err := testDB.InviteUser(mid.ID, last.ID); err != nil {
		t.Fatalf("InviteUser from invited user failed: %v", err)
	}
	if !db.HasFeature(testDB, last.ID, "pastes") {
		t.Error("expected second invitee to have pastes")
	}
}

func TestInviteUser_Unauthorized(t *testing.T) {
	cleanupTestData(t)

	from, _ := testDB.RegisterUser("invitenone", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invitenone", "comment", "")
	to, _ := testDB.RegisterUser("invitenoneto", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invitenoneto", "comment", "")

	if err := testDB.InviteUser(from.ID, to.ID); err == nil {
		t.Fatal("expected error for user without pico+ or invite")
	}
	if db.HasFeature(testDB, to.ID, "prose") {
		t.Error("expected invitee not to have prose")
	}
}

func TestInviteUser_ExpiredInvite(t *testing.T) {
	cleanupTestData(t)

	plus, _ := testDB.RegisterUser("inviteexpa", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI inviteexpa", "comment", "")
	expired, _ := testDB.RegisterUser("inviteexpb", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI inviteexpb", "comment", "")
	to, _ := testDB.RegisterUser("inviteexpc", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI inviteexpc", "comment", "")

	_, err := testDB.Db.Exec(`INSERT INTO invites (from_user_id, to_user_id) VALUES ($1, $2)`, plus.ID, expired.ID)
	if err != nil {
		t.Fatalf("insert invite failed: %v", err)
	}
	past := time.Now().Add(-24 * time.Hour)
	for _, name := range []string{"prose", "pgs", "pastes"} {
		_, _ = testDB.InsertFeature(expired.ID, name, past)
	}

	if err := testDB.InviteUser(expired.ID, to.ID); err == nil {
		t.Fatal("expected error for user whose invite access expired")
	}
}
