package pgs

import (
	"bytes"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	"github.com/picosh/pico/pkg/storage/storagetest"
	"github.com/prometheus/client_golang/prometheus"
)

// rsyncFileMax lifts pgs's per-file cap so the test can move files larger
// than the default allows.
const rsyncFileMax = 24 << 20

// rsyncEnv is a pgs SSH server backed by one storage implementation, and a
// real rsync client set up to talk to it.
type rsyncEnv struct {
	t       *testing.T
	st      storage.StorageServe
	bucket  storage.Bucket
	rsh     string
	feature *db.FeatureFlag
	// sshArgs are the client options scp and sftp need to reach the server.
	sshArgs []string
	addr    string
	user    UserSSH
}

func newRsyncEnv(t *testing.T, st storage.StorageServe) *rsyncEnv {
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync not installed")
	}
	logger := slog.New(slog.DiscardHandler)
	dbpool := pgsdb.NewDBMemory(logger)
	dbpool.SetupTestData()
	dbpool.Feature.Data.FileMax = rsyncFileMax
	dbpool.Feature.Data.StorageMax = 256 << 20
	bucket := storagetest.Bucket(t, st, shared.GetAssetBucketName(dbpool.Users[0].ID))

	t.Setenv("PGS_SSH_PORT", "0")
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.sh"
	cfg.WebProtocol = "https"
	done := make(chan error)
	readyCh := make(chan *pssh.SSHServer)
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	go StartSshServerForTesting(cfg, done, readyCh)
	t.Cleanup(func() { close(done) })

	server := <-readyCh
	if server == nil {
		t.Fatal("failed to create ssh server")
	}
	var addr string
	for range 100 {
		server.Mu.Lock()
		if server.Listener != nil {
			addr = server.Listener.Addr().String()
		}
		server.Mu.Unlock()
		if addr != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if addr == "" {
		t.Fatal("server listener not ready")
	}

	user := GenerateUser()
	dbpool.Pubkeys = append(dbpool.Pubkeys, &db.PublicKey{
		ID:     "rsync-pubkey",
		UserID: dbpool.Users[0].ID,
		Key:    shared.KeyForKeyText(user.signer.PublicKey()),
	})
	keyFile := filepath.Join(t.TempDir(), "id_ed25519")
	block := &pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: user.privateKey}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(block), 0o600); err != nil {
		t.Fatal(err)
	}
	_, port, _ := net.SplitHostPort(addr)
	rsh := fmt.Sprintf(
		"ssh -p %s -i %s -o IdentitiesOnly=yes -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR",
		port, keyFile,
	)
	sshArgs := []string{"-P", port, "-i", keyFile, "-o", "IdentitiesOnly=yes", "-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}
	return &rsyncEnv{t: t, st: st, bucket: bucket, rsh: rsh, feature: dbpool.Feature, sshArgs: sshArgs, addr: addr, user: user}
}

// discardPubsub drops cache purges, which pgs sends from goroutines that can
// outlive the test.
type discardPubsub struct{}

func (discardPubsub) Read([]byte) (int, error)    { return 0, io.EOF }
func (discardPubsub) Write(b []byte) (int, error) { return len(b), nil }
func (discardPubsub) Close() error                { return nil }

type rsyncStats struct {
	output      string
	transferred int
	deleted     int
	literal     int
	matched     int
}

var statPatterns = map[string]*regexp.Regexp{
	"transferred": regexp.MustCompile(`Number of regular files transferred: ([\d,]+)`),
	"deleted":     regexp.MustCompile(`Number of deleted files: ([\d,]+)`),
	"literal":     regexp.MustCompile(`Literal data: ([\d,]+)`),
	"matched":     regexp.MustCompile(`Matched data: ([\d,]+)`),
}

func (e *rsyncEnv) rsync(args ...string) rsyncStats {
	e.t.Helper()
	return e.rsyncExit(0, args...)
}

func (e *rsyncEnv) rsyncExit(want int, args ...string) rsyncStats {
	e.t.Helper()
	args = append([]string{"-e", e.rsh, "--stats"}, args...)
	out, err := exec.Command("rsync", args...).CombinedOutput()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			e.t.Fatalf("rsync %q: %v\n%s", args, err, out)
		}
		code = exit.ExitCode()
	}
	if code != want {
		e.t.Fatalf("rsync %q: exit %d, want %d\n%s", args, code, want, out)
	}
	stat := func(name string) int {
		m := statPatterns[name].FindSubmatch(out)
		if m == nil {
			e.t.Fatalf("no %s in rsync output:\n%s", name, out)
		}
		n, _ := strconv.Atoi(strings.ReplaceAll(string(m[1]), ",", ""))
		return n
	}
	return rsyncStats{
		output:      string(out),
		transferred: stat("transferred"),
		deleted:     stat("deleted"),
		literal:     stat("literal"),
		matched:     stat("matched"),
	}
}

func (e *rsyncEnv) stored(name string) ([]byte, time.Time, bool) {
	e.t.Helper()
	r, info, err := e.st.GetObject(e.bucket, name)
	if err != nil {
		return nil, time.Time{}, false
	}
	defer func() { _ = r.Close() }()
	data, err := io.ReadAll(r)
	if err != nil {
		e.t.Fatalf("read %s: %v", name, err)
	}
	return data, info.LastModified, true
}

// tree is a local directory of files with fixed contents and mtimes.
type tree struct {
	t   *testing.T
	dir string
}

func (tr tree) write(name string, data []byte, mtime time.Time) {
	tr.t.Helper()
	p := filepath.Join(tr.dir, name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		tr.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		tr.t.Fatal(err)
	}
	tr.touch(name, mtime)
}

func (tr tree) touch(name string, mtime time.Time) {
	tr.t.Helper()
	if err := os.Chtimes(filepath.Join(tr.dir, name), mtime, mtime); err != nil {
		tr.t.Fatal(err)
	}
}

func (tr tree) read(name string) ([]byte, time.Time) {
	tr.t.Helper()
	p := filepath.Join(tr.dir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		tr.t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		tr.t.Fatal(err)
	}
	return data, info.ModTime()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func rsyncStorageBackends() map[string]func(t *testing.T) storage.StorageServe {
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

// TestRsyncStorage runs a real rsync client against pgs on each storage
// backend and checks that quick checks, deltas, checksums and deletes are
// driven by what storage holds.
func TestRsyncStorage(t *testing.T) {
	for name, newStorage := range rsyncStorageBackends() {
		t.Run(name, func(t *testing.T) {
			env := newRsyncEnv(t, newStorage(t))
			src := tree{t, t.TempDir()}
			remote := "localhost:/site"

			t0 := time.Unix(1_600_000_000, 0)
			t1 := time.Unix(1_650_000_000, 0)
			big := randomBytes(20 << 20)
			src.write("index.html", []byte("<html>index</html>"), t0)
			src.write("big.bin", big, t0)
			src.write("css/site.css", []byte("body{}"), t0)
			src.write("css/deep/x.css", []byte("a{}"), t1)
			src.write("img/logo.svg", []byte("<svg/>"), t1)
			src.write("keep.txt", []byte("excluded from deletes"), t0)

			s := env.rsync("-rtz", src.dir+"/", remote)
			if s.transferred != 6 {
				t.Fatalf("initial upload transferred %d files\n%s", s.transferred, s.output)
			}
			for name, mtime := range map[string]time.Time{"index.html": t0, "big.bin": t0, "css/deep/x.css": t1} {
				data, stored, ok := env.stored("/site/" + name)
				local, _ := src.read(name)
				if !ok || !bytes.Equal(data, local) {
					t.Fatalf("%s not stored correctly\n%s", name, s.output)
				}
				if !stored.Equal(mtime) {
					t.Errorf("%s stored with mtime %v, want %v", name, stored, mtime)
				}
			}

			t.Run("unchanged upload sends nothing", func(t *testing.T) {
				s := env.rsync("-rtz", src.dir+"/", remote)
				if s.transferred != 0 || s.literal != 0 {
					t.Fatalf("transferred %d files, %d literal bytes\n%s", s.transferred, s.literal, s.output)
				}
			})

			t.Run("changed file is sent as a delta", func(t *testing.T) {
				copy(big[1<<20:], "a small change in the middle of the file")
				src.write("big.bin", big, t1)
				s := env.rsync("-rtz", src.dir+"/", remote)
				if s.transferred != 1 || s.literal > 16<<10 || s.matched < len(big)-16<<10 {
					t.Fatalf("transferred %d, literal %d, matched %d\n%s", s.transferred, s.literal, s.matched, s.output)
				}
				data, mtime, _ := env.stored("/site/big.bin")
				if !bytes.Equal(data, big) || !mtime.Equal(t1) {
					t.Fatalf("stored big.bin wrong after delta (mtime %v)", mtime)
				}
			})

			t.Run("grown and shrunk files", func(t *testing.T) {
				src.write("index.html", []byte("<html>index, now with more content</html>"), t1)
				src.write("css/site.css", []byte("b{}"), t1)
				env.rsync("-rtz", src.dir+"/", remote)
				for _, name := range []string{"index.html", "css/site.css"} {
					data, _, _ := env.stored("/site/" + name)
					local, _ := src.read(name)
					if !bytes.Equal(data, local) {
						t.Errorf("%s stored as %q, want %q", name, data, local)
					}
				}
			})

			t.Run("checksum mode ignores mtimes", func(t *testing.T) {
				src.touch("css/deep/x.css", t0)
				s := env.rsync("-rcz", src.dir+"/", remote)
				if s.transferred != 0 {
					t.Fatalf("-c transferred %d files with unchanged contents\n%s", s.transferred, s.output)
				}
				s = env.rsync("-rtz", src.dir+"/", remote)
				if s.transferred != 1 || s.literal != 0 {
					t.Fatalf("mtime-only change transferred %d files, %d literal bytes\n%s", s.transferred, s.literal, s.output)
				}
				if _, mtime, _ := env.stored("/site/css/deep/x.css"); !mtime.Equal(t0) {
					t.Fatalf("mtime-only change stored mtime %v, want %v", mtime, t0)
				}
			})

			t.Run("oversized file is rejected", func(t *testing.T) {
				src.write("huge.bin", randomBytes(rsyncFileMax+1), t0)
				s := env.rsyncExit(23, "-rt", src.dir+"/", remote)
				if !strings.Contains(s.output, "huge.bin") {
					t.Fatalf("no warning about huge.bin\n%s", s.output)
				}
				if _, _, ok := env.stored("/site/huge.bin"); ok {
					t.Fatal("rejected file was stored")
				}
				if err := os.Remove(filepath.Join(src.dir, "huge.bin")); err != nil {
					t.Fatal(err)
				}
			})

			t.Run("delete removes what the source lacks", func(t *testing.T) {
				if err := os.RemoveAll(filepath.Join(src.dir, "css/deep")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(src.dir, "img/logo.svg")); err != nil {
					t.Fatal(err)
				}
				if err := os.Remove(filepath.Join(src.dir, "keep.txt")); err != nil {
					t.Fatal(err)
				}
				s := env.rsync("-rt", "--delete", "--exclude=keep.txt", src.dir+"/", remote)
				if s.deleted < 2 {
					t.Fatalf("deleted %d files\n%s", s.deleted, s.output)
				}
				for name, want := range map[string]bool{
					"css/deep/x.css": false,
					"img/logo.svg":   false,
					"keep.txt":       true,
					"css/site.css":   true,
					"big.bin":        true,
				} {
					if _, _, ok := env.stored("/site/" + name); ok != want {
						t.Errorf("%s stored=%v after delete, want %v", name, ok, want)
					}
				}
			})

			dst := tree{t, t.TempDir()}

			t.Run("download", func(t *testing.T) {
				s := env.rsync("-rtz", remote+"/", dst.dir+"/")
				if s.transferred != 4 {
					t.Fatalf("download transferred %d files\n%s", s.transferred, s.output)
				}
				for _, name := range []string{"index.html", "big.bin", "css/site.css", "keep.txt"} {
					got, mtime := dst.read(name)
					want, storedMtime, _ := env.stored("/site/" + name)
					if !bytes.Equal(got, want) {
						t.Errorf("downloaded %s differs from storage", name)
					}
					if !mtime.Equal(storedMtime) {
						t.Errorf("downloaded %s has mtime %v, storage has %v", name, mtime, storedMtime)
					}
				}
			})

			t.Run("unchanged download sends nothing", func(t *testing.T) {
				s := env.rsync("-rtz", remote+"/", dst.dir+"/")
				if s.transferred != 0 || s.literal != 0 {
					t.Fatalf("transferred %d files, %d literal bytes\n%s", s.transferred, s.literal, s.output)
				}
			})

			t.Run("download delta against a local copy", func(t *testing.T) {
				local, _ := dst.read("big.bin")
				copy(local[2<<20:], "local edit")
				dst.write("big.bin", local, t0)
				s := env.rsync("-rtz", remote+"/", dst.dir+"/")
				if s.transferred != 1 || s.literal > 16<<10 || s.matched < len(big)-16<<10 {
					t.Fatalf("transferred %d, literal %d, matched %d\n%s", s.transferred, s.literal, s.matched, s.output)
				}
				if got, _ := dst.read("big.bin"); !bytes.Equal(got, big) {
					t.Fatal("big.bin wrong after delta download")
				}
			})

			t.Run("download single file", func(t *testing.T) {
				one := tree{t, t.TempDir()}
				env.rsync("-t", remote+"/css/site.css", one.dir+"/")
				if got, _ := one.read("site.css"); string(got) != "b{}" {
					t.Fatalf("site.css is %q", got)
				}
			})
		})
	}
}

func TestRsyncStorageRejections(t *testing.T) {
	for name, newStorage := range rsyncStorageBackends() {
		t.Run(name, func(t *testing.T) {
			for _, reason := range []string{"ignore", "file limit", "storage quota"} {
				t.Run(reason, func(t *testing.T) {
					env := newRsyncEnv(t, newStorage(t))
					src := tree{t, t.TempDir()}
					remote := "localhost:/site"
					mtime := time.Unix(1_600_000_000, 0)
					wantExit := 23
					accepted := map[string]string{"z.txt": "ok"}
					switch reason {
					case "ignore":
						src.write("_pgs_ignore", []byte("a.txt\n"), mtime)
						env.rsync("-rt", src.dir+"/", remote)
						wantExit = 0
					case "file limit":
						env.feature.Data.FileMax = 3
					case "storage quota":
						src.write("z.txt", []byte("full"), mtime)
						env.rsync("-rt", src.dir+"/", remote)
						env.feature.Data.StorageMax = 4
						accepted["zz.txt"] = "x"
						src.write("zz.txt", []byte("x"), mtime)
					}
					src.write("a.txt", []byte("rejected"), mtime)
					src.write("z.txt", []byte("ok"), mtime)
					s := env.rsyncExit(wantExit, "-rt", "--remove-source-files", src.dir+"/", remote)
					if !strings.Contains(s.output, "a.txt") {
						t.Fatalf("missing per-file rejection message\n%s", s.output)
					}
					if reason == "ignore" && !strings.Contains(s.output, "a.txt skipped: file ignored: (/site/a.txt), https://pico.sh/pgs#-pgs-ignore\n") {
						t.Fatalf("unexpected ignore message\n%s", s.output)
					}
					if _, _, ok := env.stored("/site/a.txt"); ok {
						t.Fatal("rejected file was stored")
					}
					if data, _ := src.read("a.txt"); string(data) != "rejected" {
						t.Fatal("rejected source file changed")
					}
					for file, contents := range accepted {
						if data, _, ok := env.stored("/site/" + file); !ok || string(data) != contents {
							t.Errorf("accepted file %s not stored correctly", file)
						}
						if _, err := os.Stat(filepath.Join(src.dir, file)); !errors.Is(err, os.ErrNotExist) {
							t.Errorf("accepted file %s not removed from source: %v", file, err)
						}
					}
				})
			}
		})
	}
}

// TestRsyncStorageQuota checks that shrinking and deleting files frees quota
// for files later in the same run.
func TestRsyncStorageQuota(t *testing.T) {
	for name, newStorage := range rsyncStorageBackends() {
		t.Run(name, func(t *testing.T) {
			env := newRsyncEnv(t, newStorage(t))
			env.feature.Data.StorageMax = 3 << 20
			src := tree{t, t.TempDir()}
			remote := "localhost:/site"
			t0 := time.Unix(1_600_000_000, 0)

			src.write("a.bin", randomBytes(2<<20), t0)
			env.rsync("-rt", src.dir+"/", remote)

			// a.bin is sent before b.bin, which only fits once a.bin's
			// shrink is counted.
			src.write("a.bin", randomBytes(512<<10), t0)
			src.write("b.bin", randomBytes(2<<20), t0)
			if s := env.rsync("-rt", src.dir+"/", remote); s.transferred != 2 {
				t.Fatalf("transferred %d files, want 2\n%s", s.transferred, s.output)
			}

			if err := os.Remove(filepath.Join(src.dir, "a.bin")); err != nil {
				t.Fatal(err)
			}
			src.write("c.bin", randomBytes(768<<10), t0)
			if s := env.rsync("-rt", "--delete-before", src.dir+"/", remote); s.deleted != 1 || s.transferred != 1 {
				t.Fatalf("deleted %d and transferred %d files, want 1 and 1\n%s", s.deleted, s.transferred, s.output)
			}
			if _, _, ok := env.stored("/site/c.bin"); !ok {
				t.Fatal("c.bin was not stored")
			}
		})
	}
}

// TestRsyncStorageMergeFilters checks rsync's per-directory filter files
// against pgs storage, where dotfiles are only stored when _pgs_ignore
// allows them.
func TestRsyncStorageMergeFilters(t *testing.T) {
	for name, newStorage := range rsyncStorageBackends() {
		t.Run(name, func(t *testing.T) {
			for _, allowDotfiles := range []bool{true, false} {
				t.Run(fmt.Sprintf("dotfiles=%v", allowDotfiles), func(t *testing.T) {
					env := newRsyncEnv(t, newStorage(t))
					src := tree{t, t.TempDir()}
					remote := "localhost:/site"
					mtime := time.Unix(1_600_000_000, 0)
					if allowDotfiles {
						src.write("_pgs_ignore", []byte("_private\n"), mtime)
						env.rsync("-rt", src.dir+"/", remote)
					}
					src.write(".rsync-filter", []byte("- keep.txt\n"), mtime)
					src.write("sub/.rsync-filter", []byte("- *.tmp\n"), mtime)
					for _, f := range []string{"index.html", "keep.txt", "stale.txt", "sub/a.tmp", "sub/b.html"} {
						src.write(f, []byte(f), mtime)
					}
					env.rsync("-rt", src.dir+"/", remote)
					_, _, stored := env.stored("/site/.rsync-filter")
					if stored != allowDotfiles {
						t.Fatalf(".rsync-filter stored=%v with dotfiles allowed=%v", stored, allowDotfiles)
					}

					dst := tree{t, t.TempDir()}
					env.rsync("-rt", "-F", remote+"/", dst.dir+"/")
					for f, filtered := range map[string]bool{"keep.txt": true, "sub/a.tmp": true, "index.html": false, "sub/b.html": false} {
						_, err := os.Stat(filepath.Join(dst.dir, f))
						if got := err == nil; got != (!filtered || !allowDotfiles) {
							t.Errorf("download: %s present=%v", f, got)
						}
					}

					for _, f := range []string{"keep.txt", "stale.txt", "sub/a.tmp"} {
						if err := os.Remove(filepath.Join(src.dir, f)); err != nil {
							t.Fatal(err)
						}
					}
					s := env.rsync("-rt", "-F", "--delete", src.dir+"/", remote)
					for f, protected := range map[string]bool{"keep.txt": true, "sub/a.tmp": true, "stale.txt": false} {
						_, _, ok := env.stored("/site/" + f)
						if ok != (protected && allowDotfiles) {
							t.Errorf("upload: %s stored=%v\n%s", f, ok, s.output)
						}
					}
				})
			}
		})
	}
}
