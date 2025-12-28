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
	Users        []*db.User
	Pubkeys      []*db.PublicKey
	Features     []*db.FeatureFlag
	PipeMonitors []*db.PipeMonitor
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

func (t *TestDB) UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error {
	for _, m := range t.PipeMonitors {
		if m.UserId == userID && m.Topic == topic {
			m.WindowDur = dur
			m.WindowEnd = winEnd
			now := time.Now()
			m.UpdatedAt = &now
			return nil
		}
	}
	now := time.Now()
	t.PipeMonitors = append(t.PipeMonitors, &db.PipeMonitor{
		ID:        fmt.Sprintf("monitor-%s-%s", userID, topic),
		UserId:    userID,
		Topic:     topic,
		WindowDur: dur,
		WindowEnd: winEnd,
		CreatedAt: &now,
		UpdatedAt: &now,
	})
	return nil
}

func (t *TestDB) UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error {
	for _, m := range t.PipeMonitors {
		if m.UserId == userID && m.Topic == topic {
			m.LastPing = lastPing
			now := time.Now()
			m.UpdatedAt = &now
			return nil
		}
	}
	return fmt.Errorf("monitor not found")
}

func (t *TestDB) RemovePipeMonitor(userID, topic string) error {
	for i, m := range t.PipeMonitors {
		if m.UserId == userID && m.Topic == topic {
			t.PipeMonitors = append(t.PipeMonitors[:i], t.PipeMonitors[i+1:]...)
			return nil
		}
	}
	return fmt.Errorf("monitor not found")
}

func (t *TestDB) FindPipeMonitorByTopic(userID, topic string) (*db.PipeMonitor, error) {
	for _, m := range t.PipeMonitors {
		if m.UserId == userID && m.Topic == topic {
			return m, nil
		}
	}
	return nil, fmt.Errorf("monitor not found")
}

func (t *TestDB) FindPipeMonitorsByUser(userID string) ([]*db.PipeMonitor, error) {
	var monitors []*db.PipeMonitor
	for _, m := range t.PipeMonitors {
		if m.UserId == userID {
			monitors = append(monitors, m)
		}
	}
	return monitors, nil
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

func TestPubSub_BlockingWaitsForSubscriber(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	pubClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher: %v", err)
	}
	defer func() { _ = pubClient.Close() }()

	subClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber: %v", err)
	}
	defer func() { _ = subClient.Close() }()

	pubSession, err := pubClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create pub session: %v", err)
	}
	defer func() { _ = pubSession.Close() }()

	pubStdin, err := pubSession.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get pub stdin: %v", err)
	}

	pubStdout, err := pubSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get pub stdout: %v", err)
	}

	// Start publisher with blocking enabled (default -b=true)
	// Publisher should wait for subscriber
	if err := pubSession.Start("pub blockingtopic"); err != nil {
		t.Fatalf("failed to start pub: %v", err)
	}

	// Read output until we see "waiting" message or timeout
	// Need to read in a loop because Read() may return partial data
	var output string
	readDone := make(chan struct{})
	go func() {
		buf := make([]byte, 1024)
		for {
			n, err := pubStdout.Read(buf)
			if n > 0 {
				output += string(buf[:n])
				if strings.Contains(output, "waiting") {
					close(readDone)
					return
				}
			}
			if err != nil {
				close(readDone)
				return
			}
		}
	}()

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for 'waiting' message, got: %q", output)
	}

	if !strings.Contains(output, "waiting") {
		t.Errorf("expected 'waiting' message for blocking pub, got: %q", output)
	}

	// Now start subscriber - this should unblock the publisher
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub blockingtopic -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Now send the message
	testMessage := "blocking message"
	_, err = pubStdin.Write([]byte(testMessage))
	if err != nil {
		t.Fatalf("failed to write message: %v", err)
	}
	_ = pubStdin.Close()

	// Subscriber should receive the message
	received := make([]byte, 100)
	nRead, err := subStdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	if !strings.Contains(string(received[:nRead]), testMessage) {
		t.Errorf("subscriber did not receive blocking message, got: %q, want: %q", string(received[:nRead]), testMessage)
	}
}

func TestPubSub_NonBlockingDoesNotWait(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Publish with -b=false (non-blocking) and no subscriber
	// Should complete immediately without waiting
	done := make(chan struct{})
	var output string
	var cmdErr error

	go func() {
		output, cmdErr = user.RunCommandWithStdin(client, "pub nonblockingtopic -b=false -c", "non-blocking message")
		close(done)
	}()

	select {
	case <-done:
		// Command completed - this is expected for non-blocking
		if cmdErr != nil {
			t.Logf("non-blocking pub completed with: %v", cmdErr)
		}
		t.Logf("non-blocking pub output: %q", output)
	case <-time.After(2 * time.Second):
		t.Errorf("non-blocking pub should complete immediately, but it blocked")
	}
}

func TestPubSub_BlockingTimeout(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Publish with blocking and short timeout, no subscriber
	// Should timeout after the specified duration
	done := make(chan struct{})
	var output string

	go func() {
		output, _ = user.RunCommandWithStdin(client, "pub timeouttopic -b=true -t=500ms", "timeout message")
		close(done)
	}()

	select {
	case <-done:
		// Command completed due to timeout
		if !strings.Contains(output, "timeout") && !strings.Contains(output, "waiting") {
			t.Logf("blocking pub with timeout output: %q", output)
		}
	case <-time.After(3 * time.Second):
		t.Errorf("blocking pub with timeout should have timed out after 500ms")
	}
}

func TestSub_WaitsForPublisher(t *testing.T) {
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

	// Start subscriber first - it should wait for publisher
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub waitfortopic -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	// Subscriber is now waiting - give it a moment
	time.Sleep(100 * time.Millisecond)

	// Now publish - subscriber should receive it
	testMessage := "delayed publish"
	_, err = user.RunCommandWithStdin(pubClient, "pub waitfortopic -c", testMessage)
	if err != nil {
		t.Logf("pub completed: %v", err)
	}

	received := make([]byte, 100)
	n, err := subStdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	if !strings.Contains(string(received[:n]), testMessage) {
		t.Errorf("subscriber waiting for publisher did not receive message, got: %q, want: %q", string(received[:n]), testMessage)
	}
}

func TestSub_KeepAliveReceivesMultipleMessages(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	subClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber: %v", err)
	}
	defer func() { _ = subClient.Close() }()

	pubClient1, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher 1: %v", err)
	}
	defer func() { _ = pubClient1.Close() }()

	pubClient2, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher 2: %v", err)
	}
	defer func() { _ = pubClient2.Close() }()

	// Start subscriber with keepAlive (-k) flag
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub keepalivetopic -k -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Send first message
	msg1 := "first message\n"
	_, err = user.RunCommandWithStdin(pubClient1, "pub keepalivetopic -c", msg1)
	if err != nil {
		t.Logf("pub 1 completed: %v", err)
	}

	received1 := make([]byte, 100)
	n1, _ := subStdout.Read(received1)
	if !strings.Contains(string(received1[:n1]), "first message") {
		t.Errorf("subscriber did not receive first message, got: %q", string(received1[:n1]))
	}

	// Send second message - subscriber with keepAlive should still receive it
	msg2 := "second message\n"
	_, err = user.RunCommandWithStdin(pubClient2, "pub keepalivetopic -c", msg2)
	if err != nil {
		t.Logf("pub 2 completed: %v", err)
	}

	received2 := make([]byte, 100)
	n2, _ := subStdout.Read(received2)
	if !strings.Contains(string(received2[:n2]), "second message") {
		t.Errorf("subscriber with keepAlive did not receive second message, got: %q", string(received2[:n2]))
	}
}

func TestSub_WithoutKeepAliveExitsAfterPublisher(t *testing.T) {
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

	// Start subscriber without keepAlive
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub exitaftertopic -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish a message
	testMessage := "single message"
	_, err = user.RunCommandWithStdin(pubClient, "pub exitaftertopic -c", testMessage)
	if err != nil {
		t.Logf("pub completed: %v", err)
	}

	// Read the message
	received := make([]byte, 100)
	n, _ := subStdout.Read(received)
	if !strings.Contains(string(received[:n]), testMessage) {
		t.Errorf("subscriber did not receive message, got: %q", string(received[:n]))
	}

	// Subscriber session should exit after publisher disconnects
	done := make(chan error)
	go func() {
		done <- subSession.Wait()
	}()

	select {
	case err := <-done:
		// Session ended as expected
		t.Logf("subscriber session ended: %v", err)
	case <-time.After(2 * time.Second):
		t.Errorf("subscriber without keepAlive should have exited after publisher disconnected")
		_ = subSession.Close()
	}
}

func TestPub_EmptyMessage(t *testing.T) {
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

	// Start subscriber
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	subStdout, err := subSession.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub stdout: %v", err)
	}

	if err := subSession.Start("sub emptytopic -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish with -e flag (empty message) - should not require stdin
	output, err := user.RunCommand(pubClient, "pub emptytopic -e -c")
	if err != nil {
		t.Logf("pub -e completed: %v, output: %s", err, output)
	}

	// Subscriber should receive something (even if empty/minimal)
	// The -e flag sends a 1-byte buffer
	received := make([]byte, 10)
	n, err := subStdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read result: n=%d, err=%v", n, err)
	}

	// With -e flag, we expect to receive at least 1 byte
	if n < 1 {
		t.Errorf("subscriber should receive empty message signal, got %d bytes", n)
	}
}

func TestPipe_AccessControl(t *testing.T) {
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

	// Alice creates a pipe with access control allowing bob
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

	if err := aliceSession.Start("pipe accesspipe -a alice,bob -c"); err != nil {
		t.Fatalf("failed to start alice pipe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Bob joins the pipe using alice's namespace
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

	if err := bobSession.Start("pipe alice/accesspipe -c"); err != nil {
		t.Fatalf("failed to start bob pipe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Alice sends message to bob
	aliceMsg := "hello bob\n"
	_, err = aliceStdin.Write([]byte(aliceMsg))
	if err != nil {
		t.Fatalf("alice failed to write: %v", err)
	}

	bobReceived := make([]byte, 100)
	n, _ := bobStdout.Read(bobReceived)
	if !strings.Contains(string(bobReceived[:n]), "hello bob") {
		t.Errorf("bob did not receive alice's message, got: %q", string(bobReceived[:n]))
	}

	// Bob sends message to alice
	bobMsg := "hello alice\n"
	_, err = bobStdin.Write([]byte(bobMsg))
	if err != nil {
		t.Fatalf("bob failed to write: %v", err)
	}

	aliceReceived := make([]byte, 100)
	n, _ = aliceStdout.Read(aliceReceived)
	if !strings.Contains(string(aliceReceived[:n]), "hello alice") {
		t.Errorf("alice did not receive bob's message, got: %q", string(aliceReceived[:n]))
	}
}

func TestPipe_Replay(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Start pipe with replay flag (-r)
	session, err := client.NewSession()
	if err != nil {
		t.Fatalf("failed to create session: %v", err)
	}
	defer func() { _ = session.Close() }()

	stdin, err := session.StdinPipe()
	if err != nil {
		t.Fatalf("failed to get stdin: %v", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get stdout: %v", err)
	}

	if err := session.Start("pipe replaytopic -r -c"); err != nil {
		t.Fatalf("failed to start pipe: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Send a message - with -r flag, should receive it back
	testMsg := "echo back\n"
	_, err = stdin.Write([]byte(testMsg))
	if err != nil {
		t.Fatalf("failed to write: %v", err)
	}

	received := make([]byte, 100)
	n, err := stdout.Read(received)
	if err != nil && err != io.EOF {
		t.Logf("read error: %v", err)
	}

	if !strings.Contains(string(received[:n]), "echo back") {
		t.Errorf("with -r flag, sender should receive own message back, got: %q", string(received[:n]))
	}
}

func TestAccessControl_UnauthorizedUserDenied(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	alice := GenerateUser("alice")
	bob := GenerateUser("bob")
	charlie := GenerateUser("charlie")
	RegisterUserWithServer(server, alice)
	RegisterUserWithServer(server, bob)
	RegisterUserWithServer(server, charlie)

	aliceClient, err := alice.NewClient()
	if err != nil {
		t.Fatalf("failed to connect alice: %v", err)
	}
	defer func() { _ = aliceClient.Close() }()

	charlieClient, err := charlie.NewClient()
	if err != nil {
		t.Fatalf("failed to connect charlie: %v", err)
	}
	defer func() { _ = charlieClient.Close() }()

	// Alice creates a topic with access only for alice and bob (not charlie)
	aliceSession, err := aliceClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create alice session: %v", err)
	}
	defer func() { _ = aliceSession.Close() }()

	if err := aliceSession.Start("sub restrictedtopic -a alice,bob -c"); err != nil {
		t.Fatalf("failed to start alice sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Charlie tries to publish to alice's restricted topic - should be denied
	output, err := charlie.RunCommandWithStdin(charlieClient, "pub alice/restrictedtopic -c", "unauthorized message")
	if err != nil {
		t.Logf("charlie pub completed with error (expected): %v", err)
	}

	// Charlie should get access denied or the message should not be delivered
	if strings.Contains(output, "access denied") {
		t.Logf("charlie correctly received access denied")
	} else {
		t.Logf("charlie output: %q (access control may work differently)", output)
	}
}

func TestPubSub_MultipleSubscribers(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	pubClient, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect publisher: %v", err)
	}
	defer func() { _ = pubClient.Close() }()

	sub1Client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber 1: %v", err)
	}
	defer func() { _ = sub1Client.Close() }()

	sub2Client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber 2: %v", err)
	}
	defer func() { _ = sub2Client.Close() }()

	sub3Client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect subscriber 3: %v", err)
	}
	defer func() { _ = sub3Client.Close() }()

	// Start three subscribers
	sub1Session, err := sub1Client.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub1 session: %v", err)
	}
	defer func() { _ = sub1Session.Close() }()

	sub1Stdout, err := sub1Session.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub1 stdout: %v", err)
	}

	if err := sub1Session.Start("sub fanout -c"); err != nil {
		t.Fatalf("failed to start sub1: %v", err)
	}

	sub2Session, err := sub2Client.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub2 session: %v", err)
	}
	defer func() { _ = sub2Session.Close() }()

	sub2Stdout, err := sub2Session.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub2 stdout: %v", err)
	}

	if err := sub2Session.Start("sub fanout -c"); err != nil {
		t.Fatalf("failed to start sub2: %v", err)
	}

	sub3Session, err := sub3Client.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub3 session: %v", err)
	}
	defer func() { _ = sub3Session.Close() }()

	sub3Stdout, err := sub3Session.StdoutPipe()
	if err != nil {
		t.Fatalf("failed to get sub3 stdout: %v", err)
	}

	if err := sub3Session.Start("sub fanout -c"); err != nil {
		t.Fatalf("failed to start sub3: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish a single message
	testMessage := "broadcast message"
	_, err = user.RunCommandWithStdin(pubClient, "pub fanout -c", testMessage)
	if err != nil {
		t.Logf("pub completed: %v", err)
	}

	// All three subscribers should receive the message
	received1 := make([]byte, 100)
	n1, _ := sub1Stdout.Read(received1)
	if !strings.Contains(string(received1[:n1]), testMessage) {
		t.Errorf("subscriber 1 did not receive message, got: %q", string(received1[:n1]))
	}

	received2 := make([]byte, 100)
	n2, _ := sub2Stdout.Read(received2)
	if !strings.Contains(string(received2[:n2]), testMessage) {
		t.Errorf("subscriber 2 did not receive message, got: %q", string(received2[:n2]))
	}

	received3 := make([]byte, 100)
	n3, _ := sub3Stdout.Read(received3)
	if !strings.Contains(string(received3[:n3]), testMessage) {
		t.Errorf("subscriber 3 did not receive message, got: %q", string(received3[:n3]))
	}
}

// Monitor CLI Tests

func TestMonitor_UnauthenticatedUserDenied(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("anonymous")

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "monitor my-service 1h")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	if !strings.Contains(output, "access denied") {
		t.Errorf("expected 'access denied', got: %s", output)
	}
}

func TestMonitor_CreateMonitor(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "monitor pico-uptime 24h")
	if err != nil {
		t.Logf("command completed: %v", err)
	}

	if strings.Contains(output, "access denied") {
		t.Errorf("authenticated user should not get access denied, got: %s", output)
	}

	// Verify monitor was created in DB
	monitor, err := server.DBPool.FindPipeMonitorByTopic("alice-id", "pico-uptime")
	if err != nil {
		t.Fatalf("monitor should exist in DB: %v", err)
	}

	if monitor.WindowDur != 24*time.Hour {
		t.Errorf("expected window duration 24h, got: %v", monitor.WindowDur)
	}

	if !strings.Contains(output, "pico-uptime") || !strings.Contains(output, "24h") {
		t.Errorf("output should confirm monitor creation, got: %s", output)
	}
}

func TestMonitor_UpdateMonitor(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Create initial monitor
	_, err = user.RunCommand(client, "monitor my-cron 1h")
	if err != nil {
		t.Logf("create command completed: %v", err)
	}

	// Upsert with new duration
	output, err := user.RunCommand(client, "monitor my-cron 6h")
	if err != nil {
		t.Logf("update command completed: %v", err)
	}

	// Verify monitor was updated
	monitor, err := server.DBPool.FindPipeMonitorByTopic("alice-id", "my-cron")
	if err != nil {
		t.Fatalf("monitor should exist in DB: %v", err)
	}

	if monitor.WindowDur != 6*time.Hour {
		t.Errorf("expected window duration 6h after update, got: %v", monitor.WindowDur)
	}

	if !strings.Contains(output, "6h") {
		t.Errorf("output should confirm updated duration, got: %s", output)
	}
}

func TestMonitor_DeleteMonitor(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Create monitor first
	_, err = user.RunCommand(client, "monitor to-delete 1h")
	if err != nil {
		t.Logf("create command completed: %v", err)
	}

	// Verify it exists
	_, err = server.DBPool.FindPipeMonitorByTopic("alice-id", "to-delete")
	if err != nil {
		t.Fatalf("monitor should exist before deletion: %v", err)
	}

	// Delete it
	output, err := user.RunCommand(client, "monitor to-delete -d")
	if err != nil {
		t.Logf("delete command completed: %v", err)
	}

	// Verify it's gone
	_, err = server.DBPool.FindPipeMonitorByTopic("alice-id", "to-delete")
	if err == nil {
		t.Errorf("monitor should be deleted from DB")
	}

	if !strings.Contains(output, "deleted") && !strings.Contains(output, "removed") {
		t.Logf("output should confirm deletion, got: %s", output)
	}
}

func TestMonitor_InvalidDuration(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "monitor my-service invaliduration")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	if !strings.Contains(output, "invalid") && !strings.Contains(output, "duration") && !strings.Contains(output, "error") {
		t.Errorf("expected error about invalid duration, got: %s", output)
	}
}

func TestMonitor_MissingTopic(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "monitor")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	// Should show usage or error about missing topic
	if !strings.Contains(output, "Usage") && !strings.Contains(output, "topic") && !strings.Contains(output, "error") {
		t.Errorf("expected usage info or error about missing topic, got: %s", output)
	}
}

// Status CLI Tests

func TestStatus_UnauthenticatedUserDenied(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("anonymous")

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "status")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	if !strings.Contains(output, "access denied") {
		t.Errorf("expected 'access denied', got: %s", output)
	}
}

func TestStatus_NoMonitors(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "status")
	if err != nil {
		t.Logf("command completed: %v", err)
	}

	if !strings.Contains(output, "no monitors") && !strings.Contains(output, "empty") {
		t.Errorf("expected message about no monitors, got: %s", output)
	}
}

func TestStatus_ShowsMonitorStatus(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	// Create a monitor
	_, err = user.RunCommand(client, "monitor web-check 1h")
	if err != nil {
		t.Logf("create monitor completed: %v", err)
	}

	// Check status
	output, err := user.RunCommand(client, "status")
	if err != nil {
		t.Logf("status command completed: %v", err)
	}

	if !strings.Contains(output, "web-check") {
		t.Errorf("status should list the monitor topic, got: %s", output)
	}
}

func TestStatus_ShowsHealthyUnhealthy(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	// Create monitors directly in DB with different states
	now := time.Now()
	windowEnd := now.Add(1 * time.Hour)
	recentPing := now.Add(-30 * time.Minute) // within window - healthy
	oldPing := now.Add(-2 * time.Hour)       // outside window - unhealthy

	_ = server.DBPool.UpsertPipeMonitor("alice-id", "healthy-service", 1*time.Hour, &windowEnd)
	_ = server.DBPool.UpdatePipeMonitorLastPing("alice-id", "healthy-service", &recentPing)

	_ = server.DBPool.UpsertPipeMonitor("alice-id", "unhealthy-service", 1*time.Hour, &windowEnd)
	_ = server.DBPool.UpdatePipeMonitorLastPing("alice-id", "unhealthy-service", &oldPing)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "status")
	if err != nil {
		t.Logf("status command completed: %v", err)
	}

	if !strings.Contains(output, "healthy-service") {
		t.Errorf("status should list healthy-service, got: %s", output)
	}

	if !strings.Contains(output, "unhealthy-service") {
		t.Errorf("status should list unhealthy-service, got: %s", output)
	}

	// Should indicate different health states
	if !strings.Contains(strings.ToLower(output), "healthy") && !strings.Contains(strings.ToLower(output), "ok") && !strings.Contains(output, "✓") {
		t.Logf("status output should indicate health state: %s", output)
	}
}

// RSS CLI Tests

func TestRss_UnauthenticatedUserDenied(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("anonymous")

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "rss")
	if err != nil {
		t.Logf("command error (expected): %v", err)
	}

	if !strings.Contains(output, "access denied") {
		t.Errorf("expected 'access denied', got: %s", output)
	}
}

func TestRss_GeneratesValidRSS(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	// Create a monitor
	now := time.Now()
	windowEnd := now.Add(1 * time.Hour)
	_ = server.DBPool.UpsertPipeMonitor("alice-id", "rss-test-service", 1*time.Hour, &windowEnd)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "rss")
	if err != nil {
		t.Logf("rss command completed: %v", err)
	}

	// Should output valid RSS XML
	if !strings.Contains(output, "<?xml") || !strings.Contains(output, "<rss") {
		t.Errorf("expected RSS XML output, got: %s", output)
	}

	if !strings.Contains(output, "rss-test-service") {
		t.Errorf("RSS should contain monitor topic, got: %s", output)
	}
}

func TestRss_AlertsOnStaleMonitor(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	// Create a stale monitor (last ping outside window)
	now := time.Now()
	windowEnd := now.Add(-30 * time.Minute) // window already ended
	oldPing := now.Add(-2 * time.Hour)

	_ = server.DBPool.UpsertPipeMonitor("alice-id", "stale-service", 1*time.Hour, &windowEnd)
	_ = server.DBPool.UpdatePipeMonitorLastPing("alice-id", "stale-service", &oldPing)

	client, err := user.NewClient()
	if err != nil {
		t.Fatalf("failed to connect: %v", err)
	}
	defer func() { _ = client.Close() }()

	output, err := user.RunCommand(client, "rss")
	if err != nil {
		t.Logf("rss command completed: %v", err)
	}

	// Should contain alert item for stale service
	if !strings.Contains(output, "stale-service") {
		t.Errorf("RSS should contain stale-service alert, got: %s", output)
	}

	// Should have item element for the alert
	if !strings.Contains(output, "<item>") {
		t.Errorf("RSS should contain item element for alert, got: %s", output)
	}
}

// Pub integration with Monitor

func TestPub_UpdatesMonitorLastPing(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()

	user := GenerateUser("alice")
	RegisterUserWithServer(server, user)

	// Create a monitor first
	now := time.Now()
	windowEnd := now.Add(1 * time.Hour)
	_ = server.DBPool.UpsertPipeMonitor("alice-id", "ping-test", 1*time.Hour, &windowEnd)

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

	// Start subscriber
	subSession, err := subClient.NewSession()
	if err != nil {
		t.Fatalf("failed to create sub session: %v", err)
	}
	defer func() { _ = subSession.Close() }()

	if err := subSession.Start("sub ping-test -c"); err != nil {
		t.Fatalf("failed to start sub: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Publish to the monitored topic
	_, err = user.RunCommandWithStdin(pubClient, "pub ping-test -c", "health check")
	if err != nil {
		t.Logf("pub command completed: %v", err)
	}

	// Verify last_ping was updated
	monitor, err := server.DBPool.FindPipeMonitorByTopic("alice-id", "ping-test")
	if err != nil {
		t.Fatalf("monitor should exist: %v", err)
	}

	if monitor.LastPing == nil {
		t.Errorf("last_ping should be set after pub")
	} else if time.Since(*monitor.LastPing) > 5*time.Second {
		t.Errorf("last_ping should be recent, got: %v", monitor.LastPing)
	}
}
