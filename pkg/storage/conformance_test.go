package storage_test

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/storage"
	"github.com/picosh/pico/pkg/storage/storagetest"
)

func backends(t *testing.T) map[string]func(t *testing.T) storage.StorageServe {
	return map[string]func(t *testing.T) storage.StorageServe{
		"fs": func(t *testing.T) storage.StorageServe {
			st, err := storage.NewStorageFS(slog.New(slog.DiscardHandler), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			return st
		},
		"memory": func(t *testing.T) storage.StorageServe {
			st, err := storage.NewStorageMemory(map[string]map[string]string{})
			if err != nil {
				t.Fatal(err)
			}
			return st
		},
	}
}

func put(t *testing.T, st storage.StorageServe, b storage.Bucket, name string, data []byte, mtime time.Time) {
	t.Helper()
	_, size, err := st.PutObject(b, name, bytes.NewReader(data), &storage.ObjectInfo{LastModified: mtime})
	if err != nil {
		t.Fatalf("put %s: %v", name, err)
	}
	if size != int64(len(data)) {
		t.Fatalf("put %s: size %d, want %d", name, size, len(data))
	}
}

// entry renders a listing entry so listings compare as sorted strings.
func entry(name string, isDir bool, size int64, mtime time.Time) string {
	if isDir {
		return name + "/"
	}
	return fmt.Sprintf("%s %d %d", name, size, mtime.Unix())
}

func list(t *testing.T, st storage.StorageServe, b storage.Bucket, dir string, recursive bool) []string {
	t.Helper()
	infos, err := st.ListObjects(b, dir, recursive)
	if err != nil {
		t.Fatalf("list %s: %v", dir, err)
	}
	out := []string{}
	for _, info := range infos {
		out = append(out, entry(info.Name(), info.IsDir(), info.Size(), info.ModTime()))
	}
	slices.Sort(out)
	return out
}

func TestStorageConformance(t *testing.T) {
	for name, newStorage := range backends(t) {
		t.Run(name, func(t *testing.T) {
			st := newStorage(t)
			b := storagetest.Bucket(t, st, "conformance")

			t1 := time.Unix(1_700_000_000, 0)
			t2 := time.Unix(1_700_100_000, 0)
			put(t, st, b, "/proj/index.html", []byte("<html></html>"), t1)
			put(t, st, b, "/proj/css/site.css", []byte("body{}"), t2)
			put(t, st, b, "/proj/css/deep/x.css", []byte("a{}"), t1)
			put(t, st, b, "/other.txt", []byte("other"), t2)
			before := time.Now().Add(-time.Second)
			put(t, st, b, "/proj/now.txt", []byte("now"), time.Time{})

			t.Run("get", func(t *testing.T) {
				r, info, err := st.GetObject(b, "/proj/index.html")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = r.Close() }()
				data, _ := io.ReadAll(r)
				if string(data) != "<html></html>" || info.Size != 13 || !info.LastModified.Equal(t1) {
					t.Fatalf("got %q size=%d mtime=%v", data, info.Size, info.LastModified)
				}

				_, info, err = st.GetObject(b, "/proj/now.txt")
				if err != nil {
					t.Fatal(err)
				}
				if info.LastModified.Before(before) || info.LastModified.After(time.Now().Add(time.Second)) {
					t.Fatalf("object stored without an mtime has mtime %v", info.LastModified)
				}

				if _, _, err := st.GetObject(b, "/proj/missing.txt"); err == nil {
					t.Fatal("missing object returned no error")
				}
			})

			t.Run("stat", func(t *testing.T) {
				info, err := st.StatObject(b, "/proj/css/site.css")
				if err != nil {
					t.Fatal(err)
				}
				if info.Size != 6 || !info.LastModified.Equal(t2) {
					t.Fatalf("size=%d mtime=%v", info.Size, info.LastModified)
				}
				for _, missing := range []string{"/proj/missing.txt", "/proj/css", "/proj/css/"} {
					if _, err := st.StatObject(b, missing); !errors.Is(err, fs.ErrNotExist) {
						t.Errorf("StatObject(%q) returned %v, want fs.ErrNotExist", missing, err)
					}
				}
			})

			t.Run("list", func(t *testing.T) {
				now := list(t, st, b, "/proj/now.txt", false)[0]
				tests := []struct {
					dir       string
					recursive bool
					want      []string
				}{
					{"/proj/index.html", false, []string{entry("index.html", false, 13, t1)}},
					{"/proj", false, []string{"/"}},
					{"/proj/", false, []string{"css/", entry("index.html", false, 13, t1), now}},
					{"/proj/", true, []string{
						"css/",
						entry("css/deep/x.css", false, 3, t1),
						"css/deep/",
						entry("css/site.css", false, 6, t2),
						entry("index.html", false, 13, t1),
						now,
					}},
					{"/proj/css/", true, []string{entry("deep/x.css", false, 3, t1), "deep/", entry("site.css", false, 6, t2)}},
					{"/missing", false, []string{}},
					{"/missing/", true, []string{}},
				}
				for _, tt := range tests {
					want := slices.Clone(tt.want)
					slices.Sort(want)
					if got := list(t, st, b, tt.dir, tt.recursive); !slices.Equal(got, want) {
						t.Errorf("list(%q, recursive=%v)\n got %q\nwant %q", tt.dir, tt.recursive, got, want)
					}
				}
			})

			t.Run("overwrite", func(t *testing.T) {
				put(t, st, b, "/proj/css/site.css", []byte("body{color:red}"), t1)
				r, info, err := st.GetObject(b, "/proj/css/site.css")
				if err != nil {
					t.Fatal(err)
				}
				data, _ := io.ReadAll(r)
				_ = r.Close()
				if string(data) != "body{color:red}" || !info.LastModified.Equal(t1) {
					t.Fatalf("got %q mtime=%v", data, info.LastModified)
				}
			})

			t.Run("random access", func(t *testing.T) {
				data := make([]byte, 4<<20+12345)
				_, _ = rand.Read(data)
				put(t, st, b, "/proj/big.bin", data, t2)

				r, info, err := st.GetObject(b, "/proj/big.bin")
				if err != nil {
					t.Fatal(err)
				}
				defer func() { _ = r.Close() }()
				if info.Size != int64(len(data)) || !info.LastModified.Equal(t2) {
					t.Fatalf("size=%d mtime=%v", info.Size, info.LastModified)
				}
				for _, off := range []int64{0, 1<<20 - 10, 5, int64(len(data)) - 100, 3 << 20} {
					buf := make([]byte, 64<<10)
					n, err := r.ReadAt(buf, off)
					want := data[off:min(off+int64(len(buf)), int64(len(data)))]
					if !bytes.Equal(buf[:n], want) {
						t.Fatalf("ReadAt(%d) returned wrong data", off)
					}
					if n < len(buf) && err != io.EOF {
						t.Fatalf("short ReadAt(%d) returned %v", off, err)
					}
				}
				if _, err := r.Seek(int64(len(data))-1000, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				tail, err := io.ReadAll(r)
				if err != nil || !bytes.Equal(tail, data[len(data)-1000:]) {
					t.Fatalf("read after seek: %d bytes, %v", len(tail), err)
				}
				if _, err := r.Seek(0, io.SeekStart); err != nil {
					t.Fatal(err)
				}
				all, err := io.ReadAll(r)
				if err != nil || !bytes.Equal(all, data) {
					t.Fatalf("full read: %d bytes, %v", len(all), err)
				}
			})

			t.Run("failed upload", func(t *testing.T) {
				body := io.MultiReader(strings.NewReader(strings.Repeat("x", 1000)), errReader{})
				_, _, err := st.PutObject(b, "/proj/broken.txt", body, &storage.ObjectInfo{})
				if err == nil {
					t.Fatal("upload from a failing reader succeeded")
				}
				r, _, err := st.GetObject(b, "/proj/broken.txt")
				if err == nil {
					data, _ := io.ReadAll(r)
					_ = r.Close()
					t.Fatalf("failed upload left an object of %d bytes", len(data))
				}
			})

			t.Run("delete", func(t *testing.T) {
				if err := st.DeleteObject(b, "/proj/css/deep/x.css"); err != nil {
					t.Fatal(err)
				}
				if err := st.DeleteObject(b, "/proj/css/deep/x.css"); err != nil {
					t.Fatalf("deleting a missing object: %v", err)
				}
				if _, _, err := st.GetObject(b, "/proj/css/deep/x.css"); err == nil {
					t.Fatal("deleted object still readable")
				}
				if _, err := st.StatObject(b, "/proj/css/deep/x.css"); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("StatObject after delete returned %v", err)
				}
				got := list(t, st, b, "/proj/css/", true)
				want := []string{"deep/", entry("site.css", false, 15, t1)}
				if !slices.Equal(got, want) {
					t.Fatalf("after delete got %q, want %q", got, want)
				}
			})

			t.Run("directories", func(t *testing.T) {
				if err := st.PutDir(b, "/proj/empty/nested"); err != nil {
					t.Fatal(err)
				}
				if got, want := list(t, st, b, "/proj/empty/", true), []string{"nested/"}; !slices.Equal(got, want) {
					t.Fatalf("list after PutDir got %q, want %q", got, want)
				}
				if got := list(t, st, b, "/proj/empty/nested", false); !slices.Equal(got, []string{"/"}) {
					t.Fatalf("empty directory not found: %q", got)
				}
				if _, err := st.StatObject(b, "/proj/empty/nested"); !errors.Is(err, fs.ErrNotExist) {
					t.Fatalf("StatObject on a directory returned %v", err)
				}

				if err := st.DeleteObject(b, "/proj/empty"); err == nil {
					t.Fatal("deleting a non-empty directory succeeded")
				}
				for _, dir := range []string{"/proj/empty/nested", "/proj/empty", "/proj/css/deep"} {
					if err := st.DeleteObject(b, dir); err != nil {
						t.Fatalf("delete %s: %v", dir, err)
					}
					if got := list(t, st, b, dir, false); len(got) != 0 {
						t.Fatalf("%s still listed as %q", dir, got)
					}
				}

				put(t, st, b, "/solo/only.txt", []byte("x"), t1)
				if err := st.DeleteObject(b, "/solo/only.txt"); err != nil {
					t.Fatal(err)
				}
				if got := list(t, st, b, "/", false); !slices.Contains(got, "solo/") {
					t.Fatalf("directory went with its last file: %q", got)
				}
				if err := st.DeleteObject(b, "/solo"); err != nil {
					t.Fatal(err)
				}
				if got := list(t, st, b, "/", false); slices.Contains(got, "solo/") {
					t.Fatalf("deleted directory still listed: %q", got)
				}
			})

			t.Run("quota", func(t *testing.T) {
				size, err := st.GetBucketQuota(b)
				if err != nil {
					t.Fatal(err)
				}
				want := uint64(13 + 15 + 5 + 3 + 4<<20 + 12345)
				if size != want {
					t.Fatalf("quota %d, want %d", size, want)
				}
			})
		})
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, fmt.Errorf("connection reset") }
