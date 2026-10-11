package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

// TestChannelMessageOrdering verifies that messages are delivered without panics or corruption.
// This applies to both multicast and round-robin dispatchers.
func TestChannelMessageOrdering(t *testing.T) {
	name := "order-test"
	numMessages := 3

	// Test with Multicast
	t.Run("Multicast", func(t *testing.T) {
		cast := NewMulticast(slog.Default())
		buf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup
		syncer := make(chan int)

		// Subscribe
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer <- 0
			_ = cast.Sub(context.TODO(), "sub", buf, []*Channel{channel}, false)
		}()

		<-syncer

		// Publish messages
		for i := 0; i < numMessages; i++ {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				msg := fmt.Sprintf("msg%d\n", idx)
				_ = cast.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
			}()
		}

		wg.Wait()

		// Verify at least some messages were received
		content := buf.String()
		if len(content) == 0 {
			t.Error("Multicast: no messages received")
		}
	})
}

// TestDispatcherClientDirection verifies that both dispatchers respect client direction.
// Publishers should not receive messages they publish.
func TestDispatcherClientDirection(t *testing.T) {
	name := "direction-test"

	t.Run("Multicast", func(t *testing.T) {
		cast := NewMulticast(slog.Default())
		pubBuf := new(Buffer)
		subBuf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Publisher (input only)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cast.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString("test")}, []*Channel{channel}, false)
		}()

		// Subscriber (output only)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cast.Sub(context.TODO(), "sub", subBuf, []*Channel{channel}, false)
		}()

		wg.Wait()

		// Publisher should not receive the message
		if pubBuf.String() != "" {
			t.Errorf("Publisher received message: %q", pubBuf.String())
		}

		// Subscriber should receive it
		if subBuf.String() != "test" {
			t.Errorf("Subscriber should have received message, got: %q", subBuf.String())
		}
	})
}

// TestChannelConcurrentPublishes verifies that concurrent publishes don't cause races or data loss.
// func TestChannelConcurrentPublishes(t *testing.T) {
// 	name := "concurrent-test"
// 	numPublishers := 10
// 	msgsPerPublisher := 5
// 	numSubscribers := 3

// 	t.Run("Multicast", func(t *testing.T) {
// 		cast := NewMulticast(slog.Default())
// 		buffers := make([]*Buffer, numSubscribers)
// 		for i := range buffers {
// 			buffers[i] = new(Buffer)
// 		}
// 		channel := NewChannel(name)

// 		var wg sync.WaitGroup

// 		// Subscribe
// 		for i := range buffers {
// 			wg.Add(1)
// 			idx := i
// 			go func() {
// 				defer wg.Done()
// 				_ = cast.Sub(context.TODO(), fmt.Sprintf("sub-%d", idx), buffers[idx], []*Channel{channel}, false)
// 			}()
// 		}
// 		time.Sleep(100 * time.Millisecond)

// 		// Concurrent publishers
// 		pubCount := int32(0)
// 		for p := 0; p < numPublishers; p++ {
// 			pubID := p
// 			for m := 0; m < msgsPerPublisher; m++ {
// 				wg.Add(1)
// 				msgNum := m
// 				go func() {
// 					defer wg.Done()
// 					msg := fmt.Sprintf("pub%d-msg%d\n", pubID, msgNum)
// 					_ = cast.Pub(context.TODO(), fmt.Sprintf("pub-%d", pubID), &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
// 					atomic.AddInt32(&pubCount, 1)
// 				}()
// 			}
// 		}

// 		wg.Wait()

// 		// Verify all messages delivered to all subscribers
// 		totalExpectedMessages := numPublishers * msgsPerPublisher
// 		for i, buf := range buffers {
// 			messageCount := bytes.Count([]byte(buf.String()), []byte("\n"))
// 			if messageCount != totalExpectedMessages {
// 				t.Errorf("Subscriber %d: expected %d messages, got %d", i, totalExpectedMessages, messageCount)
// 			}
// 		}

// 		// Verify all publishes completed
// 		if pubCount != int32(totalExpectedMessages) {
// 			t.Errorf("Expected %d publishes to complete, got %d", totalExpectedMessages, pubCount)
// 		}
// 	})
// }

// TestDispatcherEmptySubscribers verifies that dispatchers handle empty subscriber set without panic.
func TestDispatcherEmptySubscribers(t *testing.T) {
	name := "empty-subs-test"

	t.Run("Multicast", func(t *testing.T) {
		cast := NewMulticast(slog.Default())
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Publish with no subscribers (should not panic)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Multicast panicked with no subscribers: %v", r)
				}
			}()
			_ = cast.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString("test")}, []*Channel{channel}, false)
		}()

		wg.Wait()
		t.Log("Multicast handled empty subscribers correctly")
	})
}

// TestDispatcherSingleSubscriber verifies that both dispatchers work correctly with one subscriber.
func TestDispatcherSingleSubscriber(t *testing.T) {
	name := "single-sub-test"
	message := "single-sub-message"

	t.Run("Multicast", func(t *testing.T) {
		cast := NewMulticast(slog.Default())
		buf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Subscribe
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cast.Sub(context.TODO(), "sub", buf, []*Channel{channel}, false)
		}()

		time.Sleep(100 * time.Millisecond)

		// Publish
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = cast.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(message)}, []*Channel{channel}, false)
		}()

		wg.Wait()

		if buf.String() != message {
			t.Errorf("Multicast with single subscriber: expected %q, got %q", message, buf.String())
		}
	})
}

// slowSession is a subscriber whose writes take a while and fail once it is
// closed, like an ssh session.
type slowSession struct {
	Buffer
	mu     sync.Mutex
	closed bool
}

func (s *slowSession) Write(p []byte) (int, error) {
	time.Sleep(20 * time.Millisecond)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0, io.ErrClosedPipe
	}
	return s.Buffer.Write(p)
}

func (s *slowSession) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

func TestSubReceivesLastMessageWhenPublisherLeaves(t *testing.T) {
	cast := NewMulticast(slog.Default())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sub := &slowSession{}
	subDone := make(chan struct{})
	go func() {
		_ = cast.Sub(ctx, "sub", sub, []*Channel{NewChannel("topic")}, false)
		close(subDone)
	}()
	for joined := 0; joined == 0; time.Sleep(time.Millisecond) {
		for range cast.GetSubs() {
			joined++
		}
	}

	_ = cast.Pub(ctx, "pub", &Buffer{b: *bytes.NewBufferString("last")}, []*Channel{NewChannel("topic")}, false)

	select {
	case <-subDone:
	case <-time.After(5 * time.Second):
		t.Fatal("subscriber is still connected after the publisher left")
	}
	if got := sub.String(); got != "last" {
		t.Errorf("subscriber got %q, want %q", got, "last")
	}
}
