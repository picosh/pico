package pubsub

import (
	"io"
	"iter"
	"sync"

	"github.com/antoniomika/syncmap"
)

func NewClient(ID string, rw io.ReadWriter, direction ChannelDirection, blockWrite, replay, keepAlive bool) *Client {
	return &Client{
		ID:         ID,
		ReadWriter: rw,
		Direction:  direction,
		Channels:   syncmap.New[string, *Channel](),
		Done:       make(chan struct{}),
		Data:       make(chan ChannelMessage),
		Replay:     replay,
		BlockWrite: blockWrite,
		KeepAlive:  keepAlive,
	}
}

/*
Client is the container for holding state between multiple devices.  A
client has a direction (input, output, inputout) as well as a way to
send data to all the associated channels.
*/
type Client struct {
	ID         string
	ReadWriter io.ReadWriter
	Channels   *syncmap.Map[string, *Channel]
	Direction  ChannelDirection
	Done       chan struct{}
	Data       chan ChannelMessage
	Replay     bool
	BlockWrite bool
	KeepAlive  bool
	once       sync.Once
	onceData   sync.Once
}

func (c *Client) GetChannels() iter.Seq2[string, *Channel] {
	return c.Channels.Range
}

func (c *Client) Cleanup() {
	c.once.Do(func() {
		close(c.Done)
	})
}
