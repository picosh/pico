package pubkeys

import (
	"fmt"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	"golang.org/x/crypto/ssh"
)

func algo(keyType string) string {
	if idx := strings.Index(keyType, "@"); idx > 0 {
		return algo(keyType[0:idx])
	}
	parts := strings.Split(keyType, "-")
	if len(parts) == 2 {
		return parts[1]
	}
	if parts[0] == "sk" {
		return algo(strings.TrimPrefix(keyType, "sk-"))
	}
	return parts[0]
}

type Fingerprint struct {
	Type      string
	Value     string
	Algorithm string
	Styles    common.Styles
}

// String outputs a string representation of the fingerprint.
func (f Fingerprint) String() string {
	return fmt.Sprintf(
		"%s %s",
		f.Styles.ListKey.Render(strings.ToUpper(f.Algorithm)),
		f.Styles.ListKey.Render(f.Type+":"+f.Value),
	)
}

// FingerprintSHA256 returns the algorithm and SHA256 fingerprint for the given
// key.
func FingerprintSHA256(styles common.Styles, k *db.PublicKey) (Fingerprint, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key))
	if err != nil {
		return Fingerprint{}, fmt.Errorf("failed to parse public key: %w", err)
	}

	return Fingerprint{
		Algorithm: algo(key.Type()),
		Type:      "SHA256",
		Value:     strings.TrimPrefix(ssh.FingerprintSHA256(key), "SHA256:"),
		Styles:    styles,
	}, nil
}

// wrap fingerprint to support additional states.
type fingerprint struct {
	Fingerprint
}

func (f fingerprint) state(s keyState, styles common.Styles) string {
	if s == keyDeleting {
		return fmt.Sprintf(
			"%s %s",
			styles.Delete.Render(strings.ToUpper(f.Algorithm)),
			styles.Delete.Render(f.Type+":"+f.Value),
		)
	}
	return f.String()
}

type styledKey struct {
	styles       common.Styles
	date         string
	fingerprint  fingerprint
	gutter       string
	keyLabel     string
	dateLabel    string
	commentLabel string
	commentVal   string
	dateVal      string
	note         string
}

func (m Model) newStyledKey(styles common.Styles, key *db.PublicKey, active bool) styledKey {
	date := key.CreatedAt.Format(common.DateFormat)
	fp, err := FingerprintSHA256(styles, key)
	if err != nil {
		fp = Fingerprint{Value: "[error generating fingerprint]"}
	}

	var note string
	if active {
		note = m.shared.Styles.Note.Render("• Current Key")
	}

	// Default state
	return styledKey{
		styles:       styles,
		date:         date,
		fingerprint:  fingerprint{fp},
		gutter:       " ",
		keyLabel:     "Key:",
		dateLabel:    "Added:",
		commentLabel: "Name:",
		commentVal:   key.Name,
		dateVal:      styles.Label.Render(date),
		note:         note,
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateSelected)
	k.keyLabel = k.styles.Label.Render("Key:")
	k.dateLabel = k.styles.Label.Render("Added:")
	k.commentLabel = k.styles.Label.Render("Name:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(k.styles.Renderer, common.StateDeleting)
	k.keyLabel = k.styles.Delete.Render("Key:")
	k.dateLabel = k.styles.Delete.Render("Added:")
	k.commentLabel = k.styles.Delete.Render("Name:")
	k.dateVal = k.styles.Delete.Render(k.date)
}

func (k styledKey) render(state keyState) string {
	switch state {
	case keySelected:
		k.selected()
	case keyDeleting:
		k.deleting()
	}
	return fmt.Sprintf(
		"%s %s %s %s\n%s %s %s\n%s %s %s\n\n",
		k.gutter, k.commentLabel, k.commentVal, k.note,
		k.gutter, k.keyLabel, k.fingerprint.state(state, k.styles),
		k.gutter, k.dateLabel, k.dateVal,
	)
}
