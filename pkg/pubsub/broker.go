package pubsub

import (
	"errors"
	"io"
	"iter"
	"log/slog"
	"sync"
	"time"

	"github.com/antoniomika/syncmap"
)

/*
Broker receives published messages and dispatches the message to the
subscribing clients. An message contains a message topic that clients
subscribe to and brokers use these subscription lists for determining the
clients to receive the message.
*/
type Broker interface {
	GetChannels() iter.Seq2[string, *Channel]
	GetClients() iter.Seq2[string, *Client]
	Connect(*Client, []*Channel) (error, error)
}

type BaseBroker struct {
	Channels *syncmap.Map[string, *Channel]
	Logger   *slog.Logger
}

func (b *BaseBroker) Cleanup() {
	toRemove := []string{}
	for _, channel := range b.GetChannels() {
		count := 0

		for range channel.GetClients() {
			count++
		}

		if count == 0 {
			channel.Cleanup()
			toRemove = append(toRemove, channel.Topic)
		}
	}

	for _, channel := range toRemove {
		b.Channels.Delete(channel)
	}
}

func (b *BaseBroker) GetChannels() iter.Seq2[string, *Channel] {
	return b.Channels.Range
}

func (b *BaseBroker) GetClients() iter.Seq2[string, *Client] {
	return func(yield func(string, *Client) bool) {
		for _, channel := range b.GetChannels() {
			channel.Clients.Range(yield)
		}
	}
}

func (b *BaseBroker) Connect(client *Client, channels []*Channel) (error, error) {
	for _, channel := range channels {
		dataChannel := b.ensureChannel(channel)
		dataChannel.Clients.Store(client.ID, client)
		client.Channels.Store(dataChannel.Topic, dataChannel)
		defer func() {
			client.Channels.Delete(channel.Topic)
			dataChannel.Clients.Delete(client.ID)

			client.Cleanup()

			count := 0
			for _, cl := range dataChannel.GetClients() {
				if cl.Direction == ChannelDirectionInput || cl.Direction == ChannelDirectionInputOutput {
					count++
				}
			}

			if count == 0 {
				for _, cl := range dataChannel.GetClients() {
					if !cl.KeepAlive {
						cl.Cleanup()
					}
				}
			}

			b.Cleanup()
		}()
	}

	var (
		inputErr  error
		outputErr error
		wg        sync.WaitGroup
	)

	// Pub
	if client.Direction == ChannelDirectionInput || client.Direction == ChannelDirectionInputOutput {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				data := make([]byte, 32*1024)
				n, err := client.ReadWriter.Read(data)

				data = data[:n]

				channelMessage := ChannelMessage{
					Data:      data,
					ClientID:  client.ID,
					Direction: ChannelDirectionInput,
				}

				if client.BlockWrite {
				mainLoop:
					for {
						count := 0
						for _, channel := range client.GetChannels() {
							for _, chanClient := range channel.GetClients() {
								if chanClient.Direction == ChannelDirectionOutput || chanClient.Direction == ChannelDirectionInputOutput {
									count++
								}
							}
						}

						if count > 0 {
							break mainLoop
						}

						select {
						case <-client.Done:
							break mainLoop
						case <-time.After(1 * time.Millisecond):
							continue
						}
					}
				}

				var sendwg sync.WaitGroup

				for _, channel := range client.GetChannels() {
					sendwg.Add(1)
					go func() {
						defer sendwg.Done()
						select {
						case channel.Data <- channelMessage:
						case <-client.Done:
						case <-channel.Done:
						}
					}()
				}

				sendwg.Wait()

				if err != nil {
					if errors.Is(err, io.EOF) {
						return
					}
					inputErr = err
					return
				}
			}
		}()
	}

	// Sub
	if client.Direction == ChannelDirectionOutput || client.Direction == ChannelDirectionInputOutput {
		wg.Add(1)
		go func() {
			defer wg.Done()
		mainLoop:
			for {
				select {
				case data, ok := <-client.Data:
					_, err := client.ReadWriter.Write(data.Data)
					if err != nil {
						outputErr = err
						break mainLoop
					}

					if !ok {
						break mainLoop
					}
				case <-client.Done:
					break mainLoop
				}
			}
		}()
	}

	wg.Wait()

	return inputErr, outputErr
}

func (b *BaseBroker) ensureChannel(channel *Channel) *Channel {
	dataChannel, _ := b.Channels.LoadOrStore(channel.Topic, channel)
	// Allow overwriting the dispatcher
	if channel.Dispatcher != nil && dataChannel.Dispatcher == nil {
		dataChannel.Dispatcher = channel.Dispatcher
	}

	dataChannel.Handle()
	return dataChannel
}

var _ Broker = (*BaseBroker)(nil)
