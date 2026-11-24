package pico

import (
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

const (
	certsUsage     = "certs [revoked | revoke <entry> [--note <note>] [--write] | unrevoke <entry> [--write]], where <entry> is a key's `SHA256:<fingerprint>` or `id:<key id>`"
	fingerprintPre = "SHA256:"
	keyIDPre       = "id:"
)

// accountCert is a cert signed by one of the account's CAs.
type accountCert struct {
	cert *ssh.Certificate
	// fingerprint is the signed key's.
	fingerprint string
	// hashes are the cert's own fingerprints, one per copy of it.
	hashes   []string
	lastSeen *time.Time
	// current is whether the cert is within its validity window.
	current bool
}

func (a *accountCert) matches(kind, value string) bool {
	if kind == db.CertRevocationKey {
		return a.fingerprint == value
	}
	return a.cert.KeyId == value
}

// accountKeys are the names of the account's public keys by fingerprint.
func (c *Cmd) accountKeys() (map[string]string, error) {
	keys, err := c.Dbpool.FindKeysByUser(c.User)
	if err != nil {
		return nil, fmt.Errorf("cannot load public keys: %w", err)
	}

	names := map[string]string{}
	for _, k := range keys {
		if pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key)); err == nil {
			names[ssh.FingerprintSHA256(pk)] = k.Name
		}
	}
	return names, nil
}

// accountCerts are the certs in the account's access logs that its CAs signed,
// expired or not, and whether some were left out. Nothing about a logged cert
// is trusted until it verifies, since a row doesn't mean a login succeeded.
func (c *Cmd) accountCerts(keys map[string]string) (certs []*accountCert, partial bool, err error) {
	logged, partial, err := c.Dbpool.FindCertsInAccessLogs(c.User.ID)
	if err != nil {
		return nil, false, fmt.Errorf("cannot load certificates from the access logs: %w", err)
	}

	now := time.Now().Unix()
	seen := map[string]*accountCert{}
	for _, l := range logged {
		cert, err := shared.ParseCert(l.Pubkey)
		if err != nil {
			continue
		}
		if _, ok := keys[ssh.FingerprintSHA256(cert.SignatureKey)]; !ok {
			continue
		}

		// One cert can be logged with differently encoded signatures.
		hash := ssh.FingerprintSHA256(cert)
		unsigned := *cert
		unsigned.Signature = nil
		id := string(unsigned.Marshal())
		if a, ok := seen[id]; ok {
			a.hashes = append(a.hashes, hash)
			continue
		}

		seen[id] = &accountCert{
			cert:        cert,
			fingerprint: ssh.FingerprintSHA256(cert.Key),
			lastSeen:    l.LastSeen,
			current:     now >= int64(cert.ValidAfter) && (cert.ValidBefore == ssh.CertTimeInfinity || now < int64(cert.ValidBefore)),
			hashes:      []string{hash},
		}
		certs = append(certs, seen[id])
	}
	return certs, partial, nil
}

// printable is text that can't be taken for anything else on the terminal:
// text with control characters, or that could be read as more than one
// column, is quoted.
func printable(s string) string {
	plain := s != "" && !strings.ContainsFunc(s, func(r rune) bool {
		return r == utf8.RuneError || !unicode.IsPrint(r) || unicode.IsSpace(r) || strings.ContainsRune(`,"'\`, r)
	})
	if plain {
		return s
	}
	return strconv.Quote(s)
}

func (c *Cmd) table(rows [][]string) {
	var out strings.Builder
	w := tabwriter.NewWriter(&out, 0, 0, 2, ' ', tabwriter.TabIndent)
	for _, row := range rows {
		_, _ = fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	_ = w.Flush()
	for line := range strings.Lines(out.String()) {
		c.output(strings.TrimRight(line, " \n"))
	}
}

func (c *Cmd) partialNotice() {
	c.output("NOTICE: the access logs hold more certificates than could be read, so some aren't shown")
}

func (c *Cmd) certTable(certs []*accountCert) {
	rows := [][]string{{"ID", "KEY", "SCOPES", "EXPIRES", "LAST SEEN"}}
	for _, a := range certs {
		principals := make([]string, 0, len(a.cert.ValidPrincipals))
		for _, p := range a.cert.ValidPrincipals {
			principals = append(principals, printable(p))
		}

		expires := "never"
		if a.cert.ValidBefore != ssh.CertTimeInfinity {
			expires = time.Unix(int64(a.cert.ValidBefore), 0).UTC().Format(time.DateOnly)
		}

		lastSeen := ""
		if a.lastSeen != nil {
			lastSeen = shared.TimeAgo(a.lastSeen)
		}

		rows = append(rows, []string{printable(a.cert.KeyId), a.fingerprint, strings.Join(principals, ","), expires, lastSeen})
	}
	c.table(rows)
}

func (c *Cmd) certs(args []string) error {
	args = slices.Clone(args)
	for len(args) > 0 && args[0] == "" {
		args = args[1:]
	}
	if len(args) == 0 {
		return c.certsList()
	}

	switch args[0] {
	case "revoked":
		return c.certsRevoked()
	case "revoke":
		return c.certsRevoke(args[1:])
	case "unrevoke":
		return c.certsUnrevoke(args[1:])
	default:
		return fmt.Errorf("unknown command %q, usage: %s", args[0], certsUsage)
	}
}

// certsList shows the certs that can still log in.
func (c *Cmd) certsList() error {
	keys, err := c.accountKeys()
	if err != nil {
		return err
	}
	certs, partial, err := c.accountCerts(keys)
	if err != nil {
		return err
	}
	revocations, err := c.Dbpool.FindCertRevocations(c.User.ID)
	if err != nil {
		return fmt.Errorf("cannot load revoked certificates: %w", err)
	}

	certs = slices.DeleteFunc(certs, func(a *accountCert) bool {
		return !a.current || slices.ContainsFunc(revocations, func(r *db.CertRevocation) bool { return a.matches(r.Kind, r.Value) })
	})
	if len(certs) == 0 {
		c.output("no certificates found in the access logs")
	} else {
		c.certTable(certs)
	}
	if partial {
		c.partialNotice()
	}
	return nil
}

func (c *Cmd) certsRevoked() error {
	revocations, err := c.Dbpool.FindCertRevocations(c.User.ID)
	if err != nil {
		return fmt.Errorf("cannot load revoked certificates: %w", err)
	}
	if len(revocations) == 0 {
		c.output("no certificates are revoked")
		return nil
	}

	rows := [][]string{{"REVOKED", "SINCE", "NOTE"}}
	for _, r := range revocations {
		since := ""
		if r.CreatedAt != nil {
			since = r.CreatedAt.Format(time.DateOnly)
		}
		note := ""
		if r.Note != "" {
			note = strconv.Quote(r.Note)
		}
		rows = append(rows, []string{entryName(r.Kind, r.Value), since, note})
	}
	c.table(rows)
	return nil
}

func entryName(kind, value string) string {
	if kind == db.CertRevocationKey {
		return printable(value)
	}
	return keyIDPre + printable(value)
}

// parseEntry reads what an entry revokes, in the form it's compared in.
func parseEntry(arg string) (kind, value string, err error) {
	if id, ok := strings.CutPrefix(arg, keyIDPre); ok {
		if !shared.RevocableKeyID(id) {
			return "", "", fmt.Errorf("key id %q can't be revoked, use the key's fingerprint", id)
		}
		return db.CertRevocationID, id, nil
	}

	if hash, ok := strings.CutPrefix(arg, fingerprintPre); ok {
		sum, err := base64.RawStdEncoding.Strict().DecodeString(strings.TrimSuffix(hash, "="))
		if err != nil || len(sum) != 32 || strings.ContainsAny(hash, "\r\n") {
			return "", "", fmt.Errorf("%q isn't a SHA256 fingerprint", arg)
		}
		return db.CertRevocationKey, fingerprintPre + base64.RawStdEncoding.EncodeToString(sum), nil
	}

	return "", "", fmt.Errorf("%q isn't a key's `SHA256:<fingerprint>` or `id:<key id>`", arg)
}

// entryArgs parses flags on either side of the one entry they apply to.
func (c *Cmd) entryArgs(flags *flag.FlagSet, args []string) (kind, value string, err error) {
	flags.SetOutput(io.Discard)
	flags.BoolVar(&c.Write, "write", false, "apply changes")

	var entries []string
	for len(args) > 0 {
		if err := flags.Parse(args); err != nil {
			return "", "", fmt.Errorf("%w, usage: %s", err, certsUsage)
		}
		if args = flags.Args(); len(args) > 0 {
			if args[0] != "" {
				entries = append(entries, args[0])
			}
			args = args[1:]
		}
	}

	// An unquoted id with a space would otherwise name another cert.
	if len(entries) != 1 {
		return "", "", fmt.Errorf("must provide one entry, quoted if it has spaces ('id:\"a b\"'), usage: %s", certsUsage)
	}
	return parseEntry(entries[0])
}

func (c *Cmd) notice() {
	if !c.Write {
		c.output("\r\nNOTICE: changes not committed, use `--write` to save operation")
	}
}

func (c *Cmd) certsRevoke(args []string) error {
	if err := c.Scopes.CheckFull("revoking certificates"); err != nil {
		return err
	}

	flags := flag.NewFlagSet("revoke", flag.ContinueOnError)
	note := flags.String("note", "", "why")
	kind, value, err := c.entryArgs(flags, args)
	if err != nil {
		return err
	}
	if !utf8.ValidString(*note) || len(*note) > 1024 || strings.Contains(*note, "\x00") {
		return fmt.Errorf("note must be text of at most 1024 bytes")
	}

	name := entryName(kind, value)
	if c.Write {
		added, err := c.Dbpool.InsertCertRevocation(&db.CertRevocation{UserID: c.User.ID, Kind: kind, Value: value, Note: *note})
		if err != nil {
			return fmt.Errorf("cannot revoke %s: %w", name, err)
		}
		c.Log.Info("revoked certs", "user", c.User.Name, "kind", kind, "value", value, "added", added)
		if added {
			c.output("revoked " + name)
		} else {
			c.output(name + " was already revoked")
		}
	} else {
		c.output("revoking " + name)
	}

	// The entry is made first, so that nothing in the logs can hold it up.
	var certs []*accountCert
	var partial bool
	keys, listErr := c.accountKeys()
	if listErr == nil {
		certs, partial, listErr = c.accountCerts(keys)
	}

	var matched, current []*accountCert
	for _, a := range certs {
		if a.matches(kind, value) {
			matched = append(matched, a)
			if a.current {
				current = append(current, a)
			}
		}
	}
	if len(matched) == 0 && kind == db.CertRevocationKey {
		for _, a := range certs {
			if slices.Contains(a.hashes, value) {
				return fmt.Errorf("%s is the certificate's own fingerprint, which refuses nothing, revoke its key: %s", value, a.fingerprint)
			}
		}
	}

	switch {
	case len(current) > 0:
		c.certTable(current)
	case len(matched) > 0:
		c.output("every certificate in the access logs that matches it has expired or isn't valid yet")
	case listErr == nil:
		c.output("no certificate in the access logs matches it")
	}
	if partial {
		c.partialNotice()
	}
	c.warnPlainKeys(kind, value, keys, matched)
	c.notice()
	if listErr != nil {
		return fmt.Errorf("cannot show what %s matches: %w", name, listErr)
	}
	return nil
}

// warnPlainKeys names the keys an entry covers that log in without a cert.
func (c *Cmd) warnPlainKeys(kind, value string, keys map[string]string, matched []*accountCert) {
	var fingerprints []string
	if kind == db.CertRevocationKey {
		fingerprints = append(fingerprints, value)
	}
	for _, a := range matched {
		fingerprints = append(fingerprints, a.fingerprint)
	}
	slices.Sort(fingerprints)

	for _, fp := range slices.Compact(fingerprints) {
		if name, ok := keys[fp]; ok {
			c.output(fmt.Sprintf("WARNING: %s is one of your public keys (%q), so it still logs in without a certificate. Remove it there too", fp, name))
		}
	}
}

func (c *Cmd) certsUnrevoke(args []string) error {
	if err := c.Scopes.CheckFull("unrevoking certificates"); err != nil {
		return err
	}

	kind, value, err := c.entryArgs(flag.NewFlagSet("unrevoke", flag.ContinueOnError), args)
	if err != nil {
		return err
	}

	name := entryName(kind, value)
	errMissing := errors.New(name + " isn't revoked")
	if !c.Write {
		revocations, err := c.Dbpool.FindCertRevocations(c.User.ID)
		if err != nil {
			return fmt.Errorf("cannot load revoked certificates: %w", err)
		}
		if !slices.ContainsFunc(revocations, func(r *db.CertRevocation) bool { return r.Kind == kind && r.Value == value }) {
			return errMissing
		}
		c.output("unrevoking " + name)
		c.notice()
		return nil
	}

	removed, err := c.Dbpool.RemoveCertRevocation(c.User.ID, kind, value)
	if err != nil {
		return fmt.Errorf("cannot unrevoke %s: %w", name, err)
	}
	if !removed {
		return errMissing
	}
	c.Log.Info("unrevoked certs", "user", c.User.Name, "kind", kind, "value", value)
	c.output("unrevoked " + name)
	return nil
}
