package postgres

import (
	"slices"

	"github.com/picosh/pico/pkg/db"
)

func (me *PsqlDB) IsCertRevoked(userID, fingerprint, keyID string) (bool, error) {
	var revoked bool
	err := me.Db.Get(&revoked, db.IsCertRevokedQuery, userID, fingerprint, keyID)
	return revoked, err
}

func (me *PsqlDB) FindCertRevocations(userID string) ([]*db.CertRevocation, error) {
	var revocations []*db.CertRevocation
	err := me.Db.Select(&revocations, `SELECT user_id, kind, value, note, created_at FROM cert_revocations WHERE user_id=$1 ORDER BY created_at DESC, kind, value`, userID)
	if err != nil {
		return nil, err
	}
	return revocations, nil
}

func (me *PsqlDB) InsertCertRevocation(rev *db.CertRevocation) (bool, error) {
	res, err := me.Db.Exec(
		`INSERT INTO cert_revocations (user_id, kind, value, note) VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		rev.UserID,
		rev.Kind,
		rev.Value,
		rev.Note,
	)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (me *PsqlDB) RemoveCertRevocation(userID, kind, value string) (bool, error) {
	res, err := me.Db.Exec(`DELETE FROM cert_revocations WHERE user_id=$1 AND kind=$2 AND value=$3`, userID, kind, value)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Anyone with a copy of an ECDSA-signed cert can pad its signature, so one cert
// can be logged under any number of keys of any size.
const (
	maxLoggedCerts    = 1000
	maxLoggedCertSize = 16 * 1024
)

// loggedCerts groups oversized keys into one empty row, and reads one row past
// the limit.
func (me *PsqlDB) loggedCerts(userID string) ([]*db.LoggedCert, error) {
	var certs []*db.LoggedCert
	err := me.Db.Select(
		&certs,
		`SELECT CASE WHEN octet_length(pubkey) <= $2 THEN pubkey ELSE '' END AS pubkey, MAX(created_at) AS last_seen
		FROM access_logs
		WHERE user_id=$1 AND pubkey LIKE '%-cert-v01@openssh.com %'
		GROUP BY 1 ORDER BY last_seen DESC LIMIT $3`,
		userID,
		maxLoggedCertSize,
		maxLoggedCerts+1,
	)
	if err != nil {
		return nil, err
	}
	return certs, nil
}

func (me *PsqlDB) FindCertsInAccessLogs(userID string) ([]*db.LoggedCert, bool, error) {
	certs, err := me.loggedCerts(userID)
	if err != nil {
		return nil, false, err
	}

	found := len(certs)
	certs = slices.DeleteFunc(certs, func(c *db.LoggedCert) bool { return c.Pubkey == "" })
	partial := len(certs) < found || len(certs) > maxLoggedCerts
	return certs[:min(len(certs), maxLoggedCerts)], partial, nil
}
