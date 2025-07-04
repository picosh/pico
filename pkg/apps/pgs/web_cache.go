package pgs

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils/pipe"
)

type PicoPubsub interface {
	io.Reader
	io.Writer
	io.Closer
}

type PubsubPipe struct {
	Pipe *pipe.ReconnectReadWriteCloser
}

func (p *PubsubPipe) Read(b []byte) (int, error) {
	return p.Pipe.Read(b)
}

func (p *PubsubPipe) Write(b []byte) (int, error) {
	return p.Pipe.Write(b)
}

func (p *PubsubPipe) Close() error {
	return p.Pipe.Close()
}

func NewPubsubPipe(pipe *pipe.ReconnectReadWriteCloser) *PubsubPipe {
	return &PubsubPipe{
		Pipe: pipe,
	}
}

type PubsubChan struct {
	Chan chan []byte
}

func (p *PubsubChan) Read(b []byte) (int, error) {
	n := copy(b, <-p.Chan)
	return n, nil
}

func (p *PubsubChan) Write(b []byte) (int, error) {
	p.Chan <- b
	return len(b), nil
}

func (p *PubsubChan) Close() error {
	close(p.Chan)
	return nil
}

func NewPubsubChan() *PubsubChan {
	return &PubsubChan{
		Chan: make(chan []byte),
	}
}

func getSurrogateKey(userName, projectName string) string {
	return fmt.Sprintf("%s-%s", userName, projectName)
}

func CreatePubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"pub to cache-drain",
		"pub cache-drain -b=false",
		100,
		-1,
	)
	return send
}

func CreateSubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
	info := shared.NewPicoPipeClient()
	send := pipe.NewReconnectReadWriteCloser(
		ctx,
		logger,
		info,
		"sub to cache-drain",
		"sub cache-drain -k",
		100,
		-1,
	)
	return send
}

// purgeCache send a pipe pub to the pgs web instance which purges
// cached entries for a given subdomain (like "fakeuser-www-proj"). We set a
// "surrogate-key: <subdomain>" header on every pgs response which ensures all
// cached assets for a given subdomain are grouped under a single key (which is
// separate from the "GET-https-example.com-/path" key used for serving files
// from the cache).
func purgeCache(cfg *PgsConfig, writer io.Writer, surrogate string) error {
	cfg.Logger.Info("purging cache", "surrogate", surrogate)
	time.Sleep(1 * time.Second)
	_, err := writer.Write([]byte(surrogate + "\n"))
	return err
}

func purgeAllCache(cfg *PgsConfig, writer io.Writer) error {
	return purgeCache(cfg, writer, "*")
}
