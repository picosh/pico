package pgs

import (
	"fmt"
	"html/template"
	"log/slog"
	"path/filepath"
	"strconv"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/pico/pkg/storage"
)

type Config struct {
	CacheTTL           time.Duration
	CacheMaxItems      int
	CacheMaxBodySize   int64
	Domain             string
	MaxAssetSize       int64
	MaxSize            uint64
	MaxSpecialFileSize int64
	SshHost            string
	SshPort            string
	WebPort            string
	WebProtocol        string
	TxtPrefix          string

	// This channel will receive the surrogate key for a project (e.g. static site)
	// which will inform the caching layer to clear the cache for that site.
	CacheClearingQueue chan string
	// Database layer; it's just an interface that could be implemented
	// with anything.
	DB     pgsdb.PgsDB
	Logger *slog.Logger
	// Where we store the static assets uploaded to our service.
	Storage storage.StorageServe
	Pubsub  PicoPubsub
}

func (c *Config) AssetURL(username, projectName, fpath string) string {
	if username == projectName {
		return fmt.Sprintf(
			"%s://%s.%s/%s",
			c.WebProtocol,
			username,
			c.Domain,
			fpath,
		)
	}

	return fmt.Sprintf(
		"%s://%s-%s.%s/%s",
		c.WebProtocol,
		username,
		projectName,
		c.Domain,
		fpath,
	)
}

// staticDir holds pgs's templates and public files.
const staticDir = "pkg/apps/pgs"

func (c *Config) StaticPath(fname string) string {
	return filepath.Join(staticDir, fname)
}

func (c *Config) Static() router.Static {
	return router.Static{
		Dir:    staticDir,
		Logger: c.Logger,
		Site:   shared.SitePageData{Domain: template.URL(c.Domain), HomeURL: "/"},
	}
}

var maxSize = uint64(50 * shared.MB)
var maxAssetSize = int64(10 * shared.MB)

// Larger responses stream from storage instead of being held in the cache.
// Zero means no limit.
var cacheMaxBodySize = int64(0)

// Needs to be small for caching files like _headers and _redirects.
var maxSpecialFileSize = int64(5 * shared.KB)

func NewConfig(logger *slog.Logger, dbpool pgsdb.PgsDB, st storage.StorageServe, pubsub PicoPubsub) *Config {
	domain := shared.GetEnv("PGS_DOMAIN", "pgs.sh")
	port := shared.GetEnv("PGS_WEB_PORT", "3000")
	protocol := shared.GetEnv("PGS_PROTOCOL", "https")
	cacheTTL, err := time.ParseDuration(shared.GetEnv("PGS_CACHE_TTL", ""))
	if err != nil {
		cacheTTL = 600 * time.Second
	}
	cacheMaxItemsStr := shared.GetEnv("PGS_CACHE_MAX_ITEMS", "")
	cacheMaxItems := 0
	if cacheMaxItemsStr != "" {
		cacheMaxItems, _ = strconv.Atoi(cacheMaxItemsStr)
	}
	cacheMaxBody, err := strconv.ParseInt(shared.GetEnv("PGS_CACHE_MAX_BODY_SIZE", ""), 10, 64)
	if err != nil {
		cacheMaxBody = cacheMaxBodySize
	}

	sshHost := shared.GetEnv("PGS_SSH_HOST", "0.0.0.0")
	sshPort := shared.GetEnv("PGS_SSH_PORT", "2222")

	cfg := Config{
		CacheTTL:           cacheTTL,
		CacheMaxItems:      cacheMaxItems,
		CacheMaxBodySize:   cacheMaxBody,
		Domain:             domain,
		MaxAssetSize:       maxAssetSize,
		MaxSize:            maxSize,
		MaxSpecialFileSize: maxSpecialFileSize,
		SshHost:            sshHost,
		SshPort:            sshPort,
		TxtPrefix:          "pgs",
		WebPort:            port,
		WebProtocol:        protocol,

		CacheClearingQueue: make(chan string, 100),
		DB:                 dbpool,
		Logger:             logger,
		Storage:            st,
		Pubsub:             pubsub,
	}

	return &cfg
}
