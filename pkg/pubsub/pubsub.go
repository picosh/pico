package pubsub

import (
	"context"
	"io"
	"iter"
)

/*
PubSub is our take on a basic publisher and subscriber interface.

It has a few notable requirements:
- Each operation must accept an array of channels
- A way to send, receive, and stream data between clients

PubSub also inherits the properties of a Broker.
*/
type PubSub interface {
	Broker
	GetPubs() iter.Seq2[string, *Client]
	GetSubs() iter.Seq2[string, *Client]
	GetPipes() iter.Seq2[string, *Client]
	Pipe(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, replay bool) (error, error)
	Sub(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, keepAlive bool) error
	Pub(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, blockWrite bool) error
}
