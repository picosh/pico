package pgs

import (
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	"github.com/pkg/sftp"
	"github.com/prometheus/client_golang/prometheus"
)

// TestRsyncDeleteDirectoryWithKeepDir verifies that rsync --delete can
// successfully delete a directory that contains . _pico_keep_dir markers.
//
// Regression test for: "remove /storage/.../project/... directory not empty"
// The bug occurred because . _pico_keep_dir files were not cleaned up when
// their parent directory was explicitly deleted, causing os.Remove() to fail.
func TestRsyncDeleteDirectoryWithKeepDir(t *testing.T) {
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}
	logger := slog.New(
		slog.NewTextHandler(os.Stdout, opts),
	)
	slog.SetDefault(logger)
	dbpool := pgsdb.NewDBMemory(logger)
	dbpool.SetupTestData()

	// Use filesystem storage so the "directory not empty" bug manifests
	tmpDir, err := os.MkdirTemp("", "pgs-test-storage-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	st, err := storage.NewStorageFS(logger, tmpDir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	pubsub := NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()

	_ = os.Setenv("PGS_SSH_PORT", "0")
	_ = os.Setenv("PGS_PROM_PORT", "0")

	cfg := NewPgsConfig(logger, dbpool, st, pubsub)
	done := make(chan error)
	readyCh := make(chan *pssh.SSHServer)
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	go StartSshServerForTesting(cfg, done, readyCh)

	server := <-readyCh
	if server == nil {
		t.Fatal("failed to create ssh server")
	}

	var actualAddr string
	for i := 0; i < 100; i++ {
		server.Mu.Lock()
		listener := server.Listener
		server.Mu.Unlock()
		if listener != nil {
			actualAddr = listener.Addr().String()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if actualAddr == "" {
		t.Fatal("server listener not ready")
	}

	user := GenerateUser()
	dbpool.Pubkeys = append(dbpool.Pubkeys, &db.PublicKey{
		ID:     "test-pubkey-keepdir",
		UserID: dbpool.Users[0].ID,
		Key:    shared.KeyForKeyText(user.signer.PublicKey()),
	})

	conn, err := user.NewClientAddr(actualAddr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatalf("failed to create sftp client: %v", err)
	}
	defer func() {
		_ = client.Close()
	}()

	// Create temp directory with nested structure
	name, err := os.MkdirTemp("", "rsync-dir-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(name)
	}()

	// Create: project/subdir/nested/file.txt
	nestedDir := filepath.Join(name, "subdir", "nested")
	err = os.MkdirAll(nestedDir, 0755)
	if err != nil {
		t.Fatalf("failed to create nested dir: %v", err)
	}
	err = os.WriteFile(filepath.Join(nestedDir, "deep.txt"), []byte("deep content"), 0644)
	if err != nil {
		t.Fatalf("failed to write deep.txt: %v", err)
	}

	// Create: project/subdir/file.txt
	err = os.WriteFile(filepath.Join(name, "subdir", "file.txt"), []byte("subdir content"), 0644)
	if err != nil {
		t.Fatalf("failed to write file.txt: %v", err)
	}

	// Create: project/index.html (stays after delete)
	err = os.WriteFile(filepath.Join(name, "index.html"), []byte("index content"), 0644)
	if err != nil {
		t.Fatalf("failed to write index.html: %v", err)
	}

	block := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: user.privateKey,
	}
	keyFile := filepath.Join(name, "id_ed25519")
	err = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600)
	if err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	_, port, err := net.SplitHostPort(actualAddr)
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}

	eCmd := fmt.Sprintf(
		"ssh -p %s -o IdentitiesOnly=yes -i %s -o StrictHostKeyChecking=no",
		port, keyFile,
	)

	// Upload files including the subdir/ directory
	cmd := exec.Command("rsync", "-rv", "-e", eCmd, name+"/", "localhost:/testdir")
	result, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rsync upload failed: %v\noutput: %s", err, result)
	}

	// Verify files exist on the server
	_, err = client.Lstat("/testdir/subdir/file.txt")
	if err != nil {
		t.Fatalf("subdir/file.txt not found after upload: %v", err)
	}
	_, err = client.Lstat("/testdir/subdir/nested/deep.txt")
	if err != nil {
		t.Fatalf("subdir/nested/deep.txt not found after upload: %v", err)
	}

	// Now remove the entire subdir/ from the local directory
	err = os.RemoveAll(filepath.Join(name, "subdir"))
	if err != nil {
		t.Fatalf("failed to remove local subdir: %v", err)
	}

	// Run rsync --delete - this should delete the subdir/ directory
	// WITHOUT failing with "directory not empty"
	delCmd := exec.Command("rsync", "-rv", "--delete", "-e", eCmd, name+"/", "localhost:/testdir")
	result, err = delCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rsync --delete failed (this was the bug - 'directory not empty'): %v\noutput: %s", err, result)
	}

	// Verify the subdir and its contents are gone
	_, err = client.Lstat("/testdir/subdir")
	if err == nil {
		t.Fatal("subdir/ should have been deleted but still exists")
	}
	// SFTP can return "no such file" or "file does not exist" depending on version
	if !strings.Contains(err.Error(), "no such file") && !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("expected 'not found' error, got: %v", err)
	}

	// Verify the . _pico_keep_dir markers are also cleaned up
	_, err = client.Lstat("/testdir/subdir/._pico_keep_dir")
	if err == nil {
		t.Fatal("subdir/._pico_keep_dir should have been deleted but still exists")
	}

	// Verify index.html still exists (it wasn't deleted)
	fi, err := client.Lstat("/testdir/index.html")
	if err != nil {
		t.Fatalf("index.html should still exist: %v", err)
	}
	if fi.Size() != 13 {
		t.Errorf("index.html has wrong size: got %d, want 13", fi.Size())
	}

	close(done)
	time.Sleep(100 * time.Millisecond)
}

// TestRsyncDeleteNestedEmptyDirectories verifies that rsync --delete handles
// deeply nested directory structures where intermediate directories become empty
// and need . _pico_keep_dir markers managed correctly.
func TestRsyncDeleteNestedEmptyDirectories(t *testing.T) {
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}
	logger := slog.New(
		slog.NewTextHandler(io.Discard, opts),
	)
	slog.SetDefault(logger)
	dbpool := pgsdb.NewDBMemory(logger)
	dbpool.SetupTestData()

	tmpDir, err := os.MkdirTemp("", "pgs-test-nested-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(tmpDir)
	}()

	st, err := storage.NewStorageFS(logger, tmpDir)
	if err != nil {
		t.Fatalf("failed to create storage: %v", err)
	}

	pubsub := NewPubsubChan()
	defer func() {
		_ = pubsub.Close()
	}()

	_ = os.Setenv("PGS_SSH_PORT", "0")
	_ = os.Setenv("PGS_PROM_PORT", "0")

	cfg := NewPgsConfig(logger, dbpool, st, pubsub)
	done := make(chan error)
	readyCh := make(chan *pssh.SSHServer)
	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	go StartSshServerForTesting(cfg, done, readyCh)

	server := <-readyCh
	if server == nil {
		t.Fatal("failed to create ssh server")
	}

	var actualAddr string
	for i := 0; i < 100; i++ {
		server.Mu.Lock()
		listener := server.Listener
		server.Mu.Unlock()
		if listener != nil {
			actualAddr = listener.Addr().String()
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if actualAddr == "" {
		t.Fatal("server listener not ready")
	}

	user := GenerateUser()
	dbpool.Pubkeys = append(dbpool.Pubkeys, &db.PublicKey{
		ID:     "test-pubkey-nested",
		UserID: dbpool.Users[0].ID,
		Key:    shared.KeyForKeyText(user.signer.PublicKey()),
	})

	conn, err := user.NewClientAddr(actualAddr)
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() {
		_ = conn.Close()
	}()

	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatalf("failed to create sftp client: %v", err)
	}
	defer func() {
		_ = client.Close()
	}()

	// Create temp directory with deeply nested structure
	name, err := os.MkdirTemp("", "rsync-nested-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() {
		_ = os.RemoveAll(name)
	}()

	// Create: a/b/c/d/file.txt
	deepDir := filepath.Join(name, "a", "b", "c", "d")
	err = os.MkdirAll(deepDir, 0755)
	if err != nil {
		t.Fatalf("failed to create deep dir: %v", err)
	}
	err = os.WriteFile(filepath.Join(deepDir, "file.txt"), []byte("deep"), 0644)
	if err != nil {
		t.Fatalf("failed to write file: %v", err)
	}

	block := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: user.privateKey,
	}
	keyFile := filepath.Join(name, "id_ed25519")
	err = os.WriteFile(keyFile, pem.EncodeToMemory(block), 0600)
	if err != nil {
		t.Fatalf("failed to write key file: %v", err)
	}

	_, port, err := net.SplitHostPort(actualAddr)
	if err != nil {
		t.Fatalf("failed to parse server address: %v", err)
	}

	eCmd := fmt.Sprintf(
		"ssh -p %s -o IdentitiesOnly=yes -i %s -o StrictHostKeyChecking=no",
		port, keyFile,
	)

	// Upload
	cmd := exec.Command("rsync", "-rv", "-e", eCmd, name+"/", "localhost:/deep")
	result, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rsync upload failed: %v\noutput: %s", err, result)
	}

	// Remove the entire a/ directory locally
	err = os.RemoveAll(filepath.Join(name, "a"))
	if err != nil {
		t.Fatalf("failed to remove local a/: %v", err)
	}

	// rsync --delete should handle the deeply nested structure
	delCmd := exec.Command("rsync", "-rv", "--delete", "-e", eCmd, name+"/", "localhost:/deep")
	result, err = delCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("rsync --delete failed on nested dirs: %v\noutput: %s", err, result)
	}

	// Verify the entire tree is gone
	_, err = client.Lstat("/deep/a")
	if err == nil {
		t.Fatal("/deep/a should have been deleted but still exists")
	}

	close(done)
	time.Sleep(100 * time.Millisecond)
}
