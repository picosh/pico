package postfs

import (
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	sendutils "github.com/picosh/pico/pkg/send/utils"
)

func TestPostFromEntryLimit(t *testing.T) {
	user := &db.User{ID: "u"}
	entry := func(text string) *sendutils.FileEntry {
		return &sendutils.FileEntry{Filepath: "/a.md", Reader: strings.NewReader(text)}
	}
	if _, err := PostFromEntry(entry("12345"), user, "prose", 5); err != nil {
		t.Fatalf("file at the limit: %v", err)
	}
	if _, err := PostFromEntry(entry("123456"), user, "prose", 5); err == nil {
		t.Fatal("file over the limit accepted")
	}
}
