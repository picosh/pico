package pgs

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/picosh/pico/db"
	pgsdb "github.com/picosh/pico/pgs/db"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/utils"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

func TestSshServer(t *testing.T) {
	logger := slog.Default()
	dbpool := pgsdb.NewDBMemory(logger)
	// setup test data
	dbpool.SetupTestData()
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		panic(err)
	}
	cfg := NewPgsConfig(logger, dbpool, st)
	done := make(chan error)
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
	defer client.Close()

	_, err = WriteFileWithSftp(cfg, client)
	if err != nil {
		t.Error(err)
		return
	}

	done <- nil
}

type UserSSH struct {
	username string
	signer   ssh.Signer
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
	defer session.Close()

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

	stdinPipe.Close()

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

	userSigner, err := ssh.NewSignerFromKey(userKey)
	if err != nil {
		panic(err)
	}

	return UserSSH{
		username: "testuser",
		signer:   userSigner,
	}
}

func WriteFileWithSftp(cfg *PgsConfig, conn *ssh.Client) (*os.FileInfo, error) {
	// open an SFTP session over an existing ssh connection.
	client, err := sftp.NewClient(conn)
	if err != nil {
		cfg.Logger.Error("could not create sftp client", "err", err)
		return nil, err
	}
	defer client.Close()

	f, err := client.Create("test/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not create file", "err", err)
		return nil, err
	}
	if _, err := f.Write([]byte("Hello world!")); err != nil {
		cfg.Logger.Error("could not write to file", "err", err)
		return nil, err
	}
	f.Close()

	// check it's there
	fi, err := client.Lstat("test/hello.txt")
	if err != nil {
		cfg.Logger.Error("could not get stat for file", "err", err)
		return nil, err
	}

	return &fi, nil
}
