package pgs

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/shared/storage"
	"github.com/picosh/utils"
)

type PgsConfig struct {
	CacheControl       string
	CacheTTL           time.Duration
	Domain             string
	MaxAssetSize       int64
	MaxSize            uint64
	MaxSpecialFileSize int64
	SshHost            string
	SshPort            string
	TxtPrefix          string
	WebPort            string
	WebProtocol        string

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

func (c *PgsConfig) AssetURL(username, projectName, fpath string) string {
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

func (c *PgsConfig) StaticPath(fname string) string {
	return filepath.Join("pkg", "apps", "pgs", fname)
}

var maxSize = uint64(25 * utils.MB)
var maxAssetSize = int64(10 * utils.MB)

// Needs to be small for caching files like _headers and _redirects.
var maxSpecialFileSize = int64(5 * utils.KB)

func NewPgsConfig(logger *slog.Logger, dbpool pgsdb.PgsDB, st storage.StorageServe, pubsub PicoPubsub) *PgsConfig {
	domain := utils.GetEnv("PGS_DOMAIN", "pgs.sh")
	port := utils.GetEnv("PGS_WEB_PORT", "3000")
	protocol := utils.GetEnv("PGS_PROTOCOL", "https")
	cacheTTL, err := time.ParseDuration(utils.GetEnv("PGS_CACHE_TTL", ""))
	if err != nil {
		cacheTTL = 600 * time.Second
	}
	cacheControl := utils.GetEnv(
		"PGS_CACHE_CONTROL",
		fmt.Sprintf("max-age=%d", int(cacheTTL.Seconds())))

	sshHost := utils.GetEnv("PGS_SSH_HOST", "0.0.0.0")
	sshPort := utils.GetEnv("PGS_SSH_PORT", "2222")

	cfg := PgsConfig{
		CacheControl:       cacheControl,
		CacheTTL:           cacheTTL,
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
