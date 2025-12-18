package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/pkg/db"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var testDB *PsqlDB
var testLogger *slog.Logger
var skipTests bool

func setupContainerRuntime() bool {
	// Check if DATABASE_URL is set for external postgres (CI/CD or manual testing)
	if os.Getenv("TEST_DATABASE_URL") != "" {
		return true
	}

	// Try podman first
	if cmd := exec.Command("podman", "info"); cmd.Run() == nil {
		// For podman, we need to ensure the socket is running
		// User should run: systemctl --user start podman.socket
		_ = os.Setenv("TESTCONTAINERS_RYUK_DISABLED", "true")

		// Check if socket exists and is accessible
		xdgRuntime := os.Getenv("XDG_RUNTIME_DIR")
		if xdgRuntime != "" {
			socketPath := xdgRuntime + "/podman/podman.sock"
			if _, err := os.Stat(socketPath); err == nil {
				_ = os.Setenv("DOCKER_HOST", "unix://"+socketPath)
				return true
			}
		}
		// Socket not available, need to start it
		fmt.Println("Podman detected but socket not running. Run: systemctl --user start podman.socket")
		return false
	}

	// Try docker
	if cmd := exec.Command("docker", "info"); cmd.Run() == nil {
		return true
	}

	return false
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	testLogger = slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Check for external database URL first (for CI/CD or manual testing)
	if dbURL := os.Getenv("TEST_DATABASE_URL"); dbURL != "" {
		testDB = NewDB(dbURL, testLogger)
		if err := setupTestSchema(testDB.Db); err != nil {
			panic(fmt.Sprintf("failed to setup schema: %s", err))
		}
		code := m.Run()
		_ = testDB.Close()
		os.Exit(code)
	}

	if !setupContainerRuntime() {
		fmt.Println("Container runtime not available, skipping integration tests")
		fmt.Println("To run tests, either:")
		fmt.Println("  - Set TEST_DATABASE_URL to a postgres connection string")
		fmt.Println("  - Start podman socket: systemctl --user start podman.socket")
		fmt.Println("  - Start docker daemon")
		skipTests = true
		os.Exit(0)
	}

	pgContainer, err := postgres.Run(ctx,
		"postgres:14",
		postgres.WithDatabase("pico_test"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(30*time.Second)),
	)
	if err != nil {
		fmt.Printf("Failed to start postgres container (Docker may not be running): %s\n", err)
		skipTests = true
		os.Exit(0)
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		panic(fmt.Sprintf("failed to get connection string: %s", err))
	}

	testDB = NewDB(connStr, testLogger)

	if err := setupTestSchema(testDB.Db); err != nil {
		panic(fmt.Sprintf("failed to setup schema: %s", err))
	}

	code := m.Run()

	_ = testDB.Close()
	_ = pgContainer.Terminate(ctx)

	os.Exit(code)
}

func getProjectRoot() string {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		panic("failed to get current file path")
	}
	// storage_test.go is in pkg/db/postgres/, so go up 4 levels to get project root
	return filepath.Join(filepath.Dir(filename), "..", "..", "..")
}

func setupTestSchema(db *sqlx.DB) error {
	projectRoot := getProjectRoot()
	migrationsDir := filepath.Join(projectRoot, "sql", "migrations")

	// Read all migration files
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}

	// Sort by filename (they're date-prefixed, so alphabetical order is correct)
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)

	// Execute each migration in order
	for _, filename := range migrationFiles {
		migrationPath := filepath.Join(migrationsDir, filename)
		content, err := os.ReadFile(migrationPath)
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", filename, err)
		}

		_, err = db.Exec(string(content))
		if err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", filename, err)
		}
	}

	return nil
}

func cleanupTestData(t *testing.T) {
	t.Helper()
	tables := []string{
		"access_logs", "tuns_event_logs", "analytics_visits",
		"feed_items", "post_aliases", "post_tags", "posts",
		"projects", "feature_flags", "payment_history", "tokens",
		"public_keys", "app_users",
	}
	for _, table := range tables {
		_, err := testDB.Db.Exec(fmt.Sprintf("DELETE FROM %s", table))
		if err != nil {
			t.Fatalf("failed to clean up %s: %v", table, err)
		}
	}
}

func mustInsertPost(t *testing.T, post *db.Post) *db.Post {
	t.Helper()
	now := time.Now()
	if post.UpdatedAt == nil {
		post.UpdatedAt = &now
	}
	if post.PublishAt == nil {
		post.PublishAt = &now
	}
	created, err := testDB.InsertPost(post)
	if err != nil {
		t.Fatalf("InsertPost failed: %v", err)
	}
	return created
}

// ============ User Management Tests ============

func TestRegisterUser_Success(t *testing.T) {
	cleanupTestData(t)

	user, err := testDB.RegisterUser("testuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI testkey", "test comment")
	if err != nil {
		t.Fatalf("RegisterUser failed: %v", err)
	}
	if user == nil {
		t.Fatal("expected user, got nil")
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

	_, err := testDB.RegisterUser("duplicateuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI key1", "comment1")
	if err != nil {
		t.Fatalf("first RegisterUser failed: %v", err)
	}

	_, err = testDB.RegisterUser("duplicateuser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI key2", "comment2")
	if err == nil {
		t.Error("expected error for duplicate name, got nil")
	}
}

func TestRegisterUser_DeniedName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("admin", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI adminkey", "comment")
	if err == nil {
		t.Error("expected error for denied name 'admin', got nil")
	}
}

func TestRegisterUser_InvalidName(t *testing.T) {
	cleanupTestData(t)

	_, err := testDB.RegisterUser("user@invalid", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI invalidkey", "comment")
	if err == nil {
		t.Error("expected error for invalid name, got nil")
	}
}

func TestFindUser(t *testing.T) {
	cleanupTestData(t)

	created, err := testDB.RegisterUser("findme", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findmekey", "comment")
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

	_, err := testDB.RegisterUser("nameduser", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI namedkey", "comment")
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
	_, err := testDB.RegisterUser("pubkeyuser", pubkey, "comment")
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
	_, err := testDB.RegisterUser("forkeyfind", pubkey, "comment")
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

	_, _ = testDB.RegisterUser("user1", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI user1key", "comment")
	_, _ = testDB.RegisterUser("user2", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI user2key", "comment")

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

	user, err := testDB.RegisterUser("keyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI keyowner1", "comment")
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

	user, _ := testDB.RegisterUser("dupkeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI dupkey", "comment")

	err := testDB.InsertPublicKey(user.ID, "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI dupkey", "same key")
	if err == nil {
		t.Error("expected error for duplicate key, got nil")
	}
}

func TestUpdatePublicKey(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("updatekeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI updatekeyowner", "original")

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

	user, _ := testDB.RegisterUser("multikeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI multikeyowner1", "key1")
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

	user, _ := testDB.RegisterUser("removekeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removekeyowner", "key1")
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

	user, _ := testDB.RegisterUser("tokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tokenowner", "comment")

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

	user, _ := testDB.RegisterUser("upserttokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI upserttokenowner", "comment")

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

	user, _ := testDB.RegisterUser("tokensowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tokensowner", "comment")
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

	user, _ := testDB.RegisterUser("validtokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI validtokenowner", "comment")
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

	user, _ := testDB.RegisterUser("expiredtokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI expiredtokenowner", "comment")
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

	user, _ := testDB.RegisterUser("removetokenowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removetokenowner", "comment")
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

	user, _ := testDB.RegisterUser("postowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postowner", "comment")

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

	user, _ := testDB.RegisterUser("updatepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI updatepostowner", "comment")

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

func TestRemovePosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removepostowner", "comment")

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

	user, _ := testDB.RegisterUser("findpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findpostowner", "comment")

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

	user, _ := testDB.RegisterUser("filenamepostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI filenamepostowner", "comment")

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

	user, _ := testDB.RegisterUser("slugpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI slugpostowner", "comment")

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

	user, _ := testDB.RegisterUser("postsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postsowner", "comment")

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

	user, _ := testDB.RegisterUser("userpostsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI userpostsowner", "comment")
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

	user, _ := testDB.RegisterUser("allpostsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI allpostsowner", "comment")

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

func TestFindUsersWithPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedspostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedspostowner", "comment")

	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feed.txt", Slug: "feed", Title: "Feed", Space: "feeds"})

	users, err := testDB.FindUsersWithPost("feeds")
	if err != nil {
		t.Fatalf("FindUsersWithPost failed: %v", err)
	}
	if len(users) != 1 {
		t.Errorf("expected 1 user, got %d", len(users))
	}
}

func TestFindExpiredPosts(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("expiredpostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI expiredpostowner", "comment")

	expired := time.Now().Add(-24 * time.Hour)
	_ = mustInsertPost(t, &db.Post{
		UserID:    user.ID,
		Filename:  "expired.txt",
		Slug:      "expired",
		Title:     "Expired",
		Space:     "pastes",
		ExpiresAt: &expired,
	})

	posts, err := testDB.FindExpiredPosts("pastes")
	if err != nil {
		t.Fatalf("FindExpiredPosts failed: %v", err)
	}
	if len(posts) != 1 {
		t.Errorf("expected 1 expired post, got %d", len(posts))
	}
}

func TestFindPostsByFeed(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedowner", "comment")

	now := time.Now()
	_ = mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedpost.md", Slug: "feedpost", Title: "Feed Post", Space: "prose", PublishAt: &now})

	pager := &db.Pager{Num: 10, Page: 0}
	result, err := testDB.FindPostsByFeed(pager, "prose")
	if err != nil {
		t.Fatalf("FindPostsByFeed failed: %v", err)
	}
	if len(result.Data) < 1 {
		t.Errorf("expected at least 1 post in feed, got %d", len(result.Data))
	}
}

// ============ Tags Tests ============

func TestReplaceTagsByPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tagspostowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tagspostowner", "comment")
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

func TestFindUserPostsByTag(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("usertagowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI usertagowner", "comment")
	now := time.Now()
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "usertag.md", Slug: "usertag", Title: "User Tag", Space: "prose", PublishAt: &now})
	_ = testDB.ReplaceTagsByPost([]string{"mytag"}, post.ID)

	pager := &db.Pager{Num: 10, Page: 0}
	result, err := testDB.FindUserPostsByTag(pager, "mytag", user.ID, "prose")
	if err != nil {
		t.Fatalf("FindUserPostsByTag failed: %v", err)
	}
	if len(result.Data) != 1 {
		t.Errorf("expected 1 post with tag, got %d", len(result.Data))
	}
}

func TestFindPostsByTag(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tagsearchowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tagsearchowner", "comment")
	now := time.Now()
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "tagsearch.md", Slug: "tagsearch", Title: "Tag Search", Space: "prose", PublishAt: &now})
	_ = testDB.ReplaceTagsByPost([]string{"searchtag"}, post.ID)

	pager := &db.Pager{Num: 10, Page: 0}
	result, err := testDB.FindPostsByTag(pager, "searchtag", "prose")
	if err != nil {
		t.Fatalf("FindPostsByTag failed: %v", err)
	}
	if len(result.Data) != 1 {
		t.Errorf("expected 1 post with tag, got %d", len(result.Data))
	}
}

func TestFindPopularTags(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("populartagowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI populartagowner", "comment")
	post1 := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "pop1.md", Slug: "pop1", Title: "Pop 1", Space: "prose"})
	post2 := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "pop2.md", Slug: "pop2", Title: "Pop 2", Space: "prose"})
	_ = testDB.ReplaceTagsByPost([]string{"popular"}, post1.ID)
	_ = testDB.ReplaceTagsByPost([]string{"popular"}, post2.ID)

	tags, err := testDB.FindPopularTags("prose")
	if err != nil {
		t.Fatalf("FindPopularTags failed: %v", err)
	}
	if len(tags) < 1 {
		t.Errorf("expected at least 1 popular tag, got %d", len(tags))
	}
}

// ============ Aliases Tests ============

func TestReplaceAliasesByPost(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("aliasowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aliasowner", "comment")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "aliased.md", Slug: "aliased", Title: "Aliased", Space: "prose"})

	err := testDB.ReplaceAliasesByPost([]string{"alias1", "alias2"}, post.ID)
	if err != nil {
		t.Fatalf("ReplaceAliasesByPost failed: %v", err)
	}
}

func TestFindPostWithSlug_Alias(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("aliassearchowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aliassearchowner", "comment")
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

	user, _ := testDB.RegisterUser("visitowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI visitowner", "comment")

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

	user, _ := testDB.RegisterUser("summaryowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI summaryowner", "comment")

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

	user, _ := testDB.RegisterUser("sitelistowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI sitelistowner", "comment")

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

	user, _ := testDB.RegisterUser("notfoundowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI notfoundowner", "comment")

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

	user, _ := testDB.RegisterUser("featureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featureowner", "comment")

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

	user, _ := testDB.RegisterUser("findfeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findfeatureowner", "comment")
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

	user, _ := testDB.RegisterUser("featuresowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featuresowner", "comment")
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

func TestHasFeatureByUser(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("hasfeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI hasfeatureowner", "comment")
	expiresAt := time.Now().Add(365 * 24 * time.Hour)
	_, _ = testDB.InsertFeature(user.ID, "plus", expiresAt)

	has := testDB.HasFeatureByUser(user.ID, "plus")
	if !has {
		t.Error("expected HasFeatureByUser to return true")
	}

	hasNot := testDB.HasFeatureByUser(user.ID, "nonexistent")
	if hasNot {
		t.Error("expected HasFeatureByUser to return false for nonexistent feature")
	}
}

func TestRemoveFeature(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("removefeatureowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI removefeatureowner", "comment")
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

	_, _ = testDB.RegisterUser("picoplusowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI picoplusowner", "comment")

	err := testDB.AddPicoPlusUser("picoplusowner", "test@example.com", "stripe", "tx123")
	if err != nil {
		t.Fatalf("AddPicoPlusUser failed: %v", err)
	}
}

// ============ Feed Items Tests ============

func TestInsertFeedItems(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feeditemsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feeditemsowner", "comment")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feed.txt", Slug: "feed", Title: "Feed", Space: "feeds"})

	items := []*db.FeedItem{
		{PostID: post.ID, GUID: "guid-1", Data: db.FeedItemData{Title: "Item 1", Link: "http://example.com/1"}},
		{PostID: post.ID, GUID: "guid-2", Data: db.FeedItemData{Title: "Item 2", Link: "http://example.com/2"}},
	}

	err := testDB.InsertFeedItems(post.ID, items)
	if err != nil {
		t.Fatalf("InsertFeedItems failed: %v", err)
	}
}

func TestFindFeedItemsByPostID(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("findfeeditemsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findfeeditemsowner", "comment")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "findfeed.txt", Slug: "findfeed", Title: "Find Feed", Space: "feeds"})

	items := []*db.FeedItem{
		{PostID: post.ID, GUID: "find-guid-1", Data: db.FeedItemData{Title: "Find Item 1"}},
	}
	_ = testDB.InsertFeedItems(post.ID, items)

	found, err := testDB.FindFeedItemsByPostID(post.ID)
	if err != nil {
		t.Fatalf("FindFeedItemsByPostID failed: %v", err)
	}
	if len(found) != 1 {
		t.Errorf("expected 1 feed item, got %d", len(found))
	}
}

// ============ Projects Tests ============

func TestUpsertProject(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("projectowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI projectowner", "comment")

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

	user, _ := testDB.RegisterUser("findprojectowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findprojectowner", "comment")
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

	user, _ := testDB.RegisterUser("statsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI statsowner", "comment")
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

// ============ Tuns Event Logs Tests ============

func TestInsertTunsEventLog(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("tunsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tunsowner", "comment")

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

	user, _ := testDB.RegisterUser("findtunsowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findtunsowner", "comment")
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

	user, _ := testDB.RegisterUser("tunsaddrowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI tunsaddrowner", "comment")
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

	user, _ := testDB.RegisterUser("accesslogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accesslogowner", "comment")

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

	user, _ := testDB.RegisterUser("findaccesslogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI findaccesslogowner", "comment")
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

	user, _ := testDB.RegisterUser("pubkeylogowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI pubkeylogowner", "comment")
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

	user, _ := testDB.RegisterUser("accessbypubkeyowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI accessbypubkeyowner", "comment")
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

	user, _ := testDB.RegisterUser("postdataowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI postdataowner", "comment")

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

	user, _ := testDB.RegisterUser("aclowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI aclowner", "comment")

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

func TestFeedItemData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("feedjsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI feedjsonbowner", "comment")
	post := mustInsertPost(t, &db.Post{UserID: user.ID, Filename: "feedjsonb.txt", Slug: "feedjsonb", Title: "Feed JSONB", Space: "feeds"})

	now := time.Now().Truncate(time.Second)
	items := []*db.FeedItem{
		{
			PostID: post.ID,
			GUID:   "jsonb-guid",
			Data: db.FeedItemData{
				Title:       "JSONB Item",
				Description: "Description",
				Content:     "Content",
				Link:        "http://example.com",
				PublishedAt: &now,
			},
		},
	}
	_ = testDB.InsertFeedItems(post.ID, items)

	found, err := testDB.FindFeedItemsByPostID(post.ID)
	if err != nil {
		t.Fatalf("FindFeedItemsByPostID failed: %v", err)
	}
	if len(found) != 1 {
		t.Fatalf("expected 1 item, got %d", len(found))
	}
	if found[0].Data.Title != "JSONB Item" {
		t.Errorf("expected title 'JSONB Item', got '%s'", found[0].Data.Title)
	}
}

func TestFeatureFlagData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("featurejsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI featurejsonbowner", "comment")

	err := testDB.AddPicoPlusUser("featurejsonbowner", "test@example.com", "stripe", "tx456")
	if err != nil {
		t.Fatalf("AddPicoPlusUser failed: %v", err)
	}

	feature, err := testDB.FindFeature(user.ID, "plus")
	if err != nil {
		t.Fatalf("FindFeature failed: %v", err)
	}
	if feature.Data.StorageMax != 10000000000 {
		t.Errorf("expected StorageMax 10000000000, got %d", feature.Data.StorageMax)
	}
	if feature.Data.FileMax != 50000000 {
		t.Errorf("expected FileMax 50000000, got %d", feature.Data.FileMax)
	}
}

func TestPaymentHistoryData_JSONBRoundtrip(t *testing.T) {
	cleanupTestData(t)

	user, _ := testDB.RegisterUser("paymentjsonbowner", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI paymentjsonbowner", "comment")

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
