package postgres

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"golang.org/x/crypto/bcrypt"
)

func TestHashHttpPassMigration(t *testing.T) {
	cleanupTestData(t)
	conn, err := testDB.Db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	hashed, err := bcrypt.GenerateFromPassword([]byte("already"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", 73)
	rows := map[string]db.ProjectAcl{
		"plain":   {Type: "http-pass", Data: []string{"hunter2"}},
		"dollar":  {Type: "http-pass", Data: []string{"$2cool"}},
		"hashed":  {Type: "http-pass", Data: []string{string(hashed)}},
		"long":    {Type: "http-pass", Data: []string{long}},
		"empty":   {Type: "http-pass", Data: []string{}},
		"pubkeys": {Type: "pubkeys", Data: []string{"SHA256:abc"}},
	}

	_, err = conn.ExecContext(t.Context(), `CREATE TEMP TABLE projects (id text, acl jsonb)`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = conn.ExecContext(t.Context(), `DROP TABLE pg_temp.projects`) }()
	for id, acl := range rows {
		raw, err := json.Marshal(acl)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.ExecContext(t.Context(), `INSERT INTO projects VALUES ($1, $2)`, id, raw); err != nil {
			t.Fatal(err)
		}
	}

	migration, err := os.ReadFile("../../../sql/migrations/20261116_hash_http_pass_passwords.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := conn.ExecContext(t.Context(), string(migration)); err != nil {
			t.Fatal(err)
		}
	}

	got := map[string]db.ProjectAcl{}
	res, err := conn.QueryContext(t.Context(), `SELECT id, acl FROM projects`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Close() }()
	for res.Next() {
		var id string
		var raw []byte
		if err := res.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		var acl db.ProjectAcl
		if err := json.Unmarshal(raw, &acl); err != nil {
			t.Fatal(err)
		}
		got[id] = acl
	}
	if err := res.Err(); err != nil {
		t.Fatal(err)
	}

	for id, password := range map[string]string{"plain": "hunter2", "dollar": "$2cool", "long": long} {
		acl := got[id]
		if acl.Type != "http-pass" || len(acl.Data) != 1 {
			t.Fatalf("%s: acl %+v", id, acl)
		}
		if err := bcrypt.CompareHashAndPassword([]byte(acl.Data[0]), []byte(password)); err != nil {
			t.Errorf("%s: hash %q does not match: %v", id, acl.Data[0], err)
		}
		if cost, err := bcrypt.Cost([]byte(acl.Data[0])); err != nil || cost != bcrypt.DefaultCost {
			t.Errorf("%s: cost %d, %v", id, cost, err)
		}
	}
	for _, id := range []string{"hashed", "empty", "pubkeys"} {
		if a, b := got[id], rows[id]; a.Type != b.Type || strings.Join(a.Data, ",") != strings.Join(b.Data, ",") {
			t.Errorf("%s: changed to %+v", id, a)
		}
	}
}
