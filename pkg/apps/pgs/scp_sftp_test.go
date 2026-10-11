package pgs

import (
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pkg/sftp"
)

// uploadModes upload the contents of a local directory to /site. OpenSSH's
// scp uses SFTP unless -O asks for the legacy protocol.
var uploadModes = map[string]func(e *rsyncEnv, dir string) *exec.Cmd{
	"scp-legacy": func(e *rsyncEnv, dir string) *exec.Cmd {
		return exec.Command("scp", append(append([]string{"-O", "-r"}, e.sshArgs...), dir+"/.", "localhost:/site")...)
	},
	"scp-sftp": func(e *rsyncEnv, dir string) *exec.Cmd {
		return exec.Command("scp", append(append([]string{"-r"}, e.sshArgs...), dir+"/.", "localhost:/site")...)
	},
	"sftp": func(e *rsyncEnv, dir string) *exec.Cmd {
		return e.sftp("-mkdir /site", "put -r "+dir+"/. /site")
	},
}

func (e *rsyncEnv) sftp(cmds ...string) *exec.Cmd {
	cmd := exec.Command("sftp", append(append([]string{"-b", "-"}, e.sshArgs...), "localhost")...)
	cmd.Stdin = strings.NewReader(strings.Join(cmds, "\n") + "\n")
	return cmd
}

func runCmd(t *testing.T, cmd *exec.Cmd) (string, int) {
	t.Helper()
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		return string(out), exit.ExitCode()
	case err != nil:
		t.Fatalf("%s: %v", cmd.Path, err)
	}
	return string(out), 0
}

func (e *rsyncEnv) assertStored(files map[string]string) {
	e.t.Helper()
	for name, want := range files {
		if got, _, ok := e.stored("/site/" + name); !ok || string(got) != want {
			e.t.Errorf("%s stored=%v contents=%q, want %q", name, ok, got, want)
		}
	}
}

// waitStored waits for files to be stored. sshfs closes a file after the
// program writing it has exited, and the file is stored on close.
func (e *rsyncEnv) waitStored(files map[string]string) {
	e.t.Helper()
	for range 50 {
		done := true
		for name, want := range files {
			if got, _, ok := e.stored("/site/" + name); !ok || string(got) != want {
				done = false
			}
		}
		if done {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.assertStored(files)
}

func TestScpSftpUpload(t *testing.T) {
	newStorage := rsyncStorageBackends()["memory"]
	mtime := time.Unix(1_600_000_000, 0)
	for mode, upload := range uploadModes {
		t.Run(mode, func(t *testing.T) {
			t.Run("tree and overwrite", func(t *testing.T) {
				env := newRsyncEnv(t, newStorage(t))
				src := tree{t, t.TempDir()}
				files := map[string]string{"index.html": "index", "css/site.css": "css", "img/a/b.png": "png"}
				for name, contents := range files {
					src.write(name, []byte(contents), mtime)
				}
				if out, code := runCmd(t, upload(env, src.dir)); code != 0 {
					t.Fatalf("exit %d\n%s", code, out)
				}
				env.assertStored(files)

				files["index.html"] = "changed"
				src.write("index.html", []byte("changed"), mtime)
				if out, code := runCmd(t, upload(env, src.dir)); code != 0 {
					t.Fatalf("exit %d\n%s", code, out)
				}
				env.assertStored(files)
			})

			t.Run("rejected file fails without stopping the rest", func(t *testing.T) {
				env := newRsyncEnv(t, newStorage(t))
				env.feature.Data.FileMax = 100
				src := tree{t, t.TempDir()}
				ok := map[string]string{"a1.txt": "ok", "c3.txt": "ok", "sub/d4.txt": "ok"}
				for name, contents := range ok {
					src.write(name, []byte(contents), mtime)
				}
				big := strings.Repeat("x", 200)
				src.write("b2.bin", []byte(big), mtime)
				out, code := runCmd(t, upload(env, src.dir))
				if code == 0 {
					t.Errorf("rejected upload exited 0\n%s", out)
				}
				if !strings.Contains(out, "stream size exceeded") {
					t.Errorf("no rejection message\n%s", out)
				}
				env.assertStored(ok)
				if got, _, _ := env.stored("/site/b2.bin"); string(got) == big {
					t.Error("oversized file was stored")
				}
			})

			t.Run("ignored dotfile", func(t *testing.T) {
				env := newRsyncEnv(t, newStorage(t))
				src := tree{t, t.TempDir()}
				ok := map[string]string{"a1.txt": "ok", "z9.txt": "ok"}
				for name, contents := range ok {
					src.write(name, []byte(contents), mtime)
				}
				src.write(".secret", []byte("dot"), mtime)
				out, code := runCmd(t, upload(env, src.dir))
				if !strings.Contains(out, "file ignored: (/site/.secret)") {
					t.Errorf("no ignore message\n%s", out)
				}
				if code != 0 {
					t.Errorf("ignored file failed the upload: exit %d\n%s", code, out)
				}
				env.assertStored(ok)
				if _, _, stored := env.stored("/site/.secret"); stored {
					t.Error("ignored file was stored")
				}
			})
		})
	}
}

func TestScpSftpDownloadAndCommands(t *testing.T) {
	env := newRsyncEnv(t, rsyncStorageBackends()["memory"](t))
	src := tree{t, t.TempDir()}
	files := map[string]string{"index.html": "index", "css/site.css": "css", "old.txt": "old"}
	for name, contents := range files {
		src.write(name, []byte(contents), time.Unix(1_600_000_000, 0))
	}
	if out, code := runCmd(t, uploadModes["sftp"](env, src.dir)); code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}

	t.Run("sftp get", func(t *testing.T) {
		dst := t.TempDir()
		if out, code := runCmd(t, env.sftp("get -r /site "+dst)); code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		for name, want := range files {
			if got, err := os.ReadFile(filepath.Join(dst, "site", name)); err != nil || string(got) != want {
				t.Errorf("%s: %q %v", name, got, err)
			}
		}
	})

	t.Run("scp download over sftp", func(t *testing.T) {
		dst := t.TempDir()
		cmd := exec.Command("scp", append(append([]string{"-r"}, env.sshArgs...), "localhost:/site/css", dst)...)
		if out, code := runCmd(t, cmd); code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		if got, err := os.ReadFile(filepath.Join(dst, "css/site.css")); err != nil || string(got) != "css" {
			t.Errorf("site.css: %q %v", got, err)
		}
	})

	t.Run("legacy scp download is refused", func(t *testing.T) {
		cmd := exec.Command("scp", append(append([]string{"-O"}, env.sshArgs...), "localhost:/site/index.html", t.TempDir())...)
		out, code := runCmd(t, cmd)
		if code == 0 || !strings.Contains(out, "unsupported, use rsync or sftp") {
			t.Fatalf("exit %d\n%s", code, out)
		}
	})

	t.Run("sftp ls rm", func(t *testing.T) {
		out, code := runCmd(t, env.sftp("ls /site", "rm /site/old.txt"))
		if code != 0 {
			t.Fatalf("exit %d\n%s", code, out)
		}
		for _, name := range []string{"index.html", "old.txt", "css"} {
			if !strings.Contains(out, name) {
				t.Errorf("ls is missing %s\n%s", name, out)
			}
		}
		if _, _, ok := env.stored("/site/old.txt"); ok {
			t.Error("rm did not delete the file")
		}
		env.assertStored(map[string]string{"index.html": "index", "css/site.css": "css"})
	})
}

func TestSftpIgnoredFileKeepsBatchGoing(t *testing.T) {
	env := newRsyncEnv(t, rsyncStorageBackends()["memory"](t))
	src := tree{t, t.TempDir()}
	src.write(".secret", []byte("dot"), time.Unix(1_600_000_000, 0))
	src.write("after.txt", []byte("after"), time.Unix(1_600_000_000, 0))
	out, code := runCmd(t, env.sftp(
		"-mkdir /site",
		"put "+filepath.Join(src.dir, ".secret")+" /site/.secret",
		"put "+filepath.Join(src.dir, "after.txt")+" /site/after.txt",
	))
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	env.assertStored(map[string]string{"after.txt": "after"})
	if _, _, ok := env.stored("/site/.secret"); ok {
		t.Error("ignored file was stored")
	}
}

func TestSftpRejectedUploadKeepsStoredFile(t *testing.T) {
	for _, mode := range []string{"scp-sftp", "sftp"} {
		t.Run(mode, func(t *testing.T) {
			env := newRsyncEnv(t, rsyncStorageBackends()["memory"](t))
			src := tree{t, t.TempDir()}
			src.write("page.html", []byte("original"), time.Unix(1_600_000_000, 0))
			if out, code := runCmd(t, uploadModes[mode](env, src.dir)); code != 0 {
				t.Fatalf("exit %d\n%s", code, out)
			}
			env.feature.Data.FileMax = 10
			src.write("page.html", []byte(strings.Repeat("x", 100)), time.Unix(1_600_000_000, 0))
			src.write("new.html", []byte(strings.Repeat("y", 100)), time.Unix(1_600_000_000, 0))
			if out, code := runCmd(t, uploadModes[mode](env, src.dir)); code == 0 {
				t.Fatalf("oversized upload exited 0\n%s", out)
			}
			env.assertStored(map[string]string{"page.html": "original"})
			if _, _, ok := env.stored("/site/new.html"); ok {
				t.Error("rejected new file left an object in storage")
			}
		})
	}
}

// TestSftpOpenFiles drives the calls sshfs makes for touch and editors.
func TestSftpOpenFiles(t *testing.T) {
	env := newRsyncEnv(t, rsyncStorageBackends()["memory"](t))
	conn, err := env.user.NewClientAddr(env.addr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	client, err := sftp.NewClient(conn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = client.Close() }()
	_ = client.Mkdir("/site")

	put := func(name, contents string) {
		t.Helper()
		f, err := client.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("touch keeps an existing file", func(t *testing.T) {
		put("/site/a.txt", "hello")
		f, err := client.OpenFile("/site/a.txt", os.O_WRONLY|os.O_CREATE)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"a.txt": "hello"})
	})

	t.Run("touch creates an empty file", func(t *testing.T) {
		f, err := client.OpenFile("/site/empty.txt", os.O_WRONLY|os.O_CREATE)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Stat("/site/empty.txt"); err != nil {
			t.Errorf("open file not visible to stat: %v", err)
		}
		if _, _, ok := env.stored("/site/empty.txt"); ok {
			t.Error("file stored before close")
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"empty.txt": ""})
	})

	t.Run("open file is listed and readable", func(t *testing.T) {
		f, err := client.OpenFile("/site/draft.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("draft")); err != nil {
			t.Fatal(err)
		}
		infos, err := client.ReadDir("/site")
		if err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, info := range infos {
			if info.Name() == "draft.txt" && info.Size() == 5 {
				listed = true
			}
		}
		if !listed {
			t.Error("open file missing from the listing")
		}
		r, err := client.Open("/site/draft.txt")
		if err != nil {
			t.Fatal(err)
		}
		got, _ := io.ReadAll(r)
		_ = r.Close()
		if string(got) != "draft" {
			t.Errorf("read %q from the open file", got)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"draft.txt": "draft"})
	})

	t.Run("write at an offset keeps the rest", func(t *testing.T) {
		put("/site/b.txt", "hello world")
		f, err := client.OpenFile("/site/b.txt", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("WORLD"), 6); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"b.txt": "hello WORLD"})
	})

	t.Run("truncate keeps nothing", func(t *testing.T) {
		put("/site/c.txt", "old")
		f, err := client.OpenFile("/site/c.txt", os.O_WRONLY|os.O_TRUNC)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"c.txt": ""})
	})

	t.Run("rename", func(t *testing.T) {
		put("/site/r1.txt", "one")
		put("/site/r2.txt", "two")
		if err := client.Rename("/site/r1.txt", "/site/r2.txt"); err == nil {
			t.Error("rename replaced an existing file")
		}
		if err := client.PosixRename("/site/r1.txt", "/site/r2.txt"); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"r2.txt": "one"})
		if _, _, ok := env.stored("/site/r1.txt"); ok {
			t.Error("source survived the rename")
		}
		if err := client.Rename("/site/r2.txt", "/site/.r2"); err == nil {
			t.Error("rename to an ignored name succeeded")
		}
		env.assertStored(map[string]string{"r2.txt": "one"})

		f, err := client.OpenFile("/site/r2.txt", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("ONE"), 0); err != nil {
			t.Fatal(err)
		}
		if err := client.Rename("/site/r2.txt", "/site/r3.txt"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"r3.txt": "ONE"})
		if _, _, ok := env.stored("/site/r2.txt"); ok {
			t.Error("open file's old path is still stored after it closed")
		}
	})

	t.Run("truncate then write past the end leaves zeros", func(t *testing.T) {
		put("/site/z.txt", "secret")
		f, err := client.OpenFile("/site/z.txt", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.Truncate(0); err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("x"), 5); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"z.txt": "\x00\x00\x00\x00\x00x"})
	})

	t.Run("renaming an open file keeps the source until it is stored", func(t *testing.T) {
		openAndWrite := func(name, contents string) *sftp.File {
			t.Helper()
			f, err := client.OpenFile(name, os.O_WRONLY)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.WriteAt([]byte(contents), 0); err != nil {
				t.Fatal(err)
			}
			return f
		}

		put("/site/k.txt", "keep")
		f := openAndWrite("/site/k.txt", "KEEP")
		if err := client.PosixRename("/site/k.txt", "/site/.ignored"); err == nil {
			t.Error("renamed an open file to an ignored name")
		}
		_ = f.Close()
		env.assertStored(map[string]string{"k.txt": "KEEP"})

		put("/site/k.txt", "keep")
		f = openAndWrite("/site/k.txt", strings.Repeat("x", 200))
		if err := client.PosixRename("/site/k.txt", "/site/big.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Stat("/site/k.txt"); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("renamed source still visible: %v", err)
		}
		env.feature.Data.FileMax = 100
		if err := f.Close(); err == nil {
			t.Error("oversized close succeeded")
		}
		env.feature.Data.FileMax = rsyncFileMax
		env.assertStored(map[string]string{"k.txt": "keep"})
		if _, _, ok := env.stored("/site/big.txt"); ok {
			t.Error("rejected destination was stored")
		}

		f = openAndWrite("/site/k.txt", "NEW!")
		if err := client.PosixRename("/site/k.txt", "/site/moved.txt"); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"k.txt": "keep"})
		put("/site/k.txt", "replacement")
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"moved.txt": "NEW!", "k.txt": "replacement"})

		put("/site/k2.txt", "k2")
		f = openAndWrite("/site/k2.txt", "K2")
		if err := client.PosixRename("/site/k2.txt", "/site/moved2.txt"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"moved2.txt": "K2"})
		if _, _, ok := env.stored("/site/k2.txt"); ok {
			t.Error("source kept after the renamed file was stored")
		}
	})

	t.Run("renamed source replaced by another session survives", func(t *testing.T) {
		conn2, err := env.user.NewClientAddr(env.addr)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn2.Close() }()
		other, err := sftp.NewClient(conn2)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = other.Close() }()

		put("/site/s1.txt", "orig")
		f, err := client.OpenFile("/site/s1.txt", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("ORIG"), 0); err != nil {
			t.Fatal(err)
		}
		if err := client.PosixRename("/site/s1.txt", "/site/s2.txt"); err != nil {
			t.Fatal(err)
		}
		g, err := other.Create("/site/s1.txt")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := g.Write([]byte("from another session")); err != nil {
			t.Fatal(err)
		}
		if err := g.Close(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		env.assertStored(map[string]string{"s1.txt": "from another session", "s2.txt": "ORIG"})
	})

	t.Run("removing a file renamed over a stored one removes both", func(t *testing.T) {
		put("/site/old.txt", "old")
		put("/site/src.txt", "src")
		f, err := client.OpenFile("/site/src.txt", os.O_WRONLY)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteAt([]byte("SRC"), 0); err != nil {
			t.Fatal(err)
		}
		if err := client.PosixRename("/site/src.txt", "/site/old.txt"); err != nil {
			t.Fatal(err)
		}
		if err := client.Remove("/site/old.txt"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		for _, name := range []string{"old.txt", "src.txt"} {
			if _, _, ok := env.stored("/site/" + name); ok {
				t.Errorf("%s is still stored", name)
			}
		}
	})

	t.Run("removed while open is not stored", func(t *testing.T) {
		f, err := client.OpenFile("/site/swap.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte("swap")); err != nil {
			t.Fatal(err)
		}
		if err := client.Remove("/site/swap.txt"); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if _, _, ok := env.stored("/site/swap.txt"); ok {
			t.Error("file removed while open was stored")
		}
	})
}

// TestSshfs mounts pgs with sshfs and edits files the way shells and
// editors do.
func TestSshfs(t *testing.T) {
	if _, err := exec.LookPath("sshfs"); err != nil {
		t.Skip("sshfs not installed")
	}
	for name, newStorage := range rsyncStorageBackends() {
		t.Run(name, func(t *testing.T) { testSshfs(t, newRsyncEnv(t, newStorage(t))) })
	}
}

func testSshfs(t *testing.T, env *rsyncEnv) {
	mnt := t.TempDir()
	opts := []string{"-p", env.sshArgs[1], "-o", "IdentityFile=" + env.sshArgs[3], "-o", "IdentitiesOnly=yes",
		"-o", "StrictHostKeyChecking=no", "-o", "UserKnownHostsFile=/dev/null", "-o", "LogLevel=ERROR"}
	if out, err := exec.Command("sshfs", append(opts, "localhost:/", mnt)...).CombinedOutput(); err != nil {
		t.Skipf("sshfs mount failed: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("fusermount3", "-u", "-z", mnt).Run() })

	home := t.TempDir()
	steps := []struct {
		cmd    string
		fails  bool
		stored map[string]string
	}{
		{cmd: "mkdir site && echo hello > site/a.txt", stored: map[string]string{"a.txt": "hello\n"}},
		{cmd: "touch site/a.txt", stored: map[string]string{"a.txt": "hello\n"}},
		{cmd: "touch site/new.txt", stored: map[string]string{"new.txt": ""}},
		{cmd: "vim -n -es -c 'normal! ihi' -c wq site/vim.txt", stored: map[string]string{"vim.txt": "hi\n"}},
		{cmd: "vim -es -c '%s/hello/bye/' -c wq site/a.txt", stored: map[string]string{"a.txt": "bye\n"}},
		{cmd: "echo more >> site/a.txt", stored: map[string]string{"a.txt": "bye\nmore\n"}},
		{cmd: "printf abc > site/t.txt && truncate -s 1 site/t.txt", stored: map[string]string{"t.txt": "a"}},
		{cmd: "rm site/new.txt", stored: map[string]string{"a.txt": "bye\nmore\n"}},
		{cmd: "echo secret > site/.dot", fails: true},
		{cmd: "mv site/t.txt site/moved.txt", stored: map[string]string{"moved.txt": "a"}},
		{cmd: "mv site/moved.txt site/a.txt", stored: map[string]string{"a.txt": "a"}},
		{cmd: "mv site/a.txt site/.dot", fails: true, stored: map[string]string{"a.txt": "a"}},
		{cmd: "mkdir -p site/dir/sub site/empty && echo y > site/dir/sub/y.txt && mv site/dir site/dir2 && mv site/a.txt site/dir2/", stored: map[string]string{"dir2/sub/y.txt": "y\n", "dir2/a.txt": "a"}},
		{cmd: "mv site/empty site/empty2 && test -d site/empty2 && ! test -e site/empty && ! test -e site/dir"},
		{cmd: "echo q > site/q.txt && mv site/q.txt site/r.txt && cat site/r.txt", stored: map[string]string{"r.txt": "q\n"}},
	}
	for _, step := range steps {
		cmd := exec.Command("bash", "-c", step.cmd)
		cmd.Dir = mnt
		cmd.Env = append(os.Environ(), "HOME="+home)
		out, err := cmd.CombinedOutput()
		if (err != nil) != step.fails {
			t.Fatalf("%s: err=%v\n%s", step.cmd, err, out)
		}
		env.waitStored(step.stored)
	}
	for _, name := range []string{"new.txt", ".dot", "t.txt", "moved.txt", "a.txt", "dir/sub/y.txt", "q.txt"} {
		if _, _, ok := env.stored("/site/" + name); ok {
			t.Errorf("%s is stored", name)
		}
	}
}

func TestSingleFileIntoProject(t *testing.T) {
	newStorage := rsyncStorageBackends()["memory"]
	clients := map[string]func(e *rsyncEnv, src, dest string) *exec.Cmd{
		"scp-legacy": func(e *rsyncEnv, src, dest string) *exec.Cmd {
			return exec.Command("scp", append(append([]string{"-O"}, e.sshArgs...), src, "localhost:"+dest)...)
		},
		"scp-sftp": func(e *rsyncEnv, src, dest string) *exec.Cmd {
			return exec.Command("scp", append(e.sshArgs, src, "localhost:"+dest)...)
		},
		"sftp": func(e *rsyncEnv, src, dest string) *exec.Cmd {
			return e.sftp("put " + src + " " + dest)
		},
		"rsync": func(e *rsyncEnv, src, dest string) *exec.Cmd {
			return exec.Command("rsync", "-e", e.rsh, src, "localhost:"+dest)
		},
	}
	for name, client := range clients {
		for _, dest := range []string{"/site", "/site/"} {
			t.Run(name+" "+dest, func(t *testing.T) {
				env := newRsyncEnv(t, newStorage(t))
				src := tree{t, t.TempDir()}
				src.write("post.md", []byte("hello"), time.Now())
				out, code := runCmd(t, client(env, filepath.Join(src.dir, "post.md"), dest))
				if code != 0 {
					t.Fatalf("exit %d\n%s", code, out)
				}
				env.assertStored(map[string]string{"post.md": "hello"})
				if !strings.Contains(out, "https://testusr-site.pgs.sh/post.md ") {
					t.Errorf("wrong url\n%s", out)
				}
			})
		}
		t.Run(name+" /", func(t *testing.T) {
			env := newRsyncEnv(t, newStorage(t))
			src := tree{t, t.TempDir()}
			src.write("post.md", []byte("hello"), time.Now())
			out, code := runCmd(t, client(env, filepath.Join(src.dir, "post.md"), "/"))
			if code == 0 {
				t.Errorf("file at the root exited 0\n%s", out)
			}
			if !strings.Contains(out, "you must copy files to a non-root folder") {
				t.Errorf("no root error\n%s", out)
			}
		})
	}
}
