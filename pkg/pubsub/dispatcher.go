package pubsub

// MessageDispatcher defines how messages are dispatched to subscribers.
type MessageDispatcher interface {
	// Dispatch sends a message to the appropriate subscriber(s).
	// It receives the message, all subscribers, and the channel's sync primitives.
	Dispatch(msg ChannelMessage, subscribers []*Client, channelDone chan struct{}) error
}
