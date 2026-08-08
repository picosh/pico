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

// TestWildcardSubMultipleSubscribers verifies that multiple wildcard subscribers
// listening on the same pattern both receive published messages.
func TestWildcardSubMultipleSubscribers(t *testing.T) {
	cast := NewMulticast(slog.Default())

	subBuf1 := new(Buffer)
	subBuf2 := new(Buffer)
	subCtx, cancelSub := context.WithCancel(context.Background())
	defer cancelSub()

	wildcardChannel := NewChannel("logs-*")

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = cast.Sub(subCtx, "sub-1", subBuf1, []*Channel{wildcardChannel}, false)
	}()

	go func() {
		defer wg.Done()
		_ = cast.Sub(subCtx, "sub-2", subBuf2, []*Channel{wildcardChannel}, false)
	}()

	time.Sleep(50 * time.Millisecond)

	channel := NewChannel("logs-app1")
	pubCtx, cancelPub := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub()

	_ = cast.Pub(pubCtx, "pub-1", &Buffer{b: *bytes.NewBufferString("app1-log\n")}, []*Channel{channel}, false)

	time.Sleep(100 * time.Millisecond)
	cancelSub()
	wg.Wait()

	if subBuf1.String() != "app1-log\n" {
		t.Errorf("sub-1 expected app1-log, got %q", subBuf1.String())
	}
	if subBuf2.String() != "app1-log\n" {
		t.Errorf("sub-2 expected app1-log, got %q", subBuf2.String())
	}
}

// TestWildcardSubVariousPatterns verifies prefix, suffix, and middle asterisk wildcard matching.
func TestWildcardSubVariousPatterns(t *testing.T) {
	cast := NewMulticast(slog.Default())

	prefixBuf := new(Buffer)
	suffixBuf := new(Buffer)
	middleBuf := new(Buffer)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(3)

	go func() {
		defer wg.Done()
		_ = cast.Sub(ctx, "sub-prefix", prefixBuf, []*Channel{NewChannel("metric-*")}, false)
	}()
	go func() {
		defer wg.Done()
		_ = cast.Sub(ctx, "sub-suffix", suffixBuf, []*Channel{NewChannel("*-drain")}, false)
	}()
	go func() {
		defer wg.Done()
		_ = cast.Sub(ctx, "sub-middle", middleBuf, []*Channel{NewChannel("metric-*-drain")}, false)
	}()

	time.Sleep(50 * time.Millisecond)

	pubCtx, cancelPub := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub()

	// Publish to metric-app-drain
	_ = cast.Pub(pubCtx, "pub", &Buffer{b: *bytes.NewBufferString("event\n")}, []*Channel{NewChannel("metric-app-drain")}, false)

	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	if prefixBuf.String() != "event\n" {
		t.Errorf("prefix subscriber expected event, got %q", prefixBuf.String())
	}
	if suffixBuf.String() != "event\n" {
		t.Errorf("suffix subscriber expected event, got %q", suffixBuf.String())
	}
	if middleBuf.String() != "event\n" {
		t.Errorf("middle subscriber expected event, got %q", middleBuf.String())
	}
}

// TestWildcardSubLiteralCharsWithoutStar verifies that '?' or '[' without '*' are treated as literal names.
func TestWildcardSubLiteralCharsWithoutStar(t *testing.T) {
	cast := NewMulticast(slog.Default())

	subBuf := new(Buffer)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(1)

	// Subscribe to a topic with a literal '?' character
	go func() {
		defer wg.Done()
		_ = cast.Sub(ctx, "sub-literal", subBuf, []*Channel{NewChannel("topic?one")}, false)
	}()

	time.Sleep(50 * time.Millisecond)

	pubCtx, cancelPub := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancelPub()

	// Publish to topicXone (should NOT match because '?' is not treated as a wildcard)
	_ = cast.Pub(pubCtx, "pub", &Buffer{b: *bytes.NewBufferString("data\n")}, []*Channel{NewChannel("topicXone")}, false)

	time.Sleep(100 * time.Millisecond)
	cancel()
	wg.Wait()

	if subBuf.String() != "" {
		t.Errorf("literal subscriber should not have matched topicXone, got: %q", subBuf.String())
	}
}
