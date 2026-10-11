// Package pgtest starts a postgres with pico's schema for tests.
package pgtest

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/pkg/db"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"
)

var (
	once     sync.Once
	dbURL    string
	stop     = func() {}
	startErr error
)

// URL is the connection string of a postgres with pico's schema, started
// the first time a test in the package asks for one. It is the database
// TEST_DATABASE_URL names, or a container. A test skips when there is
// neither, and fails when REQUIRE_TESTCONTAINERS=1.
func URL(t testing.TB) string {
	t.Helper()
	once.Do(func() {
		var started func()
		dbURL, started, startErr = start(context.Background())
		if startErr == nil {
			stop = started
		}
	})
	if startErr != nil {
		if os.Getenv("REQUIRE_TESTCONTAINERS") == "1" {
			t.Fatalf("postgres: %v", startErr)
		}
		t.Skipf("postgres unavailable, set TEST_DATABASE_URL or start docker: %v", startErr)
	}
	return dbURL
}

// Main runs a package's tests and stops the postgres they started.
func Main(m *testing.M) {
	code := m.Run()
	stop()
	os.Exit(code)
}

// Clean deletes every row the tests wrote.
func Clean(t testing.TB, d *sqlx.DB) {
	t.Helper()
	tables := []string{
		"access_logs", "tuns_event_logs", "analytics_visits",
		"feed_items", "post_aliases", "post_tags", "posts",
		"projects", "feature_flags", "payment_history", "tokens",
		"public_keys", "pipe_monitors", "app_users",
	}
	for _, table := range tables {
		if _, err := d.Exec(fmt.Sprintf("DELETE FROM %s", table)); err != nil {
			t.Fatalf("failed to clean up %s: %v", table, err)
		}
	}
}

// InsertPost inserts post, publishing and updating it now unless it says
// otherwise.
func InsertPost(t testing.TB, d interface {
	InsertPost(post *db.Post) (*db.Post, error)
}, post *db.Post) *db.Post {
	t.Helper()
	now := time.Now()
	if post.UpdatedAt == nil {
		post.UpdatedAt = &now
	}
	if post.PublishAt == nil {
		post.PublishAt = &now
	}
	created, err := d.InsertPost(post)
	if err != nil {
		t.Fatalf("InsertPost failed: %v", err)
	}
	return created
}

func start(ctx context.Context) (string, func(), error) {
	// Packages test in parallel, so each gets its own database.
	if base := os.Getenv("TEST_DATABASE_URL"); base != "" {
		return createDatabase(base)
	}

	if err := exec.Command("docker", "info").Run(); err != nil {
		return "", nil, fmt.Errorf("docker: %w", err)
	}
	container, err := postgres.Run(ctx,
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
		return "", nil, err
	}
	terminate := func() { _ = container.Terminate(ctx) }
	connStr, err := container.ConnectionString(ctx, "sslmode=disable")
	if err == nil {
		err = migrate(connStr)
	}
	if err != nil {
		terminate()
		return "", nil, err
	}
	return connStr, terminate, nil
}

func createDatabase(base string) (string, func(), error) {
	server, err := sqlx.Connect("postgres", base)
	if err != nil {
		return "", nil, err
	}
	name := fmt.Sprintf("pico_test_%d_%d", os.Getpid(), time.Now().UnixNano())
	if _, err := server.Exec("CREATE DATABASE " + name); err != nil {
		_ = server.Close()
		return "", nil, err
	}
	drop := func() {
		_, _ = server.Exec("DROP DATABASE " + name + " WITH (FORCE)")
		_ = server.Close()
	}
	u, err := url.Parse(base)
	if err == nil {
		u.Path = "/" + name
		err = migrate(u.String())
	}
	if err != nil {
		drop()
		return "", nil, err
	}
	return u.String(), drop, nil
}

func migrate(connStr string) error {
	d, err := sqlx.Connect("postgres", connStr)
	if err != nil {
		return err
	}
	defer func() { _ = d.Close() }()

	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("failed to get current file path")
	}
	migrationsDir := filepath.Join(filepath.Dir(filename), "..", "..", "..", "..", "sql", "migrations")
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		return fmt.Errorf("failed to read migrations directory: %w", err)
	}
	// They're date-prefixed, so alphabetical order is correct.
	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && filepath.Ext(entry.Name()) == ".sql" {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)
	for _, name := range migrationFiles {
		content, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", name, err)
		}
		if _, err := d.Exec(string(content)); err != nil {
			return fmt.Errorf("failed to execute migration %s: %w", name, err)
		}
	}
	return nil
}
