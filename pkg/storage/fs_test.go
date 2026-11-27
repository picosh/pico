package storage

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/picosh/pico/pkg/send/utils"
	"golang.org/x/sync/semaphore"
)

func TestFsAdapter(t *testing.T) {
	logger := slog.Default()
	f, err := os.MkdirTemp("", "fs-tests-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = os.RemoveAll(f)
	}()

	st, err := NewStorageFS(logger, f)
	if err != nil {
		t.Fatal(err)
	}

	bucketName := "main"
	// create bucket
	bucket, err := st.UpsertBucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}

	// ensure bucket exists
	file, err := os.Stat(bucket.Path)
	if err != nil {
		t.Fatal(err)
	}
	if !file.IsDir() {
		t.Fatal("bucket must be directory")
	}

	bucketCheck, err := st.GetBucket(bucketName)
	if err != nil {
		t.Fatal(err)
	}
	if bucketCheck.Path != bucket.Path || bucketCheck.Name != bucket.Name {
		t.Fatal("upsert and get bucket incongruent")
	}

	modTime := time.Now()

	str := "here is a test file"
	reader := strings.NewReader(str)
	actualPath, size, err := st.PutObject(bucket, "./nice/test.txt", reader, &ObjectInfo{
		LastModified: modTime,
	})
	if err != nil {
		t.Fatal(err)
	}
	if size != int64(len(str)) {
		t.Fatalf("size, actual: %d, expected: %d", size, int64(len(str)))
	}
	expectedPath := filepath.Join(bucket.Path, "nice", "test.txt")
	if actualPath != expectedPath {
		t.Fatalf("path, actual: %s, expected: %s", actualPath, expectedPath)
	}

	// ensure file exists
	_, err = os.Stat(expectedPath)
	if err != nil {
		t.Fatal(err)
	}

	// get file
	r, info, err := st.GetObject(bucket, "nice/test.txt")
	if err != nil {
		t.Fatal(err)
	}
	buf := new(strings.Builder)
	_, err = io.Copy(buf, r)
	if err != nil {
		t.Fatal(err)
	}
	actualStr := buf.String()
	if actualStr != str {
		t.Fatalf("contents, actual: %s, expected: %s", actualStr, str)
	}
	if info.Size != size {
		t.Fatalf("size, actual: %d, expected: %d", size, info.Size)
	}

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "./here/we/go/again.txt", reader, &ObjectInfo{
		LastModified: modTime,
	})
	if err != nil {
		t.Fatal(err)
	}

	// list objects
	objs, err := st.ListObjects(bucket, "/", true)
	if err != nil {
		t.Fatal(err)
	}

	expectedObjs := []fs.FileInfo{
		&utils.VirtualFile{
			FName:  "here",
			FIsDir: true,
		},
		&utils.VirtualFile{
			FName:  "here/we",
			FIsDir: true,
		},
		&utils.VirtualFile{
			FName:  "here/we/go",
			FIsDir: true,
		},
		&utils.VirtualFile{FName: "here/we/go/again.txt", FSize: 25},
		&utils.VirtualFile{
			FName:  "nice",
			FIsDir: true,
		},
		&utils.VirtualFile{FName: "nice/test.txt", FSize: 19},
	}
	ignore := cmpopts.IgnoreFields(utils.VirtualFile{}, "FModTime", "FSize")
	if cmp.Equal(objs, expectedObjs, ignore) == false {
		//nolint
		t.Fatal(cmp.Diff(objs, expectedObjs, ignore))
	}

	// it should not error if folder doesn't exist
	_, err = st.ListObjects(bucket, "/not-real", true)
	if err != nil {
		t.Fatal(err)
	}

	// list buckets
	aBucket, _ := st.UpsertBucket("another")
	_, _ = st.UpsertBucket("and-another")
	buckets, err := st.ListBuckets()
	if err != nil {
		t.Fatal(err)
	}
	expectedBuckets := []string{"and-another", "another", "main"}
	if cmp.Equal(buckets, expectedBuckets) == false {
		//nolint
		t.Fatal(cmp.Diff(buckets, expectedBuckets))
	}

	// delete bucket
	err = st.DeleteBucket(aBucket)
	if err != nil {
		t.Fatal(err)
	}

	// ensure bucket was actually deleted
	_, err = os.Stat(aBucket.Path)
	if !os.IsNotExist(err) {
		t.Fatal("directory should have been deleted")
	}

	err = st.DeleteObject(bucket, "nice/test.txt")
	if err != nil {
		t.Fatal(err)
	}

	// ensure file was actually deleted
	_, err = os.Stat(filepath.Join(bucket.Path, "nice/test.txt"))
	if !os.IsNotExist(err) {
		t.Fatal("file should have been deleted")
	}

	// the containing folder stays, as it would on any filesystem
	_, err = os.Stat(filepath.Join(bucket.Path, "nice"))
	if err != nil {
		t.Fatal("containing folder should have been kept")
	}

	// it should not error if file doesn't exist
	err = st.DeleteObject(bucket, "nice/not-real.txt")
	if err != nil {
		t.Fatal(err)
	}

	str = "a deeply nested test file"
	reader = strings.NewReader(str)
	_, _, err = st.PutObject(bucket, "./here/yes/we/can.txt", reader, &ObjectInfo{
		LastModified: modTime,
	})
	if err != nil {
		t.Fatal(err)
	}

	// delete deeply nested file; its now empty parent folders stay
	err = st.DeleteObject(bucket, "here/yes/we/can.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = os.Stat(filepath.Join(bucket.Path, "here/yes/we"))
	if err != nil {
		t.Fatal("containing folder should have been kept")
	}

	// delete bucket even with file contents
	err = st.DeleteBucket(bucket)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFsLegacyKeepDir(t *testing.T) {
	st, err := NewStorageFS(slog.New(slog.DiscardHandler), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := st.UpsertBucket("legacy")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"proj/empty/" + legacyKeepDir, "proj/index.html"} {
		if _, _, err := st.PutObject(bucket, name, strings.NewReader(""), &ObjectInfo{}); err != nil {
			t.Fatal(err)
		}
	}

	for _, recursive := range []bool{false, true} {
		infos, err := st.ListObjects(bucket, "proj/", recursive)
		if err != nil {
			t.Fatal(err)
		}
		for _, info := range infos {
			if strings.Contains(info.Name(), legacyKeepDir) {
				t.Fatalf("listing (recursive=%v) shows %s", recursive, info.Name())
			}
		}
	}

	if err := st.DeleteObject(bucket, "proj/empty"); err != nil {
		t.Fatalf("deleting a directory holding only a marker: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bucket.Path, "proj/empty")); !os.IsNotExist(err) {
		t.Fatalf("directory still exists: %v", err)
	}
}

func TestFsAdapterStaysInBucket(t *testing.T) {
	base := t.TempDir()
	st, err := NewStorageFS(slog.Default(), filepath.Join(base, "store"))
	if err != nil {
		t.Fatal(err)
	}
	mine, err := st.UpsertBucket("mine")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.UpsertBucket("other")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.PutObject(other, "/secret.txt", strings.NewReader("secret"), &ObjectInfo{}); err != nil {
		t.Fatal(err)
	}

	loc, _, err := st.PutObject(mine, "/../../escaped.txt", strings.NewReader("x"), &ObjectInfo{})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(mine.Path, "escaped.txt"); loc != want {
		t.Fatalf("wrote to %s, want %s", loc, want)
	}
	if _, err := os.Stat(filepath.Join(base, "escaped.txt")); err == nil {
		t.Fatal("PutObject wrote outside the storage dir")
	}

	if _, _, err := st.GetObject(mine, "../other/secret.txt"); err == nil {
		t.Fatal("GetObject read another bucket")
	}
	if _, err := st.StatObject(mine, "../other/secret.txt"); err == nil {
		t.Fatal("StatObject reached another bucket")
	}
	if err := st.DeleteObject(mine, "../other/secret.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.StatObject(other, "secret.txt"); err != nil {
		t.Fatal("DeleteObject removed a file in another bucket")
	}
	if list, err := st.ListObjects(mine, "../other/", true); err != nil || len(list) != 0 {
		t.Fatalf("ListObjects listed another bucket: %v %v", list, err)
	}
}

func TestFsDirSize(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "big")
	if err := os.WriteFile(outside, make([]byte, 1<<20), 0o644); err != nil {
		t.Fatal(err)
	}

	// More directories than slots, nested, and one directory with more
	// files than a stat chunk.
	var want int64
	for i := range 96 {
		dir := filepath.Join(root, fmt.Sprintf("p%d", i), "a", "b")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		for j, d := range []string{dir, filepath.Dir(dir)} {
			n := i*10 + j + 1
			if err := os.WriteFile(filepath.Join(d, "f"), make([]byte, n), 0o644); err != nil {
				t.Fatal(err)
			}
			want += int64(n)
		}
	}
	flat := filepath.Join(root, "flat")
	if err := os.Mkdir(flat, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range walkChunk*3 + 5 {
		if err := os.WriteFile(filepath.Join(flat, fmt.Sprintf("f%d", i)), make([]byte, i), 0o644); err != nil {
			t.Fatal(err)
		}
		want += int64(i)
	}
	link := filepath.Join(root, "p0", "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	want += int64(len(outside))

	// With no slots every directory and chunk is walked inline.
	for _, n := range []int64{0, 4, 64} {
		got, err := walkSize(root, semaphore.NewWeighted(n))
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("%d slots: dirSize = %d, want %d", n, got, want)
		}
	}

	// Concurrent walks share the slots without blocking each other.
	shared := semaphore.NewWeighted(2)
	errs := make(chan error, 8)
	for range 8 {
		go func() {
			got, err := walkSize(root, shared)
			if err == nil && got != want {
				err = fmt.Errorf("shared slots: dirSize = %d, want %d", got, want)
			}
			errs <- err
		}()
	}
	for range 8 {
		if err := <-errs; err != nil {
			t.Fatal(err)
		}
	}

	if _, err := dirSize(filepath.Join(root, "missing")); !os.IsNotExist(err) {
		t.Fatalf("missing root: err = %v, want not exist", err)
	}
}

// walkDirList is how ListObjects listed recursively with filepath.WalkDir,
// kept to check the parallel walk against.
func walkDirList(root string) ([]utils.VirtualFile, error) {
	var files []utils.VirtualFile
	err := filepath.WalkDir(root, func(s string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == legacyKeepDir {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		fname := strings.TrimPrefix(strings.TrimPrefix(s, root), "/")
		if fname == "" {
			return nil
		}
		files = append(files, utils.VirtualFile{
			FName:    fname,
			FIsDir:   info.IsDir(),
			FSize:    info.Size(),
			FModTime: info.ModTime(),
		})
		return nil
	})
	return files, err
}

func TestFsListTreeMatchesWalkDir(t *testing.T) {
	root := t.TempDir()
	write := func(rel string, n int) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Names where sorting whole paths differs from WalkDir's order.
	for i, rel := range []string{
		"a", "a-b/x", "a.b", "b/a/z", "b/a-z", "b/a/y/q", "c/" + legacyKeepDir,
		"d/" + legacyKeepDir + "/inner", "e/E", "e/e", "e/é",
	} {
		write(rel, i+1)
	}
	for i := range walkChunk*2 + 3 {
		write(fmt.Sprintf("flat/%03d", i), i)
		write(fmt.Sprintf("tree/%d/f", i), i)
	}
	if err := os.Symlink(filepath.Join(root, "b"), filepath.Join(root, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	want, err := walkDirList(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []int64{0, 4, 32} {
		got, err := listTree(root, semaphore.NewWeighted(n))
		if err != nil {
			t.Fatal(err)
		}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("%d slots: listTree differs from WalkDir (-want +got):\n%s", n, diff)
		}
	}

	// A symlinked or regular-file root has nothing below it, as with WalkDir.
	for _, r := range []string{filepath.Join(root, "link"), filepath.Join(root, "a")} {
		got, err := listTree(r, semaphore.NewWeighted(4))
		if err != nil || len(got) != 0 {
			t.Fatalf("listTree(%s) = %v, %v; want nothing", r, got, err)
		}
	}

	entries, err := os.ReadDir(filepath.Join(root, "flat"))
	if err != nil {
		t.Fatal(err)
	}
	var wantFlat []utils.VirtualFile
	for _, d := range entries {
		info, err := d.Info()
		if err != nil {
			t.Fatal(err)
		}
		wantFlat = append(wantFlat, utils.VirtualFile{FName: info.Name(), FSize: info.Size(), FModTime: info.ModTime()})
	}
	for _, n := range []int64{0, 4, 32} {
		if diff := cmp.Diff(wantFlat, listEntries(entries, semaphore.NewWeighted(n))); diff != "" {
			t.Fatalf("%d slots: listEntries differs (-want +got):\n%s", n, diff)
		}
	}
}

func TestFsListObjectsUnreadableDir(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads any directory")
	}
	st, err := NewStorageFS(slog.Default(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.UpsertBucket("main")
	if err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(b.Path, "p", "locked")
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(b.Path, "p", "ok.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chmod(locked, 0o755) }()

	// As with WalkDir, an unreadable directory collapses the listing to the
	// listed directory itself.
	got, err := st.ListObjects(b, "p/", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !got[0].IsDir() {
		t.Fatalf("ListObjects = %v, want only the listed directory", got)
	}
}
