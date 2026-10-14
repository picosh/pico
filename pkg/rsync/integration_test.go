package rsync

import (
	"bytes"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// dirFS serves a local directory.
type dirFS struct{ root string }

func (d dirFS) full(name string) string { return filepath.Join(d.root, filepath.FromSlash(name)) }

func (d dirFS) Stat(name string) (FileInfo, error) {
	st, err := os.Stat(d.full(name))
	if err != nil {
		return FileInfo{}, err
	}
	return FileInfo{Name: st.Name(), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()}, nil
}

func (d dirFS) ReadDir(dir string, recursive bool) ([]FileInfo, error) {
	base := d.full(dir)
	var out []FileInfo
	err := filepath.WalkDir(base, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == base {
			return nil
		}
		st, err := e.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(base, p)
		out = append(out, FileInfo{Name: filepath.ToSlash(rel), Size: st.Size(), ModTime: st.ModTime(), IsDir: st.IsDir()})
		if e.IsDir() && !recursive {
			return filepath.SkipDir
		}
		return nil
	})
	return out, err
}

func (d dirFS) Open(name string) (File, FileInfo, error) {
	f, err := os.Open(d.full(name))
	if err != nil {
		return nil, FileInfo{}, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, FileInfo{}, err
	}
	return f, FileInfo{Name: st.Name(), Size: st.Size(), ModTime: st.ModTime()}, nil
}

func (d dirFS) Put(name string, info FileInfo, r io.Reader) (string, error) {
	p := d.full(name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(filepath.Dir(p), ".tmp-")
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	if err := os.Rename(tmp.Name(), p); err != nil {
		return "", err
	}
	return "", os.Chtimes(p, info.ModTime, info.ModTime)
}

func (d dirFS) Remove(name string, isDir bool) error { return os.Remove(d.full(name)) }

type rejectingFS struct {
	dirFS
	err error
}

func (d rejectingFS) Put(name string, info FileInfo, r io.Reader) (string, error) {
	if filepath.Base(name) == "skip.txt" {
		return "", d.err
	}
	return d.dirFS.Put(name, info, r)
}

// TestHelperProcess is the remote shell rsync runs in these tests. It serves
// the directory in RSYNC_TEST_ROOT on stdin and stdout.
func TestHelperProcess(t *testing.T) {
	root := os.Getenv("RSYNC_TEST_ROOT")
	if root == "" {
		t.Skip("helper process")
	}
	args := os.Args
	for len(args) > 0 && args[0] != "--" {
		args = args[1:]
	}
	// Skip "--", the host name and "rsync".
	if len(args) < 3 {
		os.Exit(2)
	}
	args = args[3:]
	rw := struct {
		io.Reader
		io.Writer
	}{os.Stdin, os.Stdout}
	var fsys FS = dirFS{root: root}
	switch os.Getenv("RSYNC_TEST_PUT_RESULT") {
	case "ignored":
		fsys = rejectingFS{dirFS{root: root}, fmt.Errorf("policy: %w", ErrFileIgnored)}
	case "failed":
		fsys = rejectingFS{dirFS{root: root}, errors.New("storage backend failed")}
	}
	err := Serve(Config{FS: fsys, Stderr: os.Stderr}, rw, args)
	code := 0
	var exit *ExitError
	if errors.As(err, &exit) {
		code = exit.Code
	} else if err != nil {
		code = 1
	}
	os.Exit(code)
}

type rsyncEnv struct {
	t      *testing.T
	remote string // directory our server serves
	local  string
}

func newRsyncEnv(t *testing.T) *rsyncEnv {
	t.Helper()
	if _, err := exec.LookPath("rsync"); err != nil {
		t.Skip("rsync binary not found")
	}
	return &rsyncEnv{t: t, remote: t.TempDir(), local: t.TempDir()}
}

// run executes the rsync client with our server on the other end.
func (e *rsyncEnv) run(args ...string) (string, error) {
	e.t.Helper()
	rsh := fmt.Sprintf("%s -test.run=^TestHelperProcess$ --", os.Args[0])
	cmd := exec.Command("rsync", append([]string{"-e", rsh}, args...)...)
	// A race build sleeps a second before exiting, once per helper.
	gorace := strings.TrimSpace(os.Getenv("GORACE") + " atexit_sleep_ms=0")
	cmd.Env = append(os.Environ(), "RSYNC_TEST_ROOT="+e.remote, "GORACE="+gorace)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func (e *rsyncEnv) mustRun(args ...string) string {
	e.t.Helper()
	out, err := e.run(args...)
	if err != nil {
		e.t.Fatalf("rsync %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func randomBytes(t *testing.T, n int) []byte {
	t.Helper()
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

// textBytes returns compressible data.
func textBytes(n int, seed string) []byte {
	var b bytes.Buffer
	for i := 0; b.Len() < n; i++ {
		fmt.Fprintf(&b, "%s line %d: the quick brown fox jumps over the lazy dog\n", seed, i)
	}
	return b.Bytes()[:n]
}

// tree returns the regular files below root with their contents.
func tree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.Type().IsRegular() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			out[filepath.ToSlash(rel)] = string(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func assertSameTree(t *testing.T, want, got string) {
	t.Helper()
	a, b := tree(t, want), tree(t, got)
	var names []string
	for k := range a {
		names = append(names, k)
	}
	for k := range b {
		if _, ok := a[k]; !ok {
			names = append(names, k)
		}
	}
	slices.Sort(names)
	for _, n := range slices.Compact(names) {
		av, aok := a[n]
		bv, bok := b[n]
		switch {
		case !aok:
			t.Errorf("unexpected file %s", n)
		case !bok:
			t.Errorf("missing file %s", n)
		case av != bv:
			t.Errorf("file %s differs (%d bytes, want %d)", n, len(bv), len(av))
		}
	}
}

func populate(t *testing.T, root string) {
	t.Helper()
	writeFile(t, filepath.Join(root, "index.html"), textBytes(3000, "index"))
	writeFile(t, filepath.Join(root, "empty.txt"), nil)
	writeFile(t, filepath.Join(root, "css/site.css"), textBytes(800, "css"))
	writeFile(t, filepath.Join(root, "img/logo.bin"), randomBytes(t, 200_000))
	writeFile(t, filepath.Join(root, "a/b/c/deep.txt"), textBytes(50, "deep"))
	writeFile(t, filepath.Join(root, "a.b"), []byte("dot file next to dir a"))
	writeFile(t, filepath.Join(root, "a-b"), []byte("dash file next to dir a"))
	writeFile(t, filepath.Join(root, "big.dat"), randomBytes(t, 3_000_000))
}

var protocols = []string{"27", "28", "29", "30", "31", ""}

func protoArgs(p string) []string {
	if p == "" {
		return nil
	}
	return []string{"--protocol=" + p}
}

func TestRsyncUpload(t *testing.T) {
	for _, proto := range protocols {
		for _, compress := range [][]string{nil, {"-z"}, {"--compress-choice=zlib"}, {"--compress-choice=zlibx"}} {
			name := fmt.Sprintf("proto=%s/%s", proto, strings.Join(compress, ""))
			t.Run(name, func(t *testing.T) {
				if proto != "" && proto < "30" && len(compress) > 0 && compress[0] != "-z" {
					t.Skip("compression choice needs protocol 30")
				}
				e := newRsyncEnv(t)
				populate(t, e.local)
				args := append(protoArgs(proto), compress...)
				e.mustRun(append(args, "-rt", e.local+"/", "host:site")...)
				assertSameTree(t, e.local, filepath.Join(e.remote, "site"))

				// Change part of a big file and upload again: only the
				// changed region should cross the wire.
				big := filepath.Join(e.local, "big.dat")
				data, _ := os.ReadFile(big)
				copy(data[1_000_000:], randomBytes(t, 5000))
				data = append(data[:2_000_000], append(randomBytes(t, 1234), data[2_000_000:]...)...)
				writeFile(t, big, data)
				writeFile(t, filepath.Join(e.local, "css/site.css"), textBytes(900, "css2"))
				out := e.mustRun(append(args, "-rt", "--stats", e.local+"/", "host:site")...)
				assertSameTree(t, e.local, filepath.Join(e.remote, "site"))
				literal := statValue(t, out, "Literal data")
				if literal > 100_000 {
					t.Errorf("literal data %d, want a delta transfer\n%s", literal, out)
				}
				if matched := statValue(t, out, "Matched data"); matched < 2_000_000 {
					t.Errorf("matched data %d, want most of big.dat\n%s", matched, out)
				}
			})
		}
	}
}

func TestRsyncDownload(t *testing.T) {
	for _, proto := range protocols {
		for _, compress := range [][]string{nil, {"-z"}, {"--compress-choice=zlib"}} {
			name := fmt.Sprintf("proto=%s/%s", proto, strings.Join(compress, ""))
			t.Run(name, func(t *testing.T) {
				if proto != "" && proto < "30" && len(compress) > 0 && compress[0] != "-z" {
					t.Skip("compression choice needs protocol 30")
				}
				e := newRsyncEnv(t)
				site := filepath.Join(e.remote, "site")
				populate(t, site)
				args := append(protoArgs(proto), compress...)
				e.mustRun(append(args, "-rt", "host:site/", e.local+"/")...)
				assertSameTree(t, site, e.local)

				// Change the server's copy and download again.
				big := filepath.Join(site, "big.dat")
				data, _ := os.ReadFile(big)
				copy(data[500_000:], randomBytes(t, 3000))
				writeFile(t, big, data)
				// rsync compares whole seconds, so make the change visible.
				later := time.Now().Add(time.Minute)
				if err := os.Chtimes(big, later, later); err != nil {
					t.Fatal(err)
				}
				out := e.mustRun(append(args, "-rt", "--stats", "host:site/", e.local+"/")...)
				assertSameTree(t, site, e.local)
				if literal := statValue(t, out, "Literal data"); literal > 100_000 {
					t.Errorf("literal data %d, want a delta transfer\n%s", literal, out)
				}
			})
		}
	}
}

func TestRsyncChecksumChoices(t *testing.T) {
	for _, choice := range []string{"md4", "md5", "xxh64", "md5,md4"} {
		t.Run(choice, func(t *testing.T) {
			e := newRsyncEnv(t)
			populate(t, e.local)
			e.mustRun("-r", "--checksum-choice="+choice, e.local+"/", "host:site")
			assertSameTree(t, e.local, filepath.Join(e.remote, "site"))
			e.mustRun("-r", "-c", "--checksum-choice="+choice, "host:site/", t.TempDir())
		})
	}
}

func TestRsyncAlwaysChecksum(t *testing.T) {
	for _, proto := range protocols {
		t.Run("proto="+proto, func(t *testing.T) {
			e := newRsyncEnv(t)
			populate(t, e.local)
			args := protoArgs(proto)
			e.mustRun(append(args, "-r", e.local+"/", "host:site")...)

			// Same size and time, different content: only -c notices.
			p := filepath.Join(e.local, "css/site.css")
			st, _ := os.Stat(filepath.Join(e.remote, "site/css/site.css"))
			data, _ := os.ReadFile(p)
			data[10] ^= 0xff
			writeFile(t, p, data)
			_ = os.Chtimes(p, st.ModTime(), st.ModTime())

			out := e.mustRun(append(args, "-rv", e.local+"/", "host:site")...)
			if strings.Contains(out, "css/site.css") {
				t.Fatalf("quick check should have skipped the file\n%s", out)
			}
			out = e.mustRun(append(args, "-rvc", e.local+"/", "host:site")...)
			if !strings.Contains(out, "css/site.css") {
				t.Fatalf("checksum run did not transfer the file\n%s", out)
			}
			assertSameTree(t, e.local, filepath.Join(e.remote, "site"))

			// And the other direction.
			dl := t.TempDir()
			e.mustRun(append(args, "-rt", "host:site/", dl+"/")...)
			data, _ = os.ReadFile(filepath.Join(dl, "index.html"))
			st, _ = os.Stat(filepath.Join(dl, "index.html"))
			data[0] ^= 0xff
			writeFile(t, filepath.Join(dl, "index.html"), data)
			_ = os.Chtimes(filepath.Join(dl, "index.html"), st.ModTime(), st.ModTime())
			e.mustRun(append(args, "-rtc", "host:site/", dl+"/")...)
			assertSameTree(t, filepath.Join(e.remote, "site"), dl)
		})
	}
}

func TestRsyncDelete(t *testing.T) {
	for _, proto := range protocols {
		for _, when := range []string{"--delete", "--delete-before", "--delete-during", "--delete-after", "--delete-delay"} {
			t.Run(fmt.Sprintf("proto=%s/%s", proto, when), func(t *testing.T) {
				if proto != "" && proto < "30" && when == "--delete-delay" {
					t.Skip("needs protocol 30")
				}
				e := newRsyncEnv(t)
				populate(t, e.local)
				args := protoArgs(proto)
				e.mustRun(append(args, "-r", e.local+"/", "host:site")...)

				writeFile(t, filepath.Join(e.remote, "site/stale.txt"), []byte("old"))
				writeFile(t, filepath.Join(e.remote, "site/old/dir/file.txt"), []byte("old"))
				writeFile(t, filepath.Join(e.remote, "site/keep.log"), []byte("protected"))
				if err := os.RemoveAll(filepath.Join(e.local, "a")); err != nil {
					t.Fatal(err)
				}

				out := e.mustRun(append(args, "-rv", when, "--exclude=*.log", e.local+"/", "host:site")...)
				if !strings.Contains(out, "deleting stale.txt") {
					t.Errorf("missing deletion message\n%s", out)
				}
				remote := tree(t, filepath.Join(e.remote, "site"))
				if _, ok := remote["keep.log"]; !ok {
					t.Error("excluded file was deleted")
				}
				delete(remote, "keep.log")
				if err := os.Remove(filepath.Join(e.remote, "site/keep.log")); err != nil {
					t.Fatal(err)
				}
				assertSameTree(t, e.local, filepath.Join(e.remote, "site"))
				if _, err := os.Stat(filepath.Join(e.remote, "site/old")); !os.IsNotExist(err) {
					t.Errorf("stale directory survived: %v", err)
				}
			})
		}
	}
}

func TestRsyncDownloadFilters(t *testing.T) {
	e := newRsyncEnv(t)
	site := filepath.Join(e.remote, "site")
	populate(t, site)
	e.mustRun("-r", "--exclude=*.bin", "--exclude=/a/", "host:site/", e.local+"/")
	got := tree(t, e.local)
	if _, ok := got["img/logo.bin"]; ok {
		t.Error("excluded file was sent")
	}
	if _, ok := got["a/b/c/deep.txt"]; ok {
		t.Error("excluded directory was sent")
	}
	if _, ok := got["index.html"]; !ok {
		t.Error("index.html missing")
	}
}

func TestRsyncSingleFile(t *testing.T) {
	e := newRsyncEnv(t)
	writeFile(t, filepath.Join(e.local, "one.txt"), []byte("hello"))

	e.mustRun(filepath.Join(e.local, "one.txt"), "host:site/renamed.txt")
	if b, err := os.ReadFile(filepath.Join(e.remote, "site/renamed.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("single file upload: %q %v", b, err)
	}
	e.mustRun(filepath.Join(e.local, "one.txt"), "host:site/")
	if b, err := os.ReadFile(filepath.Join(e.remote, "site/one.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("upload into dir: %q %v", b, err)
	}

	dl := t.TempDir()
	e.mustRun("host:site/renamed.txt", dl+"/")
	if b, err := os.ReadFile(filepath.Join(dl, "renamed.txt")); err != nil || string(b) != "hello" {
		t.Fatalf("single file download: %q %v", b, err)
	}
}

func TestRsyncListOnly(t *testing.T) {
	e := newRsyncEnv(t)
	populate(t, filepath.Join(e.remote, "site"))
	out := e.mustRun("host:site/")
	for _, want := range []string{"index.html", "css", "big.dat"} {
		if !strings.Contains(out, want) {
			t.Errorf("listing lacks %s\n%s", want, out)
		}
	}
	if strings.Contains(out, "site.css") {
		t.Errorf("non-recursive listing descended\n%s", out)
	}
	out = e.mustRun("-r", "host:site/")
	if !strings.Contains(out, "css/site.css") {
		t.Errorf("recursive listing lacks css/site.css\n%s", out)
	}
}

func TestRsyncDryRun(t *testing.T) {
	e := newRsyncEnv(t)
	populate(t, e.local)
	out := e.mustRun("-rvn", e.local+"/", "host:site")
	if !strings.Contains(out, "big.dat") {
		t.Errorf("dry run did not list files\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(e.remote, "site")); !os.IsNotExist(err) {
		t.Errorf("dry run wrote files: %v", err)
	}
}

func TestRsyncMissingSource(t *testing.T) {
	e := newRsyncEnv(t)
	out, err := e.run("-r", "host:nope/", e.local+"/")
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != exitPartial {
		t.Fatalf("want exit code %d, got %v\n%s", exitPartial, err, out)
	}
	if !strings.Contains(out, "No such file or directory") {
		t.Errorf("missing error message\n%s", out)
	}
}

func TestRsyncUnchangedSkipped(t *testing.T) {
	e := newRsyncEnv(t)
	populate(t, e.local)
	e.mustRun("-rt", e.local+"/", "host:site")
	out := e.mustRun("-rt", "--stats", e.local+"/", "host:site")
	if n := statValue(t, out, "Number of regular files transferred"); n != 0 {
		t.Errorf("transferred %d files, want 0\n%s", n, out)
	}
}

func statValue(t *testing.T, out, label string) int64 {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		rest, ok := strings.CutPrefix(line, label+": ")
		if !ok {
			continue
		}
		rest = strings.TrimSuffix(rest, " bytes")
		rest = strings.ReplaceAll(rest, ",", "")
		var v int64
		if _, err := fmt.Sscan(rest, &v); err == nil {
			return v
		}
	}
	t.Fatalf("no %q in output\n%s", label, out)
	return 0
}

func TestRsyncCompression(t *testing.T) {
	type tc struct {
		proto string
		args  []string
	}
	cases := []tc{
		{"27", []string{"-z"}},
		{"29", []string{"-z"}},
		{"30", []string{"-z"}},
		{"30", []string{"--compress-choice=zlib"}},
		{"31", []string{"--compress-choice=zlib"}},
		{"", []string{"-z"}},
		{"", []string{"--compress-choice=zlibx"}},
		{"", []string{"-z", "--compress-level=1"}},
		{"", []string{"-z", "--compress-level=9"}},
	}
	for _, c := range cases {
		for _, block := range []string{"", "--block-size=100000"} {
			t.Run(fmt.Sprintf("proto=%s/%s/%s", c.proto, strings.Join(c.args, ""), block), func(t *testing.T) {
				e := newRsyncEnv(t)
				text := textBytes(2_000_000, "compressible")
				writeFile(t, filepath.Join(e.local, "text.txt"), text)
				args := append(protoArgs(c.proto), c.args...)
				if block != "" {
					args = append(args, block)
				}

				out := e.mustRun(append(args, "-rt", "--stats", e.local+"/", "host:site")...)
				assertSameTree(t, e.local, filepath.Join(e.remote, "site"))
				if sent := statValue(t, out, "Total bytes sent"); sent > 500_000 {
					t.Errorf("upload sent %d bytes, compression is not in effect\n%s", sent, out)
				}

				// A delta with matched blocks exercises the deflate history.
				text = append(textBytes(150_000, "prefix"), text...)
				text = append(text, textBytes(70_000, "suffix")...)
				writeFile(t, filepath.Join(e.local, "text.txt"), text)
				out = e.mustRun(append(args, "-rt", "--stats", e.local+"/", "host:site")...)
				assertSameTree(t, e.local, filepath.Join(e.remote, "site"))
				if matched := statValue(t, out, "Matched data"); matched < 1_000_000 {
					t.Errorf("matched %d bytes, want a delta\n%s", matched, out)
				}

				dl := t.TempDir()
				writeFile(t, filepath.Join(dl, "text.txt"), textBytes(2_000_000, "compressible"))
				old := time.Now().Add(-time.Hour)
				_ = os.Chtimes(filepath.Join(dl, "text.txt"), old, old)
				out = e.mustRun(append(args, "-rt", "--stats", "host:site/", dl+"/")...)
				assertSameTree(t, filepath.Join(e.remote, "site"), dl)
				if recv := statValue(t, out, "Total bytes received"); recv > 300_000 {
					t.Errorf("download received %d bytes, want a compressed delta\n%s", recv, out)
				}
				if matched := statValue(t, out, "Matched data"); matched < 1_000_000 {
					t.Errorf("download matched %d bytes, want a delta\n%s", matched, out)
				}
			})
		}
	}
}

func TestRsyncArchiveMode(t *testing.T) {
	for _, proto := range protocols {
		t.Run("proto="+proto, func(t *testing.T) {
			e := newRsyncEnv(t)
			populate(t, e.local)
			if err := os.Symlink("index.html", filepath.Join(e.local, "link.html")); err != nil {
				t.Fatal(err)
			}
			args := protoArgs(proto)
			out := e.mustRun(append(args, "-av", e.local+"/", "host:site")...)
			if !strings.Contains(out, `skipping non-regular file "link.html"`) {
				t.Errorf("symlink not reported as skipped\n%s", out)
			}
			_ = os.Remove(filepath.Join(e.local, "link.html"))
			assertSameTree(t, e.local, filepath.Join(e.remote, "site"))

			dl := t.TempDir()
			e.mustRun(append(args, "-a", "host:site/", dl+"/")...)
			assertSameTree(t, e.local, dl)
		})
	}
}

func TestRsyncTransferOptions(t *testing.T) {
	cases := [][]string{
		{"-s"},
		{"-W"},
		{"--inplace"},
		{"-u"},
		{"--size-only"},
		{"-I"},
		{"--block-size=2048"},
		{"--checksum-seed=12345"},
		{"--numeric-ids", "-og"},
		{"-U"},
	}
	for _, opts := range cases {
		t.Run(strings.Join(opts, " "), func(t *testing.T) {
			e := newRsyncEnv(t)
			populate(t, e.local)
			e.mustRun(append(slices.Clone(opts), "-rt", e.local+"/", "host:site")...)
			assertSameTree(t, e.local, filepath.Join(e.remote, "site"))

			big := filepath.Join(e.remote, "site/big.dat")
			data, _ := os.ReadFile(big)
			copy(data[100_000:], randomBytes(t, 4000))
			copy(data[2_500_000:], data[10_000:20_000])
			writeFile(t, big, data)
			later := time.Now().Add(time.Minute)
			_ = os.Chtimes(big, later, later)

			dl := t.TempDir()
			e.mustRun(append(slices.Clone(opts), "-rt", e.local+"/", dl+"/")...)
			e.mustRun(append(slices.Clone(opts), "-rt", "host:site/", dl+"/")...)
			if opts[0] != "--size-only" {
				assertSameTree(t, filepath.Join(e.remote, "site"), dl)
			}
		})
	}
}

func TestRsyncSkipOptions(t *testing.T) {
	e := newRsyncEnv(t)
	writeFile(t, filepath.Join(e.local, "new.txt"), []byte("new"))
	writeFile(t, filepath.Join(e.local, "old.txt"), []byte("changed locally"))
	writeFile(t, filepath.Join(e.local, "huge.txt"), textBytes(50_000, "huge"))
	writeFile(t, filepath.Join(e.remote, "site/old.txt"), []byte("remote"))

	e.mustRun("-r", "--existing", e.local+"/", "host:site")
	got := tree(t, filepath.Join(e.remote, "site"))
	if _, ok := got["new.txt"]; ok || got["old.txt"] != "changed locally" {
		t.Errorf("--existing: %v", got)
	}

	writeFile(t, filepath.Join(e.remote, "site/old.txt"), []byte("remote"))
	e.mustRun("-r", "--ignore-existing", "--max-size=10k", e.local+"/", "host:site")
	got = tree(t, filepath.Join(e.remote, "site"))
	if got["old.txt"] != "remote" || got["new.txt"] != "new" {
		t.Errorf("--ignore-existing: %v", got)
	}
	if _, ok := got["huge.txt"]; ok {
		t.Error("--max-size let a large file through")
	}
}

func TestRsyncPutResults(t *testing.T) {
	for _, proto := range protocols {
		for _, result := range []string{"ignored", "failed"} {
			t.Run("protocol_"+proto+"/"+result, func(t *testing.T) {
				t.Setenv("RSYNC_TEST_PUT_RESULT", result)
				e := newRsyncEnv(t)
				for _, name := range []string{"a.txt", "skip.txt", "z.txt"} {
					writeFile(t, filepath.Join(e.local, name), textBytes(100_000, name))
				}
				writeFile(t, filepath.Join(e.remote, "site/skip.txt"), []byte("original"))
				out, err := e.run(append(protoArgs(proto), "-r", "--remove-source-files", e.local+"/", "host:site")...)
				if result == "ignored" {
					if err != nil {
						t.Fatalf("ignored file failed the batch: %v\n%s", err, out)
					}
				} else {
					var exit *exec.ExitError
					if !errors.As(err, &exit) || exit.ExitCode() != 23 {
						t.Fatalf("want exit 23, got %v\n%s", err, out)
					}
				}
				got := tree(t, filepath.Join(e.remote, "site"))
				if len(got) != 3 || got["skip.txt"] != "original" {
					t.Fatalf("rejected file changed: %v", got)
				}
				for _, name := range []string{"a.txt", "z.txt"} {
					if got[name] != string(textBytes(100_000, name)) {
						t.Errorf("accepted file %s not stored", name)
					}
					if _, err := os.Stat(filepath.Join(e.local, name)); !errors.Is(err, os.ErrNotExist) {
						t.Errorf("accepted file %s not removed from source: %v", name, err)
					}
				}
				if data, err := os.ReadFile(filepath.Join(e.local, "skip.txt")); err != nil || !bytes.Equal(data, textBytes(100_000, "skip.txt")) {
					t.Fatalf("rejected source file changed: %v", err)
				}
			})
		}
	}
}

// writeTree writes files, named relative to root.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, contents := range files {
		writeFile(t, filepath.Join(root, name), []byte(contents))
	}
}

// mergeFilterTree has per-directory merge files covering inheritance,
// overrides, anchoring, clearing and protect rules.
var mergeFilterTree = map[string]string{
	".rsync-filter":              "- keep.log\nP precious/\n- *.tmp\n-p *.perish\n",
	"keep.log":                   "keep",
	"stale.txt":                  "stale",
	"x.tmp":                      "tmp",
	"precious/a.txt":             "precious",
	"sub/.rsync-filter":          "+ sub-only.tmp\n- /anchored.txt\n",
	"sub/a.tmp":                  "tmp",
	"sub/sub-only.tmp":           "kept by include",
	"sub/anchored.txt":           "anchored",
	"sub/deeper/anchored.txt":    "not anchored here",
	"sub/deeper/keep.log":        "inherited",
	"cleared/.rsync-filter":      "!\n- only.txt\n",
	"cleared/keep.log":           "no longer excluded",
	"cleared/only.txt":           "only",
	"gone/.rsync-filter":         "- inside.log\n",
	"gone/inside.log":            "protects its directory",
	"gone/other.txt":             "other",
	".cvsignore":                 "precious.dat\n",
	"precious.dat":               "cvs",
	"obj.o":                      "default cvs pattern",
	"nested/.cvsignore":          "nested.dat",
	"nested/nested.dat":          "nested",
	"nested/deeper/nested.dat":   "cvsignore does not inherit",
	"nested/deeper/precious.dat": "nor does the parent's",
	"top.perish":                 "perishable rules protect a kept directory's entries",
	"perish/keep.perish":         "but not a deleted directory's",
	"perish/deeper/keep.perish":  "at any depth",
	"objdir/x.o":                 "default CVS patterns are perishable from protocol 30",
}

// TestRsyncMergeFiltersMatchRsync runs the same transfer against real rsync
// and against our server, each from a copy of mergeFilterTree, and expects
// the same result.
func TestRsyncMergeFiltersMatchRsync(t *testing.T) {
	local := map[string]string{"index.html": "new", "sub/new.txt": "new"}
	pushes := [][]string{
		{"-F", "--delete"},
		{"-F", "--delete-before"},
		{"-F", "--delete-during"},
		{"-F", "--delete-after"},
		{"-F", "--delete-delay"},
		{"-FF", "--delete"},
		{"-F", "--delete", "--delete-excluded"},
		{"-C", "--delete"},
		{"-CF", "--delete"},
		{"--filter=:e .rsync-filter", "--delete"},
		{"--filter=:n- .rsync-filter", "--delete"},
		{"--filter=dir-merge,- .rsync-filter", "--delete"},
	}
	for _, proto := range []string{"29", "30", "31", ""} {
		for _, flags := range pushes {
			t.Run(fmt.Sprintf("push/proto=%s/%s", proto, strings.Join(flags, " ")), func(t *testing.T) {
				if proto == "29" && slices.Contains(flags, "--delete-delay") {
					t.Skip("needs protocol 30")
				}
				e := newRsyncEnv(t)
				writeTree(t, e.local, local)
				ref := filepath.Join(t.TempDir(), "site")
				writeTree(t, ref, mergeFilterTree)
				writeTree(t, filepath.Join(e.remote, "site"), mergeFilterTree)
				args := append(protoArgs(proto), "-r")
				args = append(args, flags...)

				refCmd := exec.Command("rsync", append(slices.Clone(args), e.local+"/", ref)...)
				if out, err := refCmd.CombinedOutput(); err != nil {
					t.Fatalf("reference rsync: %v\n%s", err, out)
				}
				e.mustRun(append(args, e.local+"/", "host:site")...)
				if slices.Contains(flags, "--delete-delay") {
					// rsync drops inherited per-directory rules before its
					// delayed deletions, so it removes this file. We keep it.
					inherited := filepath.Join(e.remote, "site/sub/deeper/keep.log")
					if err := os.Remove(inherited); err != nil {
						t.Fatalf("file protected by an inherited rule was deleted: %v", err)
					}
				}
				assertSameTree(t, ref, filepath.Join(e.remote, "site"))
			})
		}
	}

	pulls := [][]string{{"-F"}, {"-FF"}, {"-C"}, {"--filter=:n .rsync-filter"}}
	for _, proto := range []string{"29", "30", "31", ""} {
		for _, flags := range pulls {
			for _, src := range []string{"site/", "site"} {
				t.Run(fmt.Sprintf("pull/proto=%s/%s/%s", proto, strings.Join(flags, " "), src), func(t *testing.T) {
					e := newRsyncEnv(t)
					writeTree(t, filepath.Join(e.remote, "site"), mergeFilterTree)
					refSrc := t.TempDir()
					writeTree(t, filepath.Join(refSrc, "site"), mergeFilterTree)
					ref := t.TempDir()
					args := append(protoArgs(proto), "-r")
					args = append(args, flags...)

					refCmd := exec.Command("rsync", append(slices.Clone(args), filepath.Join(refSrc, src), ref+"/")...)
					if strings.HasSuffix(src, "/") {
						refCmd.Args[len(refCmd.Args)-2] += "/"
					}
					if out, err := refCmd.CombinedOutput(); err != nil {
						t.Fatalf("reference rsync: %v\n%s", err, out)
					}
					e.mustRun(append(args, "host:"+src, e.local+"/")...)
					assertSameTree(t, ref, e.local)
				})
			}
		}
	}
}

// TestRsyncMergeFilterParentScan checks that "-F" reads .rsync-filter in
// the directories above the transfer, as rsync does up to "/".
func TestRsyncMergeFilterParentScan(t *testing.T) {
	e := newRsyncEnv(t)
	writeFile(t, filepath.Join(e.local, "index.html"), []byte("new"))
	writeTree(t, e.remote, map[string]string{
		".rsync-filter":     "- /site/scanned.txt\n",
		"site/scanned.txt":  "protected from above",
		"site/stale.txt":    "stale",
		"other/scanned.txt": "outside the transfer",
	})
	e.mustRun("-r", "-F", "--delete", e.local+"/", "host:site")
	got := tree(t, filepath.Join(e.remote, "site"))
	if _, ok := got["scanned.txt"]; !ok {
		t.Error("rule from the parent directory's merge file was ignored")
	}
	if _, ok := got["stale.txt"]; ok {
		t.Error("unprotected file survived --delete")
	}
}

// TestRsyncMergeFilterErrors checks that a merge file we can't use stops
// deletions on upload and the whole transfer on download.
func TestRsyncMergeFilterErrors(t *testing.T) {
	for _, contents := range []string{"merge other\n", "bogus rule\n"} {
		t.Run("push/"+contents, func(t *testing.T) {
			e := newRsyncEnv(t)
			writeFile(t, filepath.Join(e.local, "index.html"), []byte("new"))
			writeTree(t, filepath.Join(e.remote, "site"), map[string]string{
				"sub/.rsync-filter": contents,
				"sub/stale.txt":     "stale",
				"stale.txt":         "stale",
			})
			out, err := e.run("-r", "-F", "--delete-after", e.local+"/", "host:site")
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != exitPartial {
				t.Fatalf("want exit %d, got %v\n%s", exitPartial, err, out)
			}
			if !strings.Contains(out, "skipping file deletion") {
				t.Errorf("no explanation for skipped deletion\n%s", out)
			}
			got := tree(t, filepath.Join(e.remote, "site"))
			if got["index.html"] != "new" {
				t.Error("file was not transferred")
			}
			for _, name := range []string{"stale.txt", "sub/stale.txt"} {
				if _, ok := got[name]; !ok {
					t.Errorf("%s was deleted despite the unusable merge file", name)
				}
			}
		})
		t.Run("pull/"+contents, func(t *testing.T) {
			e := newRsyncEnv(t)
			writeTree(t, filepath.Join(e.remote, "site"), map[string]string{
				"sub/.rsync-filter": contents,
				"index.html":        "index",
			})
			out, err := e.run("-r", "-F", "host:site/", e.local+"/")
			if err == nil || !strings.Contains(out, "per-directory filter file") {
				t.Fatalf("want a merge-file error, got %v\n%s", err, out)
			}
			if got := tree(t, e.local); len(got) != 0 {
				t.Errorf("files transferred despite the error: %v", got)
			}
		})
	}
}

func TestRsyncUnsupportedOptions(t *testing.T) {
	e := newRsyncEnv(t)
	populate(t, e.local)
	for _, opt := range []string{"-H", "-A", "-X"} {
		out, err := e.run("-r", opt, e.local+"/", "host:site")
		if err == nil {
			t.Errorf("%s: want an error\n%s", opt, out)
		}
		if !strings.Contains(out, "not supported") {
			t.Errorf("%s: missing explanation\n%s", opt, out)
		}
	}
	out, err := e.run("-rR", "host:site/./css", t.TempDir())
	if err == nil {
		t.Errorf("-R download: want an error\n%s", out)
	}
	if !strings.Contains(out, "not supported when downloading") {
		t.Errorf("-R download: missing explanation\n%s", out)
	}
}

func TestRsyncUploadRelative(t *testing.T) {
	cases := []struct {
		name string
		args func(local, list string) []string
	}{
		{"files-from", func(local, list string) []string {
			return []string{"-t", "--files-from=" + list, local + "/"}
		}},
		{"files-from-delete", func(local, list string) []string {
			return []string{"-rt", "--delete", "--files-from=" + list, local + "/"}
		}},
		{"dot-marker", func(local, list string) []string {
			return []string{"-tR", local + "/./a/b/c/deep.txt"}
		}},
		{"dot-marker-dir-delete", func(local, list string) []string {
			return []string{"-rtR", "--delete", local + "/./a/b"}
		}},
	}
	for _, proto := range protocols {
		for _, tc := range cases {
			t.Run(fmt.Sprintf("proto=%s/%s", proto, tc.name), func(t *testing.T) {
				e := newRsyncEnv(t)
				populate(t, e.local)
				list := filepath.Join(t.TempDir(), "files")
				writeFile(t, list, []byte("index.html\na/b/c/deep.txt\n"))

				// Seed both sides with files outside the list so a
				// deletion that reaches too far shows up.
				ref := t.TempDir()
				populate(t, ref)
				writeFile(t, filepath.Join(ref, "a/b/stale.txt"), []byte("stale"))
				writeFile(t, filepath.Join(ref, "a/b/c/stale.txt"), []byte("stale"))
				writeFile(t, filepath.Join(ref, "a/other.txt"), []byte("other"))
				e.mustRun("-rt", ref+"/", "host:site")

				args := append(protoArgs(proto), tc.args(e.local, list)...)
				refCmd := exec.Command("rsync", append(slices.Clone(args), ref)...)
				if out, err := refCmd.CombinedOutput(); err != nil {
					t.Fatalf("reference rsync: %v\n%s", err, out)
				}
				e.mustRun(append(args, "host:site")...)
				assertSameTree(t, ref, filepath.Join(e.remote, "site"))
			})
		}
	}
}
