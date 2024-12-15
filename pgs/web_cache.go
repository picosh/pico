package pgs

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/picosh/pico/shared"
	"github.com/picosh/utils/pipe"
)

func getSurrogateKey(userName, projectName string) string {
	return fmt.Sprintf("%s-%s", userName, projectName)
}

func createPubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
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

func createSubCacheDrain(ctx context.Context, logger *slog.Logger) *pipe.ReconnectReadWriteCloser {
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

// purgeCache send an HTTP request to the pgs Caddy instance which purges
// cached entries for a given subdomain (like "fakeuser-www-proj"). We set a
// "surrogate-key: <subdomain>" header on every pgs response which ensures all
// cached assets for a given subdomain are grouped under a single key (which is
// separate from the "GET-https-example.com-/path" key used for serving files
// from the cache).
func purgeCache(cfg *shared.ConfigSite, send *pipe.ReconnectReadWriteCloser, surrogate string) error {
	cfg.Logger.Info("purging cache", "surrogate", surrogate)
	time.Sleep(1 * time.Second)
	_, err := send.Write([]byte(surrogate + "\n"))
	return err
}

func purgeAllCache(cfg *shared.ConfigSite, send *pipe.ReconnectReadWriteCloser) error {
	return purgeCache(cfg, send, "*")
}
