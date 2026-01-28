package pubsub

import (
	"iter"
	"sync"

	"github.com/antoniomika/syncmap"
)

type ChannelDirection int

func (d ChannelDirection) String() string {
	return [...]string{"input", "output", "inputoutput"}[d]
}

const (
	ChannelDirectionInput ChannelDirection = iota
	ChannelDirectionOutput
	ChannelDirectionInputOutput
)

type ChannelAction int

func (d ChannelAction) String() string {
	return [...]string{"data", "close"}[d]
}

const (
	ChannelActionData = iota
	ChannelActionClose
)

type ChannelMessage struct {
	Data      []byte
	ClientID  string
	Direction ChannelDirection
	Action    ChannelAction
}

func NewChannel(topic string) *Channel {
	return &Channel{
		Topic:   topic,
		Done:    make(chan struct{}),
		Data:    make(chan ChannelMessage),
		Clients: syncmap.New[string, *Client](),
	}
}

/*
Channel is a container for a topic.  It holds the list of clients and
a data channel to receive a message.
*/
type Channel struct {
	Topic       string
	Done        chan struct{}
	Data        chan ChannelMessage
	Clients     *syncmap.Map[string, *Client]
	handleOnce  sync.Once
	cleanupOnce sync.Once
	Dispatcher  MessageDispatcher
}

func (c *Channel) GetClients() iter.Seq2[string, *Client] {
	return c.Clients.Range
}

func (c *Channel) Cleanup() {
	c.cleanupOnce.Do(func() {
		close(c.Done)
	})
}

func (c *Channel) Handle() {
	// If no dispatcher is set, use multicast as default
	if c.Dispatcher == nil {
		c.Dispatcher = &MulticastDispatcher{}
	}

	c.handleOnce.Do(func() {
		go func() {
			defer func() {
				for _, client := range c.GetClients() {
					client.Cleanup()
				}
			}()

			for {
				select {
				case <-c.Done:
					return
				case data, ok := <-c.Data:
					if !ok {
						// Channel is closing, close all client data channels
						for _, client := range c.GetClients() {
							client.onceData.Do(func() {
								close(client.Data)
							})
						}
						return
					}

					// Collect eligible subscribers
					subscribers := dispatcherForGetClients(c.GetClients(), data)

					// Dispatch message using the configured dispatcher
					_ = c.Dispatcher.Dispatch(data, subscribers, c.Done)
				}
			}
		}()
	})
}
