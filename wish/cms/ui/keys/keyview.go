package keys

import (
	"fmt"
	"strings"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/wish/cms/ui/common"
	"golang.org/x/crypto/ssh"
)

var styles = common.DefaultStyles()

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
}

// String outputs a string representation of the fingerprint.
func (f Fingerprint) String() string {
	return fmt.Sprintf(
		"%s %s",
		styles.ListDim.Render(strings.ToUpper(f.Algorithm)),
		styles.ListKey.Render(f.Type+":"+f.Value),
	)
}

// FingerprintSHA256 returns the algorithm and SHA256 fingerprint for the given
// key.
func FingerprintSHA256(k *db.PublicKey) (Fingerprint, error) {
	key, _, _, _, err := ssh.ParseAuthorizedKey([]byte(k.Key))
	if err != nil {
		return Fingerprint{}, fmt.Errorf("failed to parse public key: %w", err)
	}

	return Fingerprint{
		Algorithm: algo(key.Type()),
		Type:      "SHA256",
		Value:     strings.TrimPrefix(ssh.FingerprintSHA256(key), "SHA256:"),
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
			styles.DeleteDim.Render(strings.ToUpper(f.Algorithm)),
			styles.Delete.Render(f.Type+":"+f.Value),
		)
	}
	return f.String()
}

type styledKey struct {
	styles      common.Styles
	date        string
	fingerprint fingerprint
	gutter      string
	keyLabel    string
	dateLabel   string
	dateVal     string
	note        string
}

func (m Model) newStyledKey(styles common.Styles, key *db.PublicKey, active bool) styledKey {
	date := key.CreatedAt.Format("02 Jan 2006 15:04:05 MST")
	fp, err := FingerprintSHA256(key)
	if err != nil {
		fp = Fingerprint{Value: "[error generating fingerprint]"}
	}

	var note string
	if active {
		note = m.styles.NoteDim.Render("• ") + m.styles.Note.Render("Current Key")
	}

	// Default state
	return styledKey{
		styles:      styles,
		date:        date,
		fingerprint: fingerprint{fp},
		gutter:      " ",
		keyLabel:    "Key:",
		dateLabel:   "Added:",
		dateVal:     styles.LabelDim.Render(date),
		note:        note,
	}
}

// Selected state.
func (k *styledKey) selected() {
	k.gutter = common.VerticalLine(common.StateSelected)
	k.keyLabel = k.styles.Label.Render("Key:")
	k.dateLabel = k.styles.Label.Render("Added:")
}

// Deleting state.
func (k *styledKey) deleting() {
	k.gutter = common.VerticalLine(common.StateDeleting)
	k.keyLabel = k.styles.Delete.Render("Key:")
	k.dateLabel = k.styles.Delete.Render("Added:")
	k.dateVal = k.styles.DeleteDim.Render(k.date)
}

func (k styledKey) render(state keyState) string {
	switch state {
	case keySelected:
		k.selected()
	case keyDeleting:
		k.deleting()
	}
	return fmt.Sprintf(
		"%s %s %s\n%s %s %s %s\n\n",
		k.gutter, k.keyLabel, k.fingerprint.state(state, k.styles),
		k.gutter, k.dateLabel, k.dateVal, k.note,
	)
}
