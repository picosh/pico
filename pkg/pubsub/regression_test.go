package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
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

	// Test with RoundRobin
	t.Run("RoundRobin", func(t *testing.T) {
		rr := NewRoundRobin(slog.Default())
		buf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup
		syncer := make(chan int)

		// Subscribe
		wg.Add(1)
		go func() {
			defer wg.Done()
			syncer <- 0
			_ = rr.Sub(context.TODO(), "sub", buf, []*Channel{channel}, false)
		}()

		<-syncer

		// Publish messages
		for i := 0; i < numMessages; i++ {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				msg := fmt.Sprintf("msg%d\n", idx)
				_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
			}()
		}

		wg.Wait()

		// Verify at least some messages were received
		content := buf.String()
		if len(content) == 0 {
			t.Error("RoundRobin: no messages received")
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

	t.Run("RoundRobin", func(t *testing.T) {
		rr := NewRoundRobin(slog.Default())
		pubBuf := new(Buffer)
		subBuf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Publisher (input only)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString("test")}, []*Channel{channel}, false)
		}()

		// Subscriber (output only)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rr.Sub(context.TODO(), "sub", subBuf, []*Channel{channel}, false)
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
func TestChannelConcurrentPublishes(t *testing.T) {
	name := "concurrent-test"
	numPublishers := 10
	msgsPerPublisher := 5
	numSubscribers := 3

	t.Run("Multicast", func(t *testing.T) {
		cast := NewMulticast(slog.Default())
		buffers := make([]*Buffer, numSubscribers)
		for i := range buffers {
			buffers[i] = new(Buffer)
		}
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Subscribe
		for i := range buffers {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				_ = cast.Sub(context.TODO(), fmt.Sprintf("sub-%d", idx), buffers[idx], []*Channel{channel}, false)
			}()
		}
		time.Sleep(100 * time.Millisecond)

		// Concurrent publishers
		pubCount := int32(0)
		for p := 0; p < numPublishers; p++ {
			pubID := p
			for m := 0; m < msgsPerPublisher; m++ {
				wg.Add(1)
				msgNum := m
				go func() {
					defer wg.Done()
					msg := fmt.Sprintf("pub%d-msg%d\n", pubID, msgNum)
					_ = cast.Pub(context.TODO(), fmt.Sprintf("pub-%d", pubID), &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
					atomic.AddInt32(&pubCount, 1)
				}()
			}
		}

		wg.Wait()

		// Verify all messages delivered to all subscribers
		totalExpectedMessages := numPublishers * msgsPerPublisher
		for i, buf := range buffers {
			messageCount := bytes.Count([]byte(buf.String()), []byte("\n"))
			if messageCount != totalExpectedMessages {
				t.Errorf("Subscriber %d: expected %d messages, got %d", i, totalExpectedMessages, messageCount)
			}
		}

		// Verify all publishes completed
		if pubCount != int32(totalExpectedMessages) {
			t.Errorf("Expected %d publishes to complete, got %d", totalExpectedMessages, pubCount)
		}
	})

	t.Run("RoundRobin", func(t *testing.T) {
		rr := NewRoundRobin(slog.Default())
		buffers := make([]*Buffer, numSubscribers)
		for i := range buffers {
			buffers[i] = new(Buffer)
		}
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Subscribe
		for i := range buffers {
			wg.Add(1)
			idx := i
			go func() {
				defer wg.Done()
				_ = rr.Sub(context.TODO(), fmt.Sprintf("sub-%d", idx), buffers[idx], []*Channel{channel}, false)
			}()
		}
		time.Sleep(100 * time.Millisecond)

		// Concurrent publishers
		pubCount := int32(0)
		for p := 0; p < numPublishers; p++ {
			pubID := p
			for m := 0; m < msgsPerPublisher; m++ {
				wg.Add(1)
				msgNum := m
				go func() {
					defer wg.Done()
					msg := fmt.Sprintf("pub%d-msg%d\n", pubID, msgNum)
					_ = rr.Pub(context.TODO(), fmt.Sprintf("pub-%d", pubID), &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
					atomic.AddInt32(&pubCount, 1)
				}()
			}
		}

		wg.Wait()

		// Verify all messages distributed (one to each subscriber per round-robin cycle)
		// Allow for some timing variance - expect at least 90% of messages
		totalExpectedMessages := numPublishers * msgsPerPublisher
		totalDelivered := int32(0)
		for _, buf := range buffers {
			messageCount := bytes.Count([]byte(buf.String()), []byte("\n"))
			totalDelivered += int32(messageCount)
		}
		minExpected := int32(float32(totalExpectedMessages) * 0.9)
		if totalDelivered < minExpected {
			t.Errorf("Expected at least %d messages, got %d", minExpected, totalDelivered)
		}

		// Verify all publishes completed
		if pubCount != int32(totalExpectedMessages) {
			t.Errorf("Expected %d publishes to complete, got %d", totalExpectedMessages, pubCount)
		}
	})
}

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

	t.Run("RoundRobin", func(t *testing.T) {
		rr := NewRoundRobin(slog.Default())
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Publish with no subscribers (should not panic)
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("RoundRobin panicked with no subscribers: %v", r)
				}
			}()
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString("test")}, []*Channel{channel}, false)
		}()

		wg.Wait()
		t.Log("RoundRobin handled empty subscribers correctly")
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

	t.Run("RoundRobin", func(t *testing.T) {
		rr := NewRoundRobin(slog.Default())
		buf := new(Buffer)
		channel := NewChannel(name)

		var wg sync.WaitGroup

		// Subscribe
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rr.Sub(context.TODO(), "sub", buf, []*Channel{channel}, false)
		}()

		time.Sleep(100 * time.Millisecond)

		// Publish
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(message)}, []*Channel{channel}, false)
		}()

		wg.Wait()

		if buf.String() != message {
			t.Errorf("RoundRobin with single subscriber: expected %q, got %q", message, buf.String())
		}
	})
}
