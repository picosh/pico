package pgs

import (
	"fmt"
	"net/http"
	"time"

	"github.com/picosh/pico/shared"
)

func getSurrogateKey(userName, projectName string) string {
	return fmt.Sprintf("%s-%s", userName, projectName)
}

func getCacheApiUrl(cfg *shared.ConfigSite) string {
	return fmt.Sprintf("%s://%s/souin-api/souin/", cfg.Protocol, cfg.Domain)
}

// purgeCache send an HTTP request to the pgs Caddy instance which purges
// cached entries for a given subdomain (like "fakeuser-www-proj"). We set a
// "surrogate-key: <subdomain>" header on every pgs response which ensures all
// cached assets for a given subdomain are grouped under a single key (which is
// separate from the "GET-https-example.com-/path" key used for serving files
// from the cache).
func purgeCache(cfg *shared.ConfigSite, surrogate string) error {
	cacheApiUrl := getCacheApiUrl(cfg)
	cfg.Logger.Info("purging cache", "url", cacheApiUrl, "surrogate", surrogate)
	client := &http.Client{
		Timeout: time.Second * 5,
	}
	req, err := http.NewRequest("PURGE", cacheApiUrl, nil)
	if err != nil {
		return err
	}
	if surrogate != "" {
		req.Header.Add("Surrogate-Key", surrogate)
	}
	req.SetBasicAuth(cfg.CacheUser, cfg.CachePassword)
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode != 204 {
		return fmt.Errorf("received unexpected response code %d", resp.StatusCode)
	}
	return nil
}

func purgeAllCache(cfg *shared.ConfigSite) error {
	return purgeCache(cfg, "")
}
