package pipe

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	psub "github.com/picosh/pubsub"
	"github.com/picosh/utils"
	"github.com/prometheus/client_golang/prometheus"
	"golang.org/x/crypto/ssh"
)

type TestDB struct {
	*stub.StubDB
	Users    []*db.User
	Pubkeys  []*db.PublicKey
	Features []*db.FeatureFlag
}

func NewTestDB(logger *slog.Logger) *TestDB {
	return &TestDB{
		StubDB: stub.NewStubDB(logger),
	}
}

func (t *TestDB) FindUserByPubkey(key string) (*db.User, error) {
	for _, pk := range t.Pubkeys {
		if pk.Key == key {
			return t.FindUser(pk.UserID)
		}
	}
	return nil, fmt.Errorf("user not found for pubkey")
}

func (t *TestDB) FindUser(userID string) (*db.User, error) {
	for _, user := range t.Users {
		if user.ID == userID {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (t *TestDB) FindUserByName(name string) (*db.User, error) {
	for _, user := range t.Users {
		if user.Name == name {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (t *TestDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	for _, ff := range t.Features {
		if ff.UserID == userID && ff.Name == name {
			return ff, nil
		}
	}
	return nil, fmt.Errorf("feature not found")
}

func (t *TestDB) HasFeatureByUser(userID string, feature string) bool {
	ff, err := t.FindFeature(userID, feature)
	if err != nil {
		return false
	}
	return ff.IsValid()
}

func (t *TestDB) InsertAccessLog(_ *db.AccessLog) error {
	return nil
}

func (t *TestDB) Close() error {
	return nil
}

func (t *TestDB) AddUser(user *db.User) {
	t.Users = append(t.Users, user)
}

func (t *TestDB) AddPubkey(pubkey *db.PublicKey) {
	t.Pubkeys = append(t.Pubkeys, pubkey)
}

type TestSSHServer struct {
	Cfg    *shared.ConfigSite
	DBPool *TestDB
	Cancel context.CancelFunc
}

func NewTestSSHServer(t *testing.T) *TestSSHServer {
	t.Helper()

	opts := &slog.HandlerOptions{
		AddSource: true,
		Level:     slog.LevelDebug,
	}
	logger := slog.New(slog.NewTextHandler(os.Stdout, opts))

	dbpool := NewTestDB(logger)

	cfg := &shared.ConfigSite{
		Domain:       "pipe.test",
		Port:         "2225",
		PortOverride: "2225",
		Protocol:     "ssh",
		Logger:       logger,
		Space:        "pipe",
	}

	ctx, cancel := context.WithCancel(context.Background())

	pubsub := psub.NewMulticast(logger)
	handler := &CliHandler{
		Logger:  logger,
		DBPool:  dbpool,
		PubSub:  pubsub,
		Cfg:     cfg,
		Waiters: syncmap.New[string, []string](),
		Access:  syncmap.New[string, []string](),
	}

	sshAuth := shared.NewSshAuthHandler(dbpool, logger, "pipe")

	prometheus.DefaultRegisterer = prometheus.NewRegistry()

	server, err := pssh.NewSSHServerWithConfig(
		ctx,
		logger,
		"pipe-ssh-test",
		"localhost",
		cfg.Port,
		"9223",
		"../../ssh_data/term_info_ed25519",
		func(conn ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			perms, _ := sshAuth.PubkeyAuthHandler(conn, key)
			if perms == nil {
				perms = &ssh.Permissions{
					Extensions: map[string]string{
						"pubkey": utils.KeyForKeyText(key),
					},
				}
			}
			return perms, nil
		},
		[]pssh.SSHServerMiddleware{
			Middleware(handler),
			pssh.LogMiddleware(handler, dbpool),
		},
		nil,
		nil,
	)

	if err != nil {
		t.Fatalf("failed to create ssh server: %v", err)
	}

	go func() {
		if err := server.ListenAndServe(); err != nil {
			logger.Error("serve", "err", err.Error())
		}
	}()

	time.Sleep(100 * time.Millisecond)

	return &TestSSHServer{
		Cfg:    cfg,
		DBPool: dbpool,
		Cancel: cancel,
	}
}

func (s *TestSSHServer) Shutdown() {
	s.Cancel()
	time.Sleep(10 * time.Millisecond)
}

type UserSSH struct {
	username   string
	signer     ssh.Signer
	privateKey []byte
}

func GenerateUser(username string) UserSSH {
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
		username:   username,
		signer:     userSigner,
		privateKey: b.Bytes,
	}
}

func (u UserSSH) PublicKey() string {
	return utils.KeyForKeyText(u.signer.PublicKey())
}

func (u UserSSH) NewClient() (*ssh.Client, error) {
	config := &ssh.ClientConfig{
		User: u.username,
		Auth: []ssh.AuthMethod{
			ssh.PublicKeys(u.signer),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
	}

	return ssh.Dial("tcp", "localhost:2225", config)
}

func (u UserSSH) RunCommand(client *ssh.Client, cmd string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = session.Close() }()

	stdoutPipe, err := session.StdoutPipe()
	if err != nil {
		return "", err
	}

	stderrPipe, err := session.StderrPipe()
	if err != nil {
		return "", err
	}

	if err := session.Start(cmd); err != nil {
		return "", err
	}

	stdout := new(strings.Builder)
	stderr := new(strings.Builder)
	_, _ = io.Copy(stdout, stdoutPipe)
	_, _ = io.Copy(stderr, stderrPipe)

	_ = session.Wait()
	return stdout.String() + stderr.String(), nil
}

func (u UserSSH) RunCommandWithStdin(client *ssh.Client, cmd string, stdin string) (string, error) {
	session, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer func() { _ = session.Close() }()

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

	_, err = stdinPipe.Write([]byte(stdin))
	if err != nil {
		return "", err
	}
	_ = stdinPipe.Close()

	buf := new(strings.Builder)
	_, err = io.Copy(buf, stdoutPipe)
	if err != nil {
		return "", err
	}

	_ = session.Wait()
	return buf.String(), nil
}

func RegisterUserWithServer(server *TestSSHServer, user UserSSH) {
	dbUser := &db.User{
		ID:   user.username + "-id",
		Name: user.username,
	}
	server.DBPool.AddUser(dbUser)
	server.DBPool.AddPubkey(&db.PublicKey{
		ID:     user.username + "-pubkey-id",
		UserID: dbUser.ID,
		Key:    user.PublicKey(),
	})
}

func TestLs_UnauthenticatedUserDenied(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("anonymous")

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "ls")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	if !strings.Contains(output, "access denied") {
		t.Errorf("expected 'access denied', got: %s", output)
	}
}

func TestLs_AuthenticatedUser(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "ls")
	if err != nil {
		t.Logf("command completed with: %v", err)
	}

	if strings.Contains(output, "access denied") {
		t.Errorf("authenticated user should not get access denied, got: %s", output)
	}

	if !strings.Contains(output, "no pubsub channels found") {
		t.Errorf("expected 'no pubsub channels found' for empty state, got: %s", output)
	}
}

func TestPubSub_BasicFlow(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	subClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber: %v", err)
	}
	defer func() { _ = subClient.Close() }()

	pubClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher: %v", err)
	}
	defer func() { _ = pubClient.Close() }()

	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub testtopic -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	testMessage := "hello from pub"
	_, err = user.RunCommandWithStdin(pubClient, "pub testtopic -c", testMessage)
	if err != nil {
		t.Logf("pub command completed: %v", err)
	}

	received := make([]byte, len(testMessage)+10)
	n, err := subStdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	receivedStr := string(received[:n])
	if !strings.Contains(receivedStr, testMessage) {
		t.Errorf("subscriber did not receive message, got: %q, want: %q", receivedStr, testMessage)
	}
}

func TestPubSub_PublicTopic(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	alice := GenerateUser("alice")
	bob := GenerateUser("bob")
	RegisterUserWithServer(server, alice)
	RegisterUserWithServer(server, bob)

	subClient, err := bob.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber: %v", err)
	}
	defer func() { _ = subClient.Close() }()

	pubClient, err := alice.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher: %v", err)
	}
	defer func() { _ = pubClient.Close() }()

	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub publictopic -p -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	testMessage := "public message"
	_, err = alice.RunCommandWithStdin(pubClient, "pub publictopic -p -c", testMessage)
	if err != nil {
		t.Logf("pub command completed: %v", err)
	}

	received := make([]byte, len(testMessage)+10)
	n, err := subStdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	receivedStr := string(received[:n])
	if !strings.Contains(receivedStr, testMessage) {
		t.Errorf("subscriber did not receive public message, got: %q, want: %q", receivedStr, testMessage)
	}
}

func TestPipe_Bidirectional(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	alice := GenerateUser("alice")
	bob := GenerateUser("bob")
	RegisterUserWithServer(server, alice)
	RegisterUserWithServer(server, bob)

	aliceClient, err := alice.NewClient()
	if err != nil {
		t.Fatalf("failed to connect alice: %v", err)
	}
	defer func() { _ = aliceClient.Close() }()

	bobClient, err := bob.NewClient()
	if err != nil {
		t.Fatalf("failed to connect bob: %v", err)
	}
	defer func() { _ = bobClient.Close() }()

	aliceSession, err := aliceClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create alice session: %v", err)
	}
	defer func() { _ = aliceSession.Close() }()

	aliceStdin, err := aliceSession.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get alice stdin: %v", err)
	}

	aliceStdout, err := aliceSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get alice stdout: %v", err)
	}

	if err := aliceSession.Start("pipe pipetopic -p -c"); err != nil {
		t.Fatalf("failed to start alice pipe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	bobSession, err := bobClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create bob session: %v", err)
	}
	defer func() { _ = bobSession.Close() }()

	bobStdin, err := bobSession.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get bob stdin: %v", err)
	}

	bobStdout, err := bobSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get bob stdout: %v", err)
	}

	if err := bobSession.Start("pipe pipetopic -p -c"); err != nil {
		t.Fatalf("failed to start bob pipe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	aliceMsg := "hello from alice\n"
	_, err = aliceStdin.Write([]byte(aliceMsg))
	if err != nil {
		t.Fatalf("alice failed to write: %v", err)
	}

	bobReceived := make([]byte, 100)
	n, err := bobStdout.Read(bobReceived)
	if err != nil && err != io.EOF {
		t.Logf("bob read error: %v", err)
	}
	if !strings.Contains(string(bobReceived[:n]), "hello from alice") {
		t.Errorf("bob did not receive alice's message, got: %q", string(bobReceived[:n]))
	}

	bobMsg := "hello from bob\n"
	_, err = bobStdin.Write([]byte(bobMsg))
	if err != nil {
		t.Fatalf("bob failed to write: %v", err)
	}

	aliceReceived := make([]byte, 100)
	n, err = aliceStdout.Read(aliceReceived)
	if err != nil && err != io.EOF {
		t.Logf("alice read error: %v", err)
	}
	if !strings.Contains(string(aliceReceived[:n]), "hello from bob") {
		t.Errorf("alice did not receive bob's message, got: %q", string(aliceReceived[:n]))
	}
}

func TestPipe_AutoGeneratedTopic(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer func() { _ = session.Close() }()

	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout: %v", err)
	}

	if err := session.Start("pipe"); err != nil {
		t.Fatalf("failed to start pipe: %v", err)
	}

	received := make([]byte, 200)
	n, err := stdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	output := string(received[:n])
	if !strings.Contains(output, "subscribe to this topic") {
		t.Errorf("expected topic subscription instructions, got: %q", output)
	}
}

func TestAccessControl_AllowedUserViaFullPath(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	alice := GenerateUser("alice")
	bob := GenerateUser("bob")
	RegisterUserWithServer(server, alice)
	RegisterUserWithServer(server, bob)

	aliceClient, err := alice.NewClient()
	if err != nil {
		t.Fatalf("failed to connect alice: %v", err)
	}
	defer func() { _ = aliceClient.Close() }()

	aliceSession, err := aliceClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create alice session: %v", err)
	}
	defer func() { _ = aliceSession.Close() }()

	aliceStdout, err := aliceSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get alice stdout: %v", err)
	}

	if err := aliceSession.Start("sub sharedtopic -a alice,bob -c"); err != nil {
		t.Fatalf("failed to start alice sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	bobClient, err := bob.NewClient()
	if err != nil {
		t.Fatalf("failed to connect bob: %v", err)
	}
	defer func() { _ = bobClient.Close() }()

	_, err = bob.RunCommandWithStdin(bobClient, "pub alice/sharedtopic -c", "bob allowed")
	if err != nil {
		t.Logf("bob pub completed: %v", err)
	}

	aliceReceived := make([]byte, 100)
	n, _ := aliceStdout.Read(aliceReceived)

	if !strings.Contains(string(aliceReceived[:n]), "bob allowed") {
		t.Errorf("alice should receive bob's message on shared topic, got: %q", string(aliceReceived[:n]))
	}
}
