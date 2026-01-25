package pubsub

import (
	"iter"
)

// MessageDispatcher defines how messages are dispatched to subscribers.
type MessageDispatcher interface {
	// Dispatch sends a message to the appropriate subscriber(s).
	// It receives the message, all subscribers, and the channel's sync primitives.
	Dispatch(msg ChannelMessage, subscribers []*Client, channelDone chan struct{}) error
}

// dispatcherForGetClients collects eligible clients for dispatching.
// Returns clients that should receive messages (output direction, not the sender unless replay).
func dispatcherForGetClients(getClients iter.Seq2[string, *Client], msg ChannelMessage) []*Client {
	subscribers := make([]*Client, 0)
	for _, client := range getClients {
		// Skip input-only clients and senders (unless replay is enabled)
		if client.Direction == ChannelDirectionInput || (client.ID == msg.ClientID && !client.Replay) {
			continue
		}
		subscribers = append(subscribers, client)
	}
	return subscribers
}
