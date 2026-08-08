package pubsub

import (
	"bytes"
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestWildcardSubExistingAndNewTopics verifies that a subscriber with a wildcard topic
// (e.g., "metric-drain*") receives messages published to existing matching sub-topics
// AND any new matching sub-topics created AFTER the subscription was established.
func TestWildcardSubExistingAndNewTopics(t *testing.T) {
	cast := NewMulticast(slog.Default())

	subBuf := new(Buffer)
	subCtx, cancelSub := context.WithCancel(context.Background())
	defer cancelSub()

	// Wildcard subscription topic
	wildcardChannel := NewChannel("metric-drain*")

	var wg sync.WaitGroup

	// Start subscriber listening on wildcard topic "metric-drain*"
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = cast.Sub(subCtx, "sub-wildcard", subBuf, []*Channel{wildcardChannel}, false)
	}()

	time.Sleep(50 * time.Millisecond)

	// Publish to first topic matching wildcard: "metric-drain-pgs"
	channelPGS := NewChannel("metric-drain-pgs")
	pub1Ctx, cancelPub1 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub1()

	_ = cast.Pub(pub1Ctx, "pub-pgs", &Buffer{b: *bytes.NewBufferString("pgs-data\n")}, []*Channel{channelPGS}, false)

	// Publish to second topic matching wildcard: "metric-drain-prose"
	channelProse := NewChannel("metric-drain-prose")
	pub2Ctx, cancelPub2 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub2()

	_ = cast.Pub(pub2Ctx, "pub-prose", &Buffer{b: *bytes.NewBufferString("prose-data\n")}, []*Channel{channelProse}, false)

	// Publish to non-matching topic: "other-topic"
	channelOther := NewChannel("other-topic")
	pub3Ctx, cancelPub3 := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub3()

	_ = cast.Pub(pub3Ctx, "pub-other", &Buffer{b: *bytes.NewBufferString("other-data\n")}, []*Channel{channelOther}, false)

	// Wait briefly for dispatch
	time.Sleep(100 * time.Millisecond)

	// Stop subscriber
	cancelSub()
	wg.Wait()

	got := subBuf.String()

	if !bytes.Contains([]byte(got), []byte("pgs-data\n")) {
		t.Errorf("expected wildcard subscriber to receive pgs-data, got: %q", got)
	}
	if !bytes.Contains([]byte(got), []byte("prose-data\n")) {
		t.Errorf("expected wildcard subscriber to receive prose-data, got: %q", got)
	}
	if bytes.Contains([]byte(got), []byte("other-data\n")) {
		t.Errorf("wildcard subscriber should NOT receive other-data, got: %q", got)
	}
}
