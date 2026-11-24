package pipe

import (
	"crypto/rand"
	"io"
	"iter"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	psub "github.com/picosh/pico/pkg/pubsub"
	"golang.org/x/crypto/ssh"
)

// withCert is the user logging in with a cert signed by their key.
func (u UserSSH) withCert(t *testing.T, principals ...string) UserSSH {
	t.Helper()
	key := GenerateUser(u.username)
	cert := &ssh.Certificate{
		Key:             key.signer.PublicKey(),
		CertType:        ssh.UserCert,
		KeyId:           "ci",
		ValidPrincipals: principals,
		ValidBefore:     ssh.CertTimeInfinity,
	}
	if err := cert.SignCert(rand.Reader, u.signer); err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewCertSigner(cert, key.signer)
	if err != nil {
		t.Fatal(err)
	}
	key.signer = signer
	return key
}

// countingPubSub counts looks at a topic's clients, which claiming it takes.
type countingPubSub struct {
	psub.PubSub
	looks atomic.Int64
}

func (c *countingPubSub) GetChannels() iter.Seq2[string, *psub.Channel] {
	c.looks.Add(1)
	return c.PubSub.GetChannels()
}

func TestRefusedScopesClaimNothing(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()
	counting := &countingPubSub{PubSub: server.PipeHandler.PubSub}
	server.PipeHandler.PubSub = counting
	alice := GenerateUser("alice")
	RegisterUserWithServer(server, alice)
	ci := alice.withCert(t, "pipe:pub:deploys", "pipe:sub:deploys")

	run := func(cmd string) string {
		t.Helper()
		client, err := ci.NewClient()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		out, err := ci.RunCommand(client, cmd)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	for _, cmd := range []string{"pub -b=false -e -a bob secret", "sub -a bob secret", "pipe -a bob secret"} {
		if out := run(cmd); !strings.Contains(out, "this certificate doesn't allow") {
			t.Errorf("%s = %q, want refused", cmd, out)
		}
	}
	if got := counting.looks.Load(); got != 0 {
		t.Errorf("refused commands looked at a topic's clients %d times", got)
	}

	// A claim that's allowed does look.
	run("pub -b=false -e -a bob deploys")
	if counting.looks.Load() == 0 {
		t.Error("an allowed claim didn't look at the topic's clients")
	}
}

func TestScopes(t *testing.T) {
	server := NewTestSSHServer(t)
	defer server.Shutdown()
	alice, bob := GenerateUser("alice"), GenerateUser("bob")
	RegisterUserWithServer(server, alice)
	RegisterUserWithServer(server, bob)
	window := time.Now().Add(time.Hour)
	for _, topic := range []string{"alice/deploys", "alice/secret"} {
		if err := server.DBPool.UpsertPipeMonitor("alice-id", topic, time.Hour, &window); err != nil {
			t.Fatal(err)
		}
	}

	pub := alice.withCert(t, "pipe:pub:deploys")
	sub := alice.withCert(t, "pipe:sub:deploys")
	prefix := alice.withCert(t, "pipe:pub:ci/*")
	both := alice.withCert(t, "pipe:pub:deploys", "pipe:sub:deploys")
	public := alice.withCert(t, "pipe:pub:public/news")
	all := alice.withCert(t, "pipe:pub", "pipe:sub")

	cases := []struct {
		name   string
		user   UserSSH
		cmd    string
		denied bool
		want   string
		absent string
	}{
		{name: "pub", user: pub, cmd: "pub -b=false -e deploys", want: "msg sent!"},
		{name: "pub by full name", user: pub, cmd: "pub -b=false -e alice/deploys", want: "msg sent!"},
		{name: "pub with the topic first", user: pub, cmd: "pub deploys -b=false -e", want: "msg sent!"},
		{name: "pub another", user: pub, cmd: "pub -b=false -e secret", denied: true},
		{name: "pub a longer name", user: pub, cmd: "pub -b=false -e deploys2", denied: true},
		{name: "pub a random topic", user: pub, cmd: "pub -b=false -e", denied: true},
		{name: "pub publicly", user: pub, cmd: "pub -p -b=false -e deploys", denied: true},
		{name: "pub to a user", user: pub, cmd: "pub -b=false -e bob/deploys", denied: true},
		{name: "pub by a subscriber", user: sub, cmd: "pub -b=false -e deploys", denied: true},
		{name: "pub prefix", user: prefix, cmd: "pub -b=false -e ci/build", want: "msg sent!"},
		{name: "pub outside the prefix", user: prefix, cmd: "pub -b=false -e deploys", denied: true},
		{name: "pub a public topic", user: public, cmd: "pub -p -b=false -e news", want: "msg sent!"},
		{name: "pub a private topic of a public name", user: public, cmd: "pub -b=false -e news", denied: true},
		{name: "claim another topic", user: pub, cmd: "pub -b=false -e -a bob secret", denied: true},
		{name: "sub by a publisher", user: pub, cmd: "sub deploys", denied: true},
		{name: "sub another", user: sub, cmd: "sub secret", denied: true},
		{name: "sub wildcard", user: sub, cmd: "sub *", denied: true},
		{name: "pipe by a publisher", user: pub, cmd: "pipe deploys", denied: true},
		{name: "pipe by a subscriber", user: sub, cmd: "pipe deploys", denied: true},
		{name: "pipe another", user: both, cmd: "pipe secret", denied: true},
		{name: "monitor", user: all, cmd: "monitor deploys 1h", denied: true},
		{name: "monitor delete", user: all, cmd: "monitor deploys -d", denied: true},
		{name: "status", user: sub, cmd: "status", want: "alice/deploys", absent: "secret"},
		{name: "status of everything", user: all, cmd: "status", want: "alice/secret"},
		{name: "uptime another", user: sub, cmd: "uptime secret", denied: true},
		{name: "rss", user: sub, cmd: "rss", denied: true},
		{name: "rss of everything", user: all, cmd: "rss", want: "<rss"},
		{name: "help", user: sub, cmd: "help", want: "pubsub"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			client, err := c.user.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()

			out, err := c.user.RunCommand(client, c.cmd)
			if err != nil {
				t.Fatal(err)
			}
			if denied := strings.Contains(out, "this certificate doesn't allow"); denied != c.denied {
				t.Errorf("output = %q, want denied = %v", out, c.denied)
			}
			if !strings.Contains(out, c.want) || (c.absent != "" && strings.Contains(out, c.absent)) {
				t.Errorf("output = %q, want %q without %q", out, c.want, c.absent)
			}
		})
	}

	if _, ok := server.PipeHandler.Access.Load("alice/secret"); ok {
		t.Error("a refused publisher set an access list")
	}

	t.Run("sub receives", func(t *testing.T) {
		out := startAs(t, sub, "alice", "sub deploys")
		server.waitForClients(t, 1)
		client, err := pub.NewClient()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		if _, err := pub.RunCommandWithStdin(client, "pub deploys", "shipped"); err != nil {
			t.Fatal(err)
		}
		if got := readWithin(out, 2*time.Second); !strings.Contains(got, "shipped") {
			t.Errorf("subscriber read %q", got)
		}
	})

	t.Run("ls", func(t *testing.T) {
		startAs(t, alice, "alice", "sub secret")
		startAs(t, alice, "alice", "sub deploys")
		server.waitForClients(t, 2)
		client, err := sub.NewClient()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = client.Close() }()
		out, err := sub.RunCommand(client, "ls")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(out, "alice/deploys") || strings.Contains(out, "secret") {
			t.Errorf("ls = %q, want only deploys", out)
		}
	})

	t.Run("refused public topic", func(t *testing.T) {
		startAs(t, bob, "bob", "sub -p -a bob news")
		waitFor(t, func() bool { _, ok := server.PipeHandler.Access.Load("public/news"); return ok })
		news := alice.withCert(t, "pipe:pub:public/news", "pipe:sub:public/news")
		for _, cmd := range []string{"pub -p -b=false -e news", "pipe -p news"} {
			client, err := news.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			out, err := news.RunCommand(client, cmd)
			_ = client.Close()
			if err != nil || !strings.Contains(out, "this certificate doesn't allow") {
				t.Errorf("%s = %q, %v, want no other public topic in its place", cmd, out, err)
			}
		}
	})
}

// startOpen starts cmd with its stdin left open, which pipe needs.
func startOpen(t *testing.T, user UserSSH, cmd string) io.Reader {
	t.Helper()
	client, err := user.NewClient()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Close() })
	session, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.StdinPipe(); err != nil {
		t.Fatal(err)
	}
	out, err := session.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Start(cmd); err != nil {
		t.Fatal(err)
	}
	return out
}

// Scopes aren't patterns apart from a trailing *, so a wildcard subscription
// only gets the topics its scopes cover.
func TestScopedWildcards(t *testing.T) {
	cases := []struct {
		scopes []string
		cmd    string
		topic  string
		// before is whether the topic exists when the client subscribes.
		before bool
		want   bool
	}{
		{[]string{"pipe:sub:ci-*"}, "sub ci-*", "ci-build", false, true},
		{[]string{"pipe:sub:ci-*"}, "sub ci-*", "ci-build", true, true},
		{[]string{"pipe:sub:ci?*"}, "sub ci?*", "ci-secret", false, false},
		{[]string{"pipe:sub:ci?*"}, "sub ci?*", "ci-secret", true, false},
		{[]string{"pipe:sub:[a-z]*"}, "sub [a-z]*", "secret", false, false},
		{[]string{"pipe:sub"}, "sub ci?*", "ci-secret", false, true},
		{[]string{"pipe:sub:ci-*", "pipe:pub:ci-*"}, "pipe ci-*", "ci-build", false, true},
		{[]string{"pipe:sub:ci?*", "pipe:pub:ci?*"}, "pipe ci?*", "ci-secret", false, false},
	}
	for _, c := range cases {
		t.Run(c.cmd+" "+c.topic, func(t *testing.T) {
			server := NewTestSSHServer(t)
			defer server.Shutdown()
			alice := GenerateUser("alice")
			RegisterUserWithServer(server, alice)
			clients := 1
			if c.before {
				startAs(t, alice, "alice", "sub -k "+c.topic)
				server.waitForClients(t, 1)
				clients = 2
			}
			out := startOpen(t, alice.withCert(t, c.scopes...), c.cmd)
			server.waitForClients(t, clients)

			client, err := alice.NewClient()
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = client.Close() }()
			if _, err := alice.RunCommandWithStdin(client, "pub -b=false "+c.topic, "message"); err != nil {
				t.Fatal(err)
			}
			wait := 500 * time.Millisecond
			if c.want {
				wait = 5 * time.Second
			}
			if got := strings.Contains(readWithin(out, wait), "message"); got != c.want {
				t.Errorf("received = %v, want %v", got, c.want)
			}
		})
	}
}
