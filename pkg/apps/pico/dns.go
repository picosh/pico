package pico

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared/router"
	"golang.org/x/crypto/ssh"
)

// customDomainService is a service that serves a domain whose TXT record
// names it. check judges the records the way the service does and describes
// the result.
type customDomainService struct {
	space    string
	check    func(c *Cmd, records []string) string
	wildcard bool
}

var customDomainServices = []customDomainService{
	{space: "pgs", check: siteCheck(resolvePgsSite)},
	{space: "prose", check: siteCheck(resolveProseSite)},
	// tuns accepts a match under either prefix; _sish is what sish uses.
	{space: "tuns", check: checkTunsRecords, wildcard: true},
	{space: "sish", check: checkTunsRecords, wildcard: true},
}

// siteCheck checks a record whose value names a site. Like the services,
// it reads only the first record.
func siteCheck(resolve func(dbpool db.DB, value string) (*db.User, string, error)) func(c *Cmd, records []string) string {
	return func(c *Cmd, records []string) string {
		value := strings.TrimSpace(records[0])
		user, site, err := resolve(c.Dbpool, value)
		if err != nil {
			return fmt.Sprintf("%q is invalid: %v", value, err)
		}
		line := fmt.Sprintf("%q is valid, serving %s", value, site)
		if user.ID != c.User.ID {
			line += " (not your account)"
		}
		return line
	}
}

func resolvePgsSite(dbpool db.DB, value string) (*db.User, string, error) {
	props, err := router.GetProjectFromSubdomain(value)
	if err != nil {
		return nil, "", err
	}
	user, err := dbpool.FindUserByName(props.Username)
	if err != nil {
		return nil, "", fmt.Errorf("user %q not found", props.Username)
	}
	if _, err := dbpool.FindProjectByName(user.ID, props.ProjectName); err != nil {
		return user, "", fmt.Errorf("user %q has no project %q", user.Name, props.ProjectName)
	}
	return user, fmt.Sprintf("pgs project %q of %s", props.ProjectName, user.Name), nil
}

func resolveProseSite(dbpool db.DB, value string) (*db.User, string, error) {
	user, err := dbpool.FindUserByName(value)
	if err != nil {
		return nil, "", fmt.Errorf("user %q not found", value)
	}
	return user, fmt.Sprintf("prose blog of %s", user.Name), nil
}

// checkTunsRecords looks for a record holding the user's name or the SHA256
// fingerprint of one of their keys. tuns compares the whole record, so it
// must match exactly.
func checkTunsRecords(c *Cmd, records []string) string {
	keys, err := c.Dbpool.FindKeysByUser(c.User)
	if err != nil {
		return fmt.Sprintf("could not load your keys: %v", err)
	}
	names := map[string]string{}
	for _, k := range keys {
		pk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key))
		if err != nil {
			continue
		}
		names[ssh.FingerprintSHA256(pk)] = k.Name
	}

	for _, r := range records {
		if r == c.User.Name {
			return fmt.Sprintf("%q is valid, tunnels from any of your keys can use this domain", r)
		}
		if name, ok := names[r]; ok {
			return fmt.Sprintf("%q is valid, tunnels from your key %q can use this domain", r, name)
		}
	}
	for _, r := range records {
		trimmed := strings.TrimSpace(r)
		if _, ok := names[trimmed]; ok || trimmed == c.User.Name {
			return fmt.Sprintf("%q is invalid: it has surrounding whitespace, and tuns needs an exact match", r)
		}
	}
	quoted := make([]string, len(records))
	for i, r := range records {
		quoted[i] = fmt.Sprintf("%q", r)
	}
	return fmt.Sprintf("%s is invalid: no record is your username or the SHA256 fingerprint of one of your keys", strings.Join(quoted, ", "))
}

// dnsCheck reports, for each service, whether domain has a TXT record and
// whether the service would accept it.
func (c *Cmd) dnsCheck(domain string) error {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	base, wildcard := strings.CutPrefix(domain, "*.")
	if base == "" || strings.ContainsAny(base, "/:@ *") {
		return fmt.Errorf("expected a domain name like example.com or *.example.com")
	}
	lookup := c.lookupTXT
	if lookup == nil {
		lookup = router.LookupTXTRecords
	}

	host := domain
	if wildcard {
		// tuns verifies a wildcard by looking up a random name under it.
		label := make([]byte, 6)
		_, _ = rand.Read(label)
		host = hex.EncodeToString(label) + "." + base
	}

	for _, svc := range customDomainServices {
		name := router.TXTRecordName(host, svc.space)
		if wildcard && !svc.wildcard {
			c.output(fmt.Sprintf("%s: wildcard domains are not supported", router.TXTRecordName(domain, svc.space)))
			continue
		}
		records, err := lookup(host, svc.space)
		switch {
		case errors.Is(err, router.ErrNoTXTRecord):
			c.output(fmt.Sprintf("%s: no TXT record", name))
		case err != nil:
			c.output(fmt.Sprintf("%s: lookup failed: %v", name, err))
		default:
			c.output(fmt.Sprintf("%s: %s", name, svc.check(c, records)))
		}
	}
	c.output("pgs and prose remember a missing record for 30 seconds and a found record for 2 minutes. tuns checks when a tunnel connects.")
	return nil
}
