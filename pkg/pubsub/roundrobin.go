package pubsub

import (
	"slices"
	"strings"
	"sync"
)

/*
RoundRobin is a load-balancing broker that distributes published messages
to subscribers using a round-robin algorithm.

Unlike Multicast which sends each message to all subscribers, RoundRobin
sends each message to exactly one subscriber, rotating through the available
subscribers for each published message. This provides load balancing for
message processing.

It maintains independent round-robin state per channel/topic.
*/
type RoundRobinDispatcher struct {
	index uint32
	mu    sync.Mutex
}

func (d *RoundRobinDispatcher) Dispatch(msg ChannelMessage, subscribers []*Client, channelDone chan struct{}) error {
	// If no subscribers, nothing to dispatch
	// BlockWrite behavior at publish time ensures subscribers are present when needed
	if len(subscribers) == 0 {
		return nil
	}

	slices.SortFunc(subscribers, func(a, b *Client) int {
		return strings.Compare(a.ID, b.ID)
	})

	// Select the next subscriber in round-robin order
	d.mu.Lock()
	selectedIdx := int(d.index % uint32(len(subscribers)))
	d.index++
	d.mu.Unlock()

	selectedClient := subscribers[selectedIdx]

	select {
	case selectedClient.Data <- msg:
	case <-selectedClient.Done:
	case <-channelDone:
	}

	return nil
}
