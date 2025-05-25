package pgs

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
	"github.com/pkg/sftp"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/ssh"
)

func TestSshServerSftp(t *testing.T) {
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}
	logger := slog.New(
		slog.NewTextHandler(os.Stdout, opts),
	)
	slog.SetDefault(logger)
	dbpool := pgsdb.NewDBMemory(logger)
	// setup test data
	dbpool.SetupTestData()
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		panic(err)
	}
	cfg := NewPgsConfig(logger, dbpool, st)
	done := make(chan error)
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	go StartSshServer(cfg, done)
	// Hack to wait for startup
	time.Sleep(time.Millisecond * 100)

	user := GenerateUser()
	// add user's pubkey to the default test account
	dbpool.Pubkeys = append(dbpool.Pubkeys, &db.PublicKey{
		ID:     "nice-pubkey",
		UserID: dbpool.Users[0].ID,
		Key:    utils.KeyForKeyText(user.signer.PublicKey()),
	})

	client, err := user.NewClient()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		_ = client.Close()
	}()

	_, err = WriteFileWithSftp(cfg, client)
	if err != nil {
		t.Error(err)
		return
	}

	_, err = WriteFilesMultProjectsWithSftp(cfg, client)
	if err != nil {
		t.Error(err)
		return
	}

	projects, err := dbpool.FindProjectsByUser(dbpool.Users[0].ID)
	if err != nil {
		t.Error(err)
		return
	}

	names := ""
	for _, proj := range projects {
		names += "_" + proj.Name
	}

	if names != "_test_mult_mult2" {
		t.Errorf("not all projects created: %s", names)
		return
	}

	close(done)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
		return
	}

	err = p.Signal(os.Interrupt)
	if err != nil {
		t.Fatal(err)
		return
	}
	<-time.After(10 * time.Millisecond)
}

func TestSshServerRsync(t *testing.T) {
	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}
	logger := slog.New(
		slog.NewTextHandler(os.Stdout, opts),
	)
	slog.SetDefault(logger)
	dbpool := pgsdb.NewDBMemory(logger)
	// setup test data
	dbpool.SetupTestData()
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		panic(err)
	}
	cfg := NewPgsConfig(logger, dbpool, st)
	done := make(chan error)
	prometheus.DefaultRegisterer = prometheus.NewRegistry()
	go StartSshServer(cfg, done)
	// Hack to wait for startup
	time.Sleep(time.Millisecond * 100)

	user := GenerateUser()
	key := utils.KeyForKeyText(user.signer.PublicKey())
	// add user's pubkey to the default test account
	dbpool.Pubkeys = append(dbpool.Pubkeys, &db.PublicKey{
		ID:     "nice-pubkey",
		UserID: dbpool.Users[0].ID,
		Key:    key,
	})

	conn, err := user.NewClient()
	if err != nil {
		t.Error(err)
		return
	}
	defer func() {
		_ = conn.Close()
	}()

	// open an SFTP session over an existing ssh connection.
	client, err := sftp.NewClient(conn)
	if err != nil {
		cfg.Logger.Error("could not create sftp client", "err", err)
		panic(err)
	}
	defer func() {
		_ = client.Close()
	}()

	name, err := os.MkdirTemp("", "rsync-")
	if err != nil {
		panic(err)
	}

	// remove the temporary directory at the end of the program
	// defer os.RemoveAll(name)

	block := &pem.Block{
		Type:  "OPENSSH PRIVATE KEY",
		Bytes: user.privateKey,
	}
	keyFile := filepath.Join(name, "id_ed25519")
	err = os.WriteFile(
		keyFile,
		pem.EncodeToMemory(block), 0600,
	)
	if err != nil {
		t.Fatal(err)
	}

	index := "<!doctype html><html><body>index</body></html>"
	err = os.WriteFile(
		filepath.Join(name, "index.html"),
		[]byte(index), 0666,
	)
	if err != nil {
		t.Fatal(err)
	}

	about := "<!doctype html><html><body>about</body></html>"
	aboutFile := filepath.Join(name, "about.html")
	err = os.WriteFile(
		aboutFile,
		[]byte(about), 0666,
	)
	if err != nil {
		t.Fatal(err)
	}

	contact := "<!doctype html><html><body>contact</body></html>"
	err = os.WriteFile(
		filepath.Join(name, "contact.html"),
		[]byte(contact), 0666,
	)
	if err != nil {
		t.Fatal(err)
	}

	eCmd := fmt.Sprintf(
		"ssh -p 2222 -o IdentitiesOnly=yes -i %s -o StrictHostKeyChecking=no",
		keyFile,
	)

	// copy files
	cmd := exec.Command("rsync", "-rv", "-e", eCmd, name+"/", "localhost:/test")
	result, err := cmd.CombinedOutput()
	if err != nil {
		cfg.Logger.Error("cannot upload", "err", err, "result", string(result))
		t.Error(err)
		return
	}

	// check it's there
	fi, err := client.Lstat("/test/about.html")
	if err != nil {
		cfg.Logger.Error("could not get stat for file", "err", err)
		t.Error("about.html not found")
		return
	}
	if fi.Size() != 46 {
		cfg.Logger.Error("about.html wrong size", "size", fi.Size())
		t.Error("about.html wrong size")
		return
	}

	// remove about file
	_ = os.Remove(aboutFile)

	// copy files with delete
	delCmd := exec.Command("rsync", "-rv", "--delete", "-e", eCmd, name+"/", "localhost:/test")
	result, err = delCmd.CombinedOutput()
	if err != nil {
		cfg.Logger.Error("cannot upload with delete", "err", err, "result", string(result))
		t.Error(err)
		return
	}

	// check it's not there
	_, err = client.Lstat("/test/about.html")
	if err == nil {
		cfg.Logger.Error("file still exists")
		t.Error("about.html found")
		return
	}

	dlName, err := os.MkdirTemp("", "rsync-download")
	if err != nil {
		panic(err)
	}
	defer func() {
		_ = os.RemoveAll(dlName)
	}()
	// download files
	downloadCmd := exec.Command("rsync", "-rvvv", "-e", eCmd, "localhost:/test/", dlName+"/")
	result, err = downloadCmd.CombinedOutput()
	if err != nil {
		cfg.Logger.Error("cannot download files", "err", err, "result", string(result))
		t.Error(err)
		return
	}
	// check contents
	idx, err := os.ReadFile(filepath.Join(dlName, "index.html"))
	if err != nil {
		cfg.Logger.Error("cannot open file", "file", "index.html", "err", err)
		t.Error(err)
		return
	}
	if string(idx) != index {
		t.Error("downloaded index.html file does not match original")
		return
	}
	_, err = os.ReadFile(filepath.Join(dlName, "about.html"))
	if err == nil {
		cfg.Logger.Error("about file should not exist", "file", "about.html")
		t.Error(err)
		return
	}
	cnt, err := os.ReadFile(filepath.Join(dlName, "contact.html"))
	if err != nil {
		cfg.Logger.Error("cannot open file", "file", "contact.html", "err", err)
		t.Error(err)
		return
	}
	if string(cnt) != contact {
		t.Error("downloaded contact.html file does not match original")
		return
	}

	close(done)

	p, err := os.FindProcess(os.Getpid())
	if err != nil {
		t.Fatal(err)
		return
	}

	err = p.Signal(os.Interrupt)
	if err != nil {
		t.Fatal(err)
		return
	}
	<-time.After(10 * time.Millisecond)
}

type UserSSH struct {
	username   string
	signer     ssh.Signer
	privateKey []byte
}

func NewUserSSH(username string, signer ssh.Signer) *UserSSH {
	return &UserSSH{
		username: username,
		signer:   signer,
	}
}

func (s UserSSH) Public() string {
	pubkey := s.signer.PublicKey()
	return string(ssh.MarshalAuthorizedKey(pubkey))
}

func (s UserSSH) MustCmd(client *ssh.Client, patch []byte, cmd string) string {
	res, err := s.Cmd(client, patch, cmd)
	if err != nil {
		panic(err)
	}
	return res
}

func (s UserSSH) NewClient() (*ssh.Client, error) {
	host := "localhost:2222"

	config := &ssh.ClientConfig{
		User: s.username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(s.signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	client, err := ssh.Dial("tcp", host, config)
	return client, err
}

func (s UserSSH) Cmd(client *ssh.Client, patch []byte, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() {
		_ = session.Close()
	}()

	stdinPipe, err := session.StdinPipe()
	if err != nil {
		return "", err
	}

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}

	if err := session.Start(cmd); err != nil {
		return "", err
	}

	if patch != nil {
		_, err = stdinPipe.Write(patch)
		if err != nil {
			return "", err
		}
	}

	err = stdinPipe.Close()
	if err != nil {
		return "", err
	}

	if err := session.Wait(); err != nil {
		return "", err
	}

	buf := new(strings.Builder)
	_, err = io.Copy(buf, stdoutPipe)
	if err != nil {
		return "", err
	}

	return buf.String(), nil
}

func GenerateUser() UserSSH {
	_, userKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		panic(err)
	}

	b, err := ssh.MarshalPrivateKey(userKey, "")
	if err != nil {
		panic(err)
	}

	userSigner, err := ssh.NewSignerFromKey(userKey)
	if err != nil {
		panic(err)
	}

	return UserSSH{
		username:   "testuser",
		signer:     userSigner,
		privateKey: b.Bytes,
	}
}

func WriteFileWithSftp(cfg *PgsConfig, conn *ssh.Client) (*os.FileInfo, error) {
	// open an SFTP session over an existing ssh connection.
	client, err := sftp.NewClient(conn)
	if err != nil {
		cfg.Logger.Error("could not create sftp client", "err", err)
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()

	f, err := client.Create("test/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not create file", "err", err)
		return nil, err
	}
	if _, err := f.Write([]byte("Hello world!")); err != nil {
		cfg.Logger.Error("could not write to file", "err", err)
		return nil, err
	}

	cfg.Logger.Info("closing", "err", f.Close())

	// check it's there
	fi, err := client.Lstat("test/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not get stat for file", "err", err)
		return nil, err
	}

	return &fi, nil
}

func WriteFilesMultProjectsWithSftp(cfg *PgsConfig, conn *ssh.Client) (*os.FileInfo, error) {
	// open an SFTP session over an existing ssh connection.
	client, err := sftp.NewClient(conn)
	if err != nil {
		cfg.Logger.Error("could not create sftp client", "err", err)
		return nil, err
	}
	defer func() {
		_ = client.Close()
	}()

	f, err := client.Create("mult/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not create file", "err", err)
		return nil, err
	}
	if _, err := f.Write([]byte("Hello world!")); err != nil {
		cfg.Logger.Error("could not write to file", "err", err)
		return nil, err
	}

	f, err = client.Create("mult2/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not create file", "err", err)
		return nil, err
	}
	if _, err := f.Write([]byte("Hello world!")); err != nil {
		cfg.Logger.Error("could not write to file", "err", err)
		return nil, err
	}

	cfg.Logger.Info("closing", "err", f.Close())

	// check it's there
	fi, err := client.Lstat("test/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not get stat for file", "err", err)
		return nil, err
	}

	return &fi, nil
}
