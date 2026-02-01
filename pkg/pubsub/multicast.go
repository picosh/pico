package pubsub

import (
	"context"
	"errors"
	"io"
	"iter"
	"log/slog"

	"github.com/antoniomika/syncmap"
)

/*
Multicast is a flexible, bidirectional broker.

It provides the most pure version of our PubSub interface which lets
end-developers build one-to-many connections between publishers and
subscribers and vice versa.

It doesn't provide any topic filtering capabilities and is only
concerned with sending data to and from an `io.ReadWriter` via our
channels.
*/
type Multicast struct {
	Broker
	Logger *slog.Logger
}

func NewMulticast(logger *slog.Logger) *Multicast {
	return &Multicast{
		Logger: logger,
		Broker: &BaseBroker{
			Channels: syncmap.New[string, *Channel](),
			Logger:   logger.With(slog.Bool("broker", true)),
		},
	}
}

func (p *Multicast) getClients(direction ChannelDirection) iter.Seq2[string, *Client] {
	return func(yield func(string, *Client) bool) {
		for clientID, client := range p.GetClients() {
			if client.Direction == direction {
				yield(clientID, client)
			}
		}
	}
}

func (p *Multicast) GetPipes() iter.Seq2[string, *Client] {
	return p.getClients(ChannelDirectionInputOutput)
}

func (p *Multicast) GetPubs() iter.Seq2[string, *Client] {
	return p.getClients(ChannelDirectionInput)
}

func (p *Multicast) GetSubs() iter.Seq2[string, *Client] {
	return p.getClients(ChannelDirectionOutput)
}

func (p *Multicast) connect(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, direction ChannelDirection, blockWrite bool, replay, keepAlive bool, dispatcher MessageDispatcher) (error, error) {
	client := NewClient(ID, rw, direction, blockWrite, replay, keepAlive)

	// Set dispatcher on all channels (only if not already set)
	for _, ch := range channels {
		ch.SetDispatcher(dispatcher)
	}

	go func() {
		<-ctx.Done()
		client.Cleanup()
	}()

	return p.Connect(client, channels)
}

func (p *Multicast) Pipe(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, replay bool) (error, error) {
	return p.connect(ctx, ID, rw, channels, ChannelDirectionInputOutput, false, replay, false, &MulticastDispatcher{})
}

func (p *Multicast) Pub(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, blockWrite bool) error {
	return errors.Join(p.connect(ctx, ID, rw, channels, ChannelDirectionInput, blockWrite, false, false, &MulticastDispatcher{}))
}

func (p *Multicast) Sub(ctx context.Context, ID string, rw io.ReadWriter, channels []*Channel, keepAlive bool) error {
	return errors.Join(p.connect(ctx, ID, rw, channels, ChannelDirectionOutput, false, false, keepAlive, &MulticastDispatcher{}))
}

var _ PubSub = (*Multicast)(nil)
