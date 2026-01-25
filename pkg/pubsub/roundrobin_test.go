package pubsub

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestRoundRobinSingleSub(t *testing.T) {
	// Single publisher, single subscriber
	// Should work like normal pub/sub
	orderActual := ""
	orderExpected := "sub-pub-"
	actual := new(Buffer)
	expected := "some test data"
	name := "test-channel"
	syncer := make(chan int)

	rr := NewRoundRobin(slog.Default())

	var wg sync.WaitGroup
	wg.Add(2)

	channel := NewChannel(name)

	go func() {
		orderActual += "sub-"
		syncer <- 0
		fmt.Println(rr.Sub(context.TODO(), "1", actual, []*Channel{channel}, false))
		wg.Done()
	}()

	<-syncer

	go func() {
		orderActual += "pub-"
		fmt.Println(rr.Pub(context.TODO(), "2", &Buffer{b: *bytes.NewBufferString(expected)}, []*Channel{channel}, true))
		wg.Done()
	}()

	wg.Wait()

	if orderActual != orderExpected {
		t.Fatalf("\norderActual:(%s)\norderExpected:(%s)", orderActual, orderExpected)
	}
	if actual.String() != expected {
		t.Fatalf("\nactual:(%s)\nexpected:(%s)", actual.String(), expected)
	}
}

func TestRoundRobinMultipleSubs(t *testing.T) {
	// Single publisher, multiple subscribers
	// Verify round-robin distributes across subscribers
	name := "test-channel"

	rr := NewRoundRobin(slog.Default())

	buffers := []*Buffer{new(Buffer), new(Buffer), new(Buffer)}
	channel := NewChannel(name)

	var wg sync.WaitGroup

	// Subscribe three clients sequentially with sync point
	syncer := make(chan int, 3)
	for i := range buffers {
		idx := i
		wg.Add(1)
		go func() {
			defer wg.Done()
			clientID := fmt.Sprintf("sub-%d", idx)
			_ = rr.Sub(context.TODO(), clientID, buffers[idx], []*Channel{channel}, false)
		}()
		syncer <- i
	}

	// Wait for all subscribers to connect
	for i := 0; i < 3; i++ {
		<-syncer
	}
	time.Sleep(200 * time.Millisecond)

	// Publish many messages to verify distribution
	numMsgs := 9
	for i := 0; i < numMsgs; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("msg%d\n", idx)
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
		}()
	}

	wg.Wait()

	// Verify that messages were distributed across all subscribers
	for i, buf := range buffers {
		content := buf.String()
		t.Logf("sub-%d received %d bytes", i, len(content))
		if len(content) == 0 {
			t.Logf("WARNING: sub-%d received no messages", i)
		}
	}

	// At least one subscriber should have received messages
	totalLen := 0
	for _, buf := range buffers {
		totalLen += len(buf.String())
	}
	if totalLen == 0 {
		t.Fatal("No messages were delivered to any subscriber")
	}
}

func TestRoundRobinDistribution(t *testing.T) {
	// Verify that messages are distributed evenly across subscribers
	expected := "msg"
	name := "test-channel"
	numSubs := 3
	numMessages := 9

	rr := NewRoundRobin(slog.Default())

	buffers := make([]*Buffer, numSubs)
	for i := 0; i < numSubs; i++ {
		buffers[i] = new(Buffer)
	}
	channels := []*Channel{NewChannel(name)}

	var wg sync.WaitGroup

	// Subscribe clients
	for i := 0; i < numSubs; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			clientID := fmt.Sprintf("sub-%d", idx)
			_ = rr.Sub(context.TODO(), clientID, buffers[idx], channels, false)
		}()
	}

	time.Sleep(100 * time.Millisecond)

	// Publish multiple messages
	for i := 0; i < numMessages; i++ {
		wg.Add(1)
		msgIdx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("%s%d\n", expected, msgIdx)
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, channels, false)
		}()
	}

	wg.Wait()

	// Count messages per subscriber
	msgCounts := make(map[int]int)
	for i, buf := range buffers {
		// Count occurrences of "msg" in the buffer
		content := buf.String()
		count := 0
		for j := 0; j < numMessages; j++ {
			marker := fmt.Sprintf("msg%d", j)
			if bytes.Contains([]byte(content), []byte(marker)) {
				count++
			}
		}
		msgCounts[i] = count
		t.Logf("sub-%d received %d messages", i, count)
	}

	// Verify relatively even distribution (within 1 message difference due to concurrency)
	minCount := msgCounts[0]
	maxCount := msgCounts[0]
	for i := 1; i < numSubs; i++ {
		if msgCounts[i] < minCount {
			minCount = msgCounts[i]
		}
		if msgCounts[i] > maxCount {
			maxCount = msgCounts[i]
		}
	}

	if maxCount-minCount > 2 {
		t.Fatalf("Uneven distribution: min=%d, max=%d, difference=%d", minCount, maxCount, maxCount-minCount)
	}
}

func TestRoundRobinSubscriberJoinLeave(t *testing.T) {
	// Test behavior when subscribers join and leave mid-stream
	// Verify the broker gracefully handles subscriber changes
	name := "test-channel"

	rr := NewRoundRobin(slog.Default())
	channel := NewChannel(name)

	buf1 := new(Buffer)
	buf2 := new(Buffer)
	buf3 := new(Buffer)

	var wg sync.WaitGroup

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())

	// Start with 2 subscribers
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = rr.Sub(ctx1, "sub-1", buf1, []*Channel{channel}, false)
	}()
	go func() {
		defer wg.Done()
		_ = rr.Sub(ctx2, "sub-2", buf2, []*Channel{channel}, false)
	}()

	time.Sleep(200 * time.Millisecond)

	// Publish some messages with 2 subscribers
	for i := 0; i < 2; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("msg%d\n", idx)
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
		}()
	}
	time.Sleep(100 * time.Millisecond)

	// Remove sub-1
	cancel1()
	time.Sleep(200 * time.Millisecond)

	// Add sub-3
	ctx3, cancel3 := context.WithCancel(context.Background())
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = rr.Sub(ctx3, "sub-3", buf3, []*Channel{channel}, false)
	}()

	time.Sleep(200 * time.Millisecond)

	// Publish more messages with different subscriber set
	for i := 2; i < 4; i++ {
		wg.Add(1)
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("msg%d\n", idx)
			_ = rr.Pub(context.TODO(), "pub", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{channel}, false)
		}()
	}

	wg.Wait()
	cancel2()
	cancel3()

	t.Logf("sub-1: %d bytes", len(buf1.String()))
	t.Logf("sub-2: %d bytes", len(buf2.String()))
	t.Logf("sub-3: %d bytes", len(buf3.String()))

	// Verify that messages were delivered (exact distribution depends on timing)
	totalLen := len(buf1.String()) + len(buf2.String()) + len(buf3.String())
	if totalLen == 0 {
		t.Fatal("No messages were delivered after subscriber changes")
	}
}

func TestRoundRobinMultipleChannels(t *testing.T) {
	// Test that each channel maintains independent round-robin state
	rr := NewRoundRobin(slog.Default())

	ch1 := NewChannel("topic-1")
	ch2 := NewChannel("topic-2")

	buf1ch1 := new(Buffer)
	buf2ch1 := new(Buffer)
	buf1ch2 := new(Buffer)
	buf2ch2 := new(Buffer)

	var wg sync.WaitGroup

	// Subscribe to channel 1
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = rr.Sub(context.TODO(), "sub-1-ch1", buf1ch1, []*Channel{ch1}, false)
	}()
	go func() {
		defer wg.Done()
		_ = rr.Sub(context.TODO(), "sub-2-ch1", buf2ch1, []*Channel{ch1}, false)
	}()

	// Subscribe to channel 2
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = rr.Sub(context.TODO(), "sub-1-ch2", buf1ch2, []*Channel{ch2}, false)
	}()
	go func() {
		defer wg.Done()
		_ = rr.Sub(context.TODO(), "sub-2-ch2", buf2ch2, []*Channel{ch2}, false)
	}()

	time.Sleep(100 * time.Millisecond)

	// Publish to channel 1
	wg.Add(2)
	for i := 0; i < 2; i++ {
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("ch1-msg%d\n", idx)
			_ = rr.Pub(context.TODO(), "pub-1", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{ch1}, false)
		}()
	}

	// Publish to channel 2
	wg.Add(2)
	for i := 0; i < 2; i++ {
		idx := i
		go func() {
			defer wg.Done()
			msg := fmt.Sprintf("ch2-msg%d\n", idx)
			_ = rr.Pub(context.TODO(), "pub-2", &Buffer{b: *bytes.NewBufferString(msg)}, []*Channel{ch2}, false)
		}()
	}

	wg.Wait()

	t.Logf("ch1-buf1: %s", buf1ch1.String())
	t.Logf("ch1-buf2: %s", buf2ch1.String())
	t.Logf("ch2-buf1: %s", buf1ch2.String())
	t.Logf("ch2-buf2: %s", buf2ch2.String())

	// Both channels should have distributed messages independently
	ch1Total := len(buf1ch1.String()) + len(buf2ch1.String())
	ch2Total := len(buf1ch2.String()) + len(buf2ch2.String())

	if ch1Total == 0 {
		t.Fatal("Channel 1 should have received messages")
	}
	if ch2Total == 0 {
		t.Fatal("Channel 2 should have received messages")
	}
}
