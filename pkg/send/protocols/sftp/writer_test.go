package sftp

import (
	"math"
	"testing"
)

// A client-chosen offset or size can't make the buffer panic or allocate
// past its bound.
func TestBufferBounds(t *testing.T) {
	b := &buffer{}
	if _, err := b.WriteAt([]byte("x"), -1); err == nil {
		t.Error("negative offset accepted")
	}
	if _, err := b.WriteAt([]byte("x"), 1<<62); err == nil {
		t.Error("huge offset accepted")
	}
	if _, err := b.WriteAt([]byte("x"), math.MaxInt64); err == nil {
		t.Error("overflowing offset accepted")
	}
	if err := b.truncate(-1); err == nil {
		t.Error("negative truncate accepted")
	}
	if err := b.truncate(maxBufferSize + 1); err == nil {
		t.Error("huge truncate accepted")
	}

	if _, err := b.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatal(err)
	}
	if err := b.truncate(2); err != nil {
		t.Fatal(err)
	}
	if _, err := b.WriteAt([]byte("!"), 4); err != nil {
		t.Fatal(err)
	}
	if got := string(b.bytes()); got != "he\x00\x00!" {
		t.Fatalf("buffer = %q", got)
	}
}
