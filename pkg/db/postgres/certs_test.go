package postgres

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
)

func TestCertRevocations(t *testing.T) {
	cleanupTestData(t)
	alice, err := testDB.RegisterUser("certalice", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI certalice", "comment", "")
	if err != nil {
		t.Fatal(err)
	}
	bob, err := testDB.RegisterUser("certbob", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI certbob", "comment", "")
	if err != nil {
		t.Fatal(err)
	}

	insert := func(kind, value string) {
		t.Helper()
		if added, err := testDB.InsertCertRevocation(&db.CertRevocation{UserID: alice.ID, Kind: kind, Value: value, Note: "why"}); err != nil || !added {
			t.Fatalf("InsertCertRevocation = %v, %v", added, err)
		}
	}
	insert(db.CertRevocationKey, "SHA256:a")
	insert(db.CertRevocationID, "ci")

	for _, c := range []struct {
		name                string
		userID, fp, id      string
		want, wantUnrevoked bool
	}{
		{"key", alice.ID, "SHA256:a", "other", true, false},
		{"id", alice.ID, "SHA256:b", "ci", true, true},
		{"both", alice.ID, "SHA256:a", "ci", true, true},
		{"neither", alice.ID, "SHA256:b", "other", false, false},
		{"no id", alice.ID, "SHA256:b", "", false, false},
		{"id named like the key", alice.ID, "SHA256:b", "SHA256:a", false, false},
		{"key named like the id", alice.ID, "ci", "other", false, false},
		{"other case", alice.ID, "SHA256:A", "CI", false, false},
		{"pattern", alice.ID, "SHA256:%", "c%", false, false},
		{"other account", bob.ID, "SHA256:a", "ci", false, false},
	} {
		if got, err := testDB.IsCertRevoked(c.userID, c.fp, c.id); err != nil || got != c.want {
			t.Errorf("%s: IsCertRevoked = %v, %v, want %v", c.name, got, err, c.want)
		}
		defer func() {
			if got, err := testDB.IsCertRevoked(c.userID, c.fp, c.id); err != nil || got != c.wantUnrevoked {
				t.Errorf("%s: IsCertRevoked after the key is unrevoked = %v, %v, want %v", c.name, got, err, c.wantUnrevoked)
			}
		}()
	}

	if added, err := testDB.InsertCertRevocation(&db.CertRevocation{UserID: alice.ID, Kind: db.CertRevocationID, Value: "ci", Note: "again"}); err != nil || added {
		t.Errorf("second InsertCertRevocation = %v, %v", added, err)
	}
	revs, err := testDB.FindCertRevocations(alice.ID)
	if err != nil || len(revs) != 2 || revs[0].Note != "why" || revs[0].CreatedAt == nil {
		t.Errorf("FindCertRevocations = %+v, %v", revs, err)
	}
	if revs, err := testDB.FindCertRevocations(bob.ID); err != nil || len(revs) != 0 {
		t.Errorf("FindCertRevocations of another account = %+v, %v", revs, err)
	}

	for _, rev := range []db.CertRevocation{{Kind: db.CertRevocationID}, {Kind: "any", Value: "x"}} {
		rev.UserID = alice.ID
		if _, err := testDB.InsertCertRevocation(&rev); err == nil {
			t.Errorf("inserted %+v", rev)
		}
	}
	// The longest id the cert commands accept fits the index.
	insert(db.CertRevocationID, strings.Repeat("\U0001f511", 256))

	if removed, err := testDB.RemoveCertRevocation(bob.ID, db.CertRevocationKey, "SHA256:a"); err != nil || removed {
		t.Errorf("another account's RemoveCertRevocation = %v, %v", removed, err)
	}
	if removed, err := testDB.RemoveCertRevocation(alice.ID, db.CertRevocationID, "SHA256:a"); err != nil || removed {
		t.Errorf("another kind's RemoveCertRevocation = %v, %v", removed, err)
	}
	if removed, err := testDB.RemoveCertRevocation(alice.ID, db.CertRevocationKey, "SHA256:a"); err != nil || !removed {
		t.Errorf("RemoveCertRevocation = %v, %v", removed, err)
	}
	if removed, err := testDB.RemoveCertRevocation(alice.ID, db.CertRevocationKey, "SHA256:a"); err != nil || removed {
		t.Errorf("second RemoveCertRevocation = %v, %v", removed, err)
	}
}

func TestIsCertRevoked_FailsWithoutTable(t *testing.T) {
	cleanupTestData(t)
	if _, err := testDB.Db.Exec(`ALTER TABLE cert_revocations RENAME TO cert_revocations_away`); err != nil {
		t.Fatal(err)
	}
	defer testDB.Db.MustExec(`ALTER TABLE cert_revocations_away RENAME TO cert_revocations`)
	if _, err := testDB.IsCertRevoked("5f3b0f0e-0000-4000-8000-000000000000", "SHA256:a", "ci"); err == nil {
		t.Error("IsCertRevoked without its table didn't fail")
	}
}

func TestFindCertsInAccessLogs(t *testing.T) {
	cleanupTestData(t)
	alice, _ := testDB.RegisterUser("loggedalice", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI loggedalice", "comment", "")
	bob, _ := testDB.RegisterUser("loggedbob", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI loggedbob", "comment", "")
	const cert = "ssh-ed25519-cert-v01@openssh.com "
	for _, l := range []struct{ userID, pubkey string }{
		{alice.ID, cert + "AAAA"},
		{alice.ID, "sk-" + cert + "BBBB"},
		{alice.ID, cert + "AAAA"},
		{alice.ID, "ssh-ed25519 AAAA"},
		{alice.ID, ""},
		{bob.ID, cert + "CCCC"},
	} {
		if err := testDB.InsertAccessLog(&db.AccessLog{UserID: l.userID, Service: "pgs", Pubkey: l.pubkey}); err != nil {
			t.Fatal(err)
		}
	}

	certs, partial, err := testDB.FindCertsInAccessLogs(alice.ID)
	if err != nil || partial {
		t.Fatal(partial, err)
	}
	var got []string
	for _, c := range certs {
		got = append(got, c.Pubkey)
		if c.LastSeen == nil {
			t.Errorf("%s has no time", c.Pubkey)
		}
	}
	if want := []string{cert + "AAAA", "sk-" + cert + "BBBB"}; !slices.Equal(got, want) {
		t.Errorf("FindCertsInAccessLogs = %q, want %q", got, want)
	}
}

func TestFindCertsInAccessLogsLimits(t *testing.T) {
	cleanupTestData(t)
	alice, _ := testDB.RegisterUser("limitsalice", "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI limitsalice", "comment", "")
	const cert = "ssh-ed25519-cert-v01@openssh.com "
	insert := func(pubkey string, age int) {
		t.Helper()
		if _, err := testDB.Db.Exec(`INSERT INTO access_logs (user_id, service, pubkey, created_at) VALUES ($1, 'pgs', $2, NOW() - make_interval(secs => $3))`, alice.ID, pubkey, age); err != nil {
			t.Fatal(err)
		}
	}
	find := func() ([]*db.LoggedCert, bool) {
		t.Helper()
		certs, partial, err := testDB.FindCertsInAccessLogs(alice.ID)
		if err != nil {
			t.Fatal(err)
		}
		return certs, partial
	}

	largest := cert + strings.Repeat("A", maxLoggedCertSize-len(cert))
	insert(largest, 5000)
	if certs, partial := find(); partial || len(certs) != 1 || certs[0].Pubkey != largest {
		t.Errorf("largest cert: %d certs, partial %v", len(certs), partial)
	}
	insert(largest+"A", 1)
	insert(largest+"B", 2)
	if certs, partial := find(); !partial || len(certs) != 1 || certs[0].Pubkey != largest {
		t.Errorf("oversized certs: %d certs, partial %v", len(certs), partial)
	}

	if _, err := testDB.Db.Exec(`DELETE FROM access_logs WHERE octet_length(pubkey) > $1`, maxLoggedCertSize); err != nil {
		t.Fatal(err)
	}
	for i := range maxLoggedCerts - 1 {
		insert(fmt.Sprintf("%s%04d", cert, i), i)
	}
	if certs, partial := find(); partial || len(certs) != maxLoggedCerts {
		t.Errorf("at the limit: %d certs, partial %v", len(certs), partial)
	}
	insert(cert+"one more", 6000)
	insert(cert+"another", 6001)
	if rows, err := testDB.loggedCerts(alice.ID); err != nil || len(rows) != maxLoggedCerts+1 {
		t.Errorf("read %d rows, %v, want %d", len(rows), err, maxLoggedCerts+1)
	}
	certs, partial := find()
	if !partial || len(certs) != maxLoggedCerts || certs[0].Pubkey != cert+"0000" || certs[len(certs)-1].Pubkey != largest {
		t.Errorf("over the limit: %d certs, partial %v", len(certs), partial)
	}
}
