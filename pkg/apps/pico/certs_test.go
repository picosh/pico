package pico

import (
	"context"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

// certsDB holds accounts' keys, access logs and revocations.
type certsDB struct {
	*stub.StubDB
	mu      sync.Mutex
	users   map[string]*db.User
	keys    []*db.PublicKey
	logs    []*db.AccessLog
	revoked []*db.CertRevocation
	logsErr error
	partial bool
	// revokedAtRead is how many entries there were when logs were last read.
	revokedAtRead int
}

func (d *certsDB) FindUserByPubkey(key string) (*db.User, error) {
	i := slices.IndexFunc(d.keys, func(k *db.PublicKey) bool { return k.Key == key })
	if i < 0 {
		return nil, errors.New("no user for key")
	}
	user := *d.users[d.keys[i].UserID]
	return &user, nil
}

func (d *certsDB) FindUserByKey(_, key string) (*db.User, error) { return d.FindUserByPubkey(key) }
func (d *certsDB) FindUser(id string) (*db.User, error)          { return d.users[id], nil }

func (d *certsDB) FindKeysByUser(user *db.User) ([]*db.PublicKey, error) {
	var keys []*db.PublicKey
	for _, k := range d.keys {
		if k.UserID == user.ID {
			keys = append(keys, k)
		}
	}
	return keys, nil
}

func (d *certsDB) InsertAccessLog(log *db.AccessLog) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	now := time.Now().Add(-2*time.Hour - time.Duration(len(d.logs))*time.Second)
	log.CreatedAt = &now
	d.logs = append(d.logs, log)
	return nil
}

func (d *certsDB) FindCertsInAccessLogs(userID string) ([]*db.LoggedCert, bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.revokedAtRead = len(d.revoked)
	var certs []*db.LoggedCert
	for _, l := range d.logs {
		if l.UserID == userID && !slices.ContainsFunc(certs, func(c *db.LoggedCert) bool { return c.Pubkey == l.Pubkey }) {
			certs = append(certs, &db.LoggedCert{Pubkey: l.Pubkey, LastSeen: l.CreatedAt})
		}
	}
	return certs, d.partial, d.logsErr
}

func (d *certsDB) IsCertRevoked(userID, fingerprint, keyID string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.ContainsFunc(d.revoked, func(r *db.CertRevocation) bool {
		return r.UserID == userID &&
			((r.Kind == db.CertRevocationKey && r.Value == fingerprint) || (r.Kind == db.CertRevocationID && r.Value == keyID))
	}), nil
}

func (d *certsDB) FindCertRevocations(userID string) ([]*db.CertRevocation, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var found []*db.CertRevocation
	for _, r := range d.revoked {
		if r.UserID == userID {
			found = append(found, r)
		}
	}
	return found, nil
}

func (d *certsDB) InsertCertRevocation(rev *db.CertRevocation) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if slices.ContainsFunc(d.revoked, func(r *db.CertRevocation) bool {
		return r.UserID == rev.UserID && r.Kind == rev.Kind && r.Value == rev.Value
	}) {
		return false, nil
	}
	now := time.Now()
	rev.CreatedAt = &now
	d.revoked = append(d.revoked, rev)
	return true, nil
}

func (d *certsDB) RemoveCertRevocation(userID, kind, value string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	n := len(d.revoked)
	d.revoked = slices.DeleteFunc(d.revoked, func(r *db.CertRevocation) bool {
		return r.UserID == userID && r.Kind == kind && r.Value == value
	})
	return len(d.revoked) < n, nil
}

func newSigner(t *testing.T) ssh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

func newCert(t *testing.T, ca ssh.Signer, key ssh.PublicKey, keyID string, principals ...string) *ssh.Certificate {
	t.Helper()
	cert := &ssh.Certificate{
		Key:             key,
		CertType:        ssh.UserCert,
		KeyId:           keyID,
		ValidPrincipals: principals,
		ValidBefore:     uint64(time.Date(2099, 2, 22, 0, 0, 0, 0, time.UTC).Unix()),
	}
	if err := cert.SignCert(rand.Reader, ca); err != nil {
		t.Fatal(err)
	}
	return cert
}

type certsEnv struct {
	*certsDB
	ca     ssh.Signer
	laptop ssh.Signer
}

func newCertsEnv(t *testing.T) *certsEnv {
	t.Helper()
	env := &certsEnv{
		certsDB: &certsDB{
			StubDB: stub.NewStubDB(slog.New(slog.DiscardHandler)),
			users:  map[string]*db.User{"1": {ID: "1", Name: "alice"}, "2": {ID: "2", Name: "bob"}},
		},
		ca:     newSigner(t),
		laptop: newSigner(t),
	}
	env.keys = []*db.PublicKey{
		{UserID: "1", Name: "ca", Key: shared.KeyForKeyText(env.ca.PublicKey())},
		{UserID: "1", Name: "laptop", Key: shared.KeyForKeyText(env.laptop.PublicKey())},
	}
	return env
}

func (e *certsEnv) log(t *testing.T, userID string, key ssh.PublicKey) {
	t.Helper()
	if err := e.InsertAccessLog(&db.AccessLog{UserID: userID, Pubkey: shared.KeyForKeyText(key)}); err != nil {
		t.Fatal(err)
	}
}

func (e *certsEnv) run(scopes shared.Scopes, args ...string) (string, error) {
	out := &fakeSession{}
	cmd := &Cmd{User: e.users["1"], Session: out, Log: slog.New(slog.DiscardHandler), Dbpool: e.certsDB, Scopes: scopes}
	err := cmd.certs(args)
	return out.String(), err
}

func TestCertsList(t *testing.T) {
	env := newCertsEnv(t)
	key := newSigner(t).PublicKey()
	fp := ssh.FingerprintSHA256(key)
	otherCA := newSigner(t)
	env.keys = append(env.keys, &db.PublicKey{UserID: "2", Name: "ca", Key: shared.KeyForKeyText(otherCA.PublicKey())})

	ok := newCert(t, env.ca, key, "ci-blog", "pgs:deploy:blog", "pipe")
	env.log(t, "1", ok)
	env.log(t, "1", ok)
	out, err := env.run(nil)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out), "\r\n")
	if len(lines) != 2 || !slices.Equal(strings.Fields(lines[0]), []string{"ID", "KEY", "SCOPES", "EXPIRES", "LAST", "SEEN"}) ||
		!slices.Equal(strings.Fields(lines[1]), []string{"ci-blog", fp, "pgs:deploy:blog,pipe", "2099-02-22", "2", "hours", "ago"}) {
		t.Errorf("certs = %q", out)
	}

	// Rows that aren't certs of the account's own CAs, whatever put them there.
	forged := newCert(t, newSigner(t), key, "forged", "admin")
	forged.SignatureKey = env.ca.PublicKey()
	expired := &ssh.Certificate{Key: key, CertType: ssh.UserCert, KeyId: "expired", ValidPrincipals: []string{"pgs"}, ValidBefore: 1}
	early := &ssh.Certificate{Key: key, CertType: ssh.UserCert, KeyId: "early", ValidPrincipals: []string{"pgs"}, ValidAfter: ssh.CertTimeInfinity >> 2, ValidBefore: ssh.CertTimeInfinity}
	host := &ssh.Certificate{Key: key, CertType: ssh.HostCert, KeyId: "host", ValidPrincipals: []string{"pgs"}, ValidBefore: ssh.CertTimeInfinity}
	for _, cert := range []*ssh.Certificate{expired, early, host} {
		if err := cert.SignCert(rand.Reader, env.ca); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []ssh.PublicKey{forged, expired, early, host, newCert(t, otherCA, key, "others-ca", "pgs"), newCert(t, newSigner(t), key, "unknown-ca", "pgs"), env.laptop.PublicKey()} {
		env.log(t, "1", row)
	}
	env.log(t, "2", newCert(t, otherCA, key, "bobs", "pgs"))
	for _, row := range []string{"", "ssh-ed25519-cert-v01@openssh.com AAAA", "\x1b[2J", "ssh-ed25519-cert-v01@openssh.com"} {
		_ = env.InsertAccessLog(&db.AccessLog{UserID: "1", Pubkey: row, Identity: "\x1b[2J"})
	}
	if again, err := env.run(nil); err != nil || again != out {
		t.Errorf("certs with other rows = %q, %v, want %q", again, err, out)
	}

	env.revoked = []*db.CertRevocation{{UserID: "2", Kind: db.CertRevocationID, Value: "ci-blog"}}
	if again, err := env.run(nil); err != nil || again != out {
		t.Errorf("certs revoked by another account = %q, %v, want %q", again, err, out)
	}
	env.revoked = []*db.CertRevocation{{UserID: "1", Kind: db.CertRevocationID, Value: "ci-blog"}}
	if out, err := env.run(nil); err != nil || strings.Contains(out, "ci-blog") {
		t.Errorf("certs shows a revoked cert: %q, %v", out, err)
	}
}

// TestCertsListRepeated lists a cert once, however its signature is encoded.
func TestCertsListRepeated(t *testing.T) {
	env := newCertsEnv(t)
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if env.ca, err = ssh.NewSignerFromKey(priv); err != nil {
		t.Fatal(err)
	}
	env.keys[0].Key = shared.KeyForKeyText(env.ca.PublicKey())

	cert := newCert(t, env.ca, newSigner(t).PublicKey(), "ci", "pgs")
	padded := *cert
	var sig struct{ R, S []byte }
	if err := ssh.Unmarshal(cert.Signature.Blob, &sig); err != nil {
		t.Fatal(err)
	}
	sig.R = append(make([]byte, 100), sig.R...)
	padded.Signature = &ssh.Signature{Format: cert.Signature.Format, Blob: ssh.Marshal(sig)}
	broken := *cert
	broken.Signature = &ssh.Signature{Format: cert.Signature.Format, Blob: ssh.Marshal(struct{ R, S []byte }{sig.S, sig.R})}
	if shared.KeyForKeyText(&padded) == shared.KeyForKeyText(cert) {
		t.Fatal("the certs are logged as one key")
	}
	for _, c := range []*ssh.Certificate{&broken, cert, &padded} {
		env.log(t, "1", c)
	}
	// Differs in what's signed.
	env.log(t, "1", newCert(t, env.ca, cert.Key, "ci", "pipe"))
	if out, err := env.run(nil); err != nil || strings.Count(out, "ci  ") != 2 || strings.Contains(out, "NOTICE") {
		t.Errorf("certs = %q, %v", out, err)
	}

	for _, c := range []*ssh.Certificate{cert, &padded} {
		if _, err := env.run(nil, "revoke", ssh.FingerprintSHA256(c)); err == nil || !strings.Contains(err.Error(), ssh.FingerprintSHA256(cert.Key)) {
			t.Errorf("the hash of one copy: %v, want the key's", err)
		}
	}

	env.partial = true
	for _, args := range [][]string{nil, {"revoke", "id:ci"}, {"revoke", "id:other"}} {
		if out, err := env.run(nil, args...); err != nil || !strings.Contains(out, "some aren't shown") {
			t.Errorf("certs %q = %q, %v, want a notice", args, out, err)
		}
	}
	env.logs = nil
	if out, err := env.run(nil); err != nil || !strings.Contains(out, "no certificates found") || !strings.Contains(out, "some aren't shown") {
		t.Errorf("certs = %q, %v, want a notice", out, err)
	}
}

func TestCertsPrintsUntrustedText(t *testing.T) {
	env := newCertsEnv(t)
	hostile := []string{
		"\x1b]0;title\x07\x1b[2J",
		"a\r\nforged  SHA256:x",
		"a\tb",
		"\u202egnp",
		"\u009b2J",
		"\xff\xfe",
		"two words",
		"a,admin",
		"",
	}
	for _, text := range hostile {
		env.log(t, "1", newCert(t, env.ca, newSigner(t).PublicKey(), text, "pgs:read:"+text, "pgs"))
	}
	env.revoked = []*db.CertRevocation{{UserID: "1", Kind: db.CertRevocationID, Value: hostile[0], Note: hostile[1]}}

	for _, args := range [][]string{nil, {"revoked"}, {"revoke", "id:a\r\nforged  SHA256:x"}, {"revoke", "id:\u202egnp"}, {"unrevoke", "id:" + hostile[0]}} {
		out, err := env.run(nil, args...)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"\x1b", "\x07", "\t", "\u202e", "\u009b", "\xff", "\nforged"} {
			if strings.Contains(out, bad) {
				t.Errorf("certs %q printed %q: %q", args, bad, out)
			}
		}
		if len(args) == 0 {
			if n := strings.Count(strings.TrimSpace(out), "\r\n"); n != len(hostile)-1 {
				t.Errorf("certs printed %d rows, want %d: %q", n, len(hostile)-1, out)
			}
			for _, want := range []string{`"two words"`, `"a,admin"`, `""`} {
				if !strings.Contains(out, want) {
					t.Errorf("certs doesn't show %s: %q", want, out)
				}
			}
		}
	}
	if _, err := env.run(nil, "revoke", "id:\x1b[2J\x00"); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %q", err)
	}
	if _, err := env.run(nil, "\x1b[2J"); err == nil || strings.Contains(err.Error(), "\x1b") {
		t.Errorf("err = %q", err)
	}
}

func TestCertsRevoke(t *testing.T) {
	env := newCertsEnv(t)
	key := newSigner(t).PublicKey()
	fp := ssh.FingerprintSHA256(key)
	cert := newCert(t, env.ca, key, "ci-blog", "pgs")
	env.log(t, "1", cert)

	out, err := env.run(nil, "revoke", fp)
	if err != nil || len(env.revoked) != 0 || !strings.Contains(out, "ci-blog") || !strings.Contains(out, "--write") {
		t.Errorf("revoke without --write = %q, %v, %d entries", out, err, len(env.revoked))
	}

	// Flags go on either side, and a padded fingerprint is the same one.
	out, err = env.run(nil, "revoke", "--write", fp+"=", "--note", "left on a bus", "")
	if err != nil || len(env.revoked) != 1 || *env.revoked[0] != (db.CertRevocation{UserID: "1", Kind: "key", Value: fp, Note: "left on a bus", CreatedAt: env.revoked[0].CreatedAt}) || strings.Contains(out, "WARNING") || strings.Contains(out, "NOTICE") {
		t.Errorf("revoke = %q, %v, %+v", out, err, env.revoked)
	}
	if env.revokedAtRead != 1 {
		t.Error("access logs were read before the entry was made")
	}
	if out, err = env.run(nil, "revoke", fp, "--write"); err != nil || len(env.revoked) != 1 || !strings.Contains(out, "already revoked") {
		t.Errorf("second revoke = %q, %v", out, err)
	}
	if out, err = env.run(nil, "revoked"); err != nil || !strings.Contains(out, fp) || !strings.Contains(out, `"left on a bus"`) {
		t.Errorf("revoked = %q, %v", out, err)
	}

	if out, err = env.run(nil, "unrevoke", fp); err != nil || len(env.revoked) != 1 || !strings.Contains(out, "--write") {
		t.Errorf("unrevoke without --write = %q, %v", out, err)
	}
	if out, err = env.run(nil, "unrevoke", fp, "--write"); err != nil || len(env.revoked) != 0 {
		t.Errorf("unrevoke = %q, %v", out, err)
	}
	for _, args := range [][]string{{"unrevoke", fp}, {"unrevoke", fp, "--write"}} {
		if _, err = env.run(nil, args...); err == nil {
			t.Errorf("%q of an entry that isn't there didn't fail", args)
		}
	}

	if out, err = env.run(nil, "revoke", "id:never offered", "--write"); err != nil || len(env.revoked) != 1 || env.revoked[0].Value != "never offered" || !strings.Contains(out, "no certificate in the access logs matches") {
		t.Errorf("revoke of an unseen cert = %q, %v", out, err)
	}
}

func TestCertsRevokeBadArgs(t *testing.T) {
	env := newCertsEnv(t)
	key := newSigner(t).PublicKey()
	fp := ssh.FingerprintSHA256(key)
	cert := newCert(t, env.ca, key, "ci", "pgs")
	env.log(t, "1", cert)

	for name, args := range map[string][]string{
		"no entry":            {"--write"},
		"two entries":         {fp, "id:ci", "--write"},
		"unquoted id":         {"id:alice", "smith", "--write"},
		"no kind":             {"ci", "--write"},
		"empty id":            {"id:", "--write"},
		"id with NUL":         {"id:a\x00", "--write"},
		"id that isn't utf8":  {"id:\xff", "--write"},
		"long id":             {"id:" + strings.Repeat("x", 1025), "--write"},
		"short hash":          {"SHA256:AAAA", "--write"},
		"not a hash":          {"SHA256:" + strings.Repeat("!", 43), "--write"},
		"other spelling":      {fp[:len(fp)-1] + "_", "--write"},
		"hash with a newline": {fp[:20] + "\n" + fp[20:], "--write"},
		"md5":                 {"MD5:00:11", "--write"},
		"a key":               {shared.KeyForKeyText(key), "--write"},
		"unknown flag":        {fp, "--wrote"},
		"long note":           {fp, "--write", "--note", strings.Repeat("x", 1025)},
	} {
		if out, err := env.run(nil, append([]string{"revoke"}, args...)...); err == nil || len(env.revoked) != 0 {
			t.Errorf("%s: %q, %v, %d entries", name, out, err, len(env.revoked))
		}
	}
	for _, args := range [][]string{{"revoke", ssh.FingerprintSHA256(cert)}, {"revoke", ssh.FingerprintSHA256(cert), "--write"}} {
		if _, err := env.run(nil, args...); err == nil || !strings.Contains(err.Error(), fp) {
			t.Errorf("%q, the cert's own hash: %v, want the key's", args, err)
		}
	}
}

func TestCertsRevokeWarnsOfPlainKeys(t *testing.T) {
	env := newCertsEnv(t)
	fp := ssh.FingerprintSHA256(env.laptop.PublicKey())
	env.log(t, "1", newCert(t, env.ca, env.laptop.PublicKey(), "laptop-cert", "pgs"))
	env.log(t, "1", newCert(t, env.ca, newSigner(t).PublicKey(), "ci", "pgs"))

	warning := "WARNING: " + fp + ` is one of your public keys ("laptop")`
	for _, args := range [][]string{{fp}, {fp, "--write"}, {"id:laptop-cert"}, {"id:laptop-cert", "--write"}} {
		if out, err := env.run(nil, append([]string{"revoke"}, args...)...); err != nil || !strings.Contains(out, warning) {
			t.Errorf("revoke %q = %q, %v, want %q", args, out, err, warning)
		}
	}
	if out, err := env.run(nil, "revoke", "id:ci"); err != nil || strings.Contains(out, "WARNING") {
		t.Errorf("revoke of another key = %q, %v", out, err)
	}

	// The key still logs in once its certs have expired.
	expired := &ssh.Certificate{Key: env.laptop.PublicKey(), CertType: ssh.UserCert, KeyId: "old", ValidPrincipals: []string{"pgs"}, ValidBefore: 1}
	if err := expired.SignCert(rand.Reader, env.ca); err != nil {
		t.Fatal(err)
	}
	env.log(t, "1", expired)
	if out, err := env.run(nil, "revoke", "id:old"); err != nil || !strings.Contains(out, warning) || !strings.Contains(out, "has expired") || strings.Contains(out, "1970") {
		t.Errorf("revoke of an expired cert = %q, %v, want %q", out, err, warning)
	}

	// An entry is made and warned about when the logs can't be read.
	env.revoked, env.logsErr = nil, errors.New("timed out")
	if out, err := env.run(nil, "revoke", fp, "--write"); err == nil || len(env.revoked) != 1 || !strings.Contains(out, "revoked "+fp) || !strings.Contains(out, warning) {
		t.Errorf("revoke without access logs = %q, %v", out, err)
	}
}

func TestCertsScoped(t *testing.T) {
	env := newCertsEnv(t)
	fp := ssh.FingerprintSHA256(env.laptop.PublicKey())
	entry := &db.CertRevocation{UserID: "1", Kind: db.CertRevocationKey, Value: fp}
	env.revoked = []*db.CertRevocation{entry}
	for _, scopes := range []shared.Scopes{{}, {{Role: "read"}}, {{Role: "pico"}, {Role: "admin"}}} {
		for _, args := range [][]string{
			{"revoke", "id:x", "--write"}, {"revoke", "id:x"}, {"unrevoke", fp, "--write"}, {"unrevoke", fp},
		} {
			if out, err := env.run(scopes, args...); !errors.Is(err, fs.ErrPermission) || len(env.revoked) != 1 || env.revoked[0] != entry {
				t.Errorf("%q with scopes %v = %q, %v, %+v", args, scopes, out, err, env.revoked)
			}
		}
	}
}

// TestCertsOverSSH logs in to a server set up as pico.sh is.
func TestCertsOverSSH(t *testing.T) {
	env := newCertsEnv(t)
	logger := slog.New(slog.DiscardHandler)
	ctx, cancel := context.WithCancel(t.Context())
	t.Cleanup(cancel)
	auth := shared.NewSshAuthHandler(env.certsDB, logger, "pico")
	server, err := pssh.NewSSHServerWithConfig(ctx, logger, "pico", "127.0.0.1", "0", "", filepath.Join(t.TempDir(), "host_key"),
		auth.OpenPubkeyAuthHandler,
		[]pssh.SSHServerMiddleware{
			Middleware(&CliHandler{DBPool: env.certsDB, Logger: logger}),
			pssh.LogMiddleware(NewUploadHandler(env.certsDB, &Config{}), env.certsDB),
		}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = server.ListenAndServe() }()
	addr := ""
	for deadline := time.Now().Add(5 * time.Second); addr == "" && time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		server.Mu.Lock()
		if server.Listener != nil {
			addr = server.Listener.Addr().String()
		}
		server.Mu.Unlock()
	}

	run := func(signer ssh.Signer, command string) (string, error) {
		client, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: "alice", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: ssh.InsecureIgnoreHostKey()})
		if err != nil {
			return "", fmt.Errorf("refused: %w", err)
		}
		defer func() { _ = client.Close() }()
		session, err := client.NewSession()
		if err != nil {
			return "", err
		}
		out, err := session.CombinedOutput(command)
		if missing := (*ssh.ExitMissingError)(nil); errors.As(err, &missing) {
			err = nil
		}
		return string(out), err
	}
	refused := func(err error) bool { return err != nil && strings.HasPrefix(err.Error(), "refused") }
	withCert := func(key ssh.Signer, keyID string, principals ...string) ssh.Signer {
		signer, err := ssh.NewCertSigner(newCert(t, env.ca, key.PublicKey(), keyID, principals...), key)
		if err != nil {
			t.Fatal(err)
		}
		return signer
	}

	key := newSigner(t)
	fp := ssh.FingerprintSHA256(key.PublicKey())
	first := withCert(key, "alice smith", "pico")
	if out, err := run(first, "certs"); err != nil || !strings.Contains(out, `"alice smith"`) || !strings.Contains(out, fp) {
		t.Fatalf("certs = %q, %v", out, err)
	}
	for _, principals := range [][]string{{"pico:read"}, {"pgs"}, {"pgs:deploy:blog"}} {
		if out, err := run(withCert(newSigner(t), "scoped", principals...), "certs revoke id:x --write"); !refused(err) {
			t.Errorf("%q cert logged in: %q, %v", principals, out, err)
		}
	}

	if out, err := run(env.laptop, `certs revoke id:"alice smith" --note "left the team" --write`); err != nil || len(env.revoked) != 1 || env.revoked[0].Value != "alice smith" || env.revoked[0].Note != "left the team" {
		t.Fatalf("revoke = %q, %v, %+v", out, err, env.revoked)
	}
	for name, signer := range map[string]ssh.Signer{"same cert": first, "same id": withCert(newSigner(t), "alice smith", "admin")} {
		if out, err := run(signer, "certs"); !refused(err) {
			t.Errorf("%s logged in: %q, %v", name, out, err)
		}
	}

	// The signed key on its own has no account.
	if out, err := run(key, "certs unrevoke id:\"alice smith\" --write"); err == nil || !strings.Contains(out, "need to create an account") || len(env.revoked) != 1 {
		t.Errorf("plain key = %q, %v", out, err)
	}
	renewed := withCert(key, "other", "pico")
	if out, err := run(renewed, "certs revoked"); err != nil || !strings.Contains(out, `id:"alice smith"`) {
		t.Errorf("another id = %q, %v", out, err)
	}
	if out, err := run(env.laptop, "certs revoke "+fp+" --write"); err != nil {
		t.Errorf("revoke by key = %q, %v", out, err)
	}
	if out, err := run(renewed, "certs"); !refused(err) {
		t.Errorf("revoked key logged in: %q, %v", out, err)
	}

	for _, entry := range []string{fp, `id:"alice smith"`} {
		if out, err := run(env.laptop, "certs unrevoke "+entry+" --write"); err != nil {
			t.Errorf("unrevoke = %q, %v", out, err)
		}
	}
	if out, err := run(first, "certs"); err != nil {
		t.Errorf("unrevoked cert = %q, %v", out, err)
	}
	if out, err := run(env.laptop, "certs revoke nonsense"); err == nil || !strings.Contains(out, "isn't a key's") {
		t.Errorf("bad entry = %q, %v", out, err)
	}
}
