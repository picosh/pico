package feeds

import (
	"fmt"

	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("FEEDS_DEBUG", "0")
	domain := shared.GetEnv("FEEDS_DOMAIN", "feeds.sh")
	email := shared.GetEnv("FEEDS_EMAIL", "hello@feeds.sh")
	subdomains := shared.GetEnv("FEEDS_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("FEEDS_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := shared.GetEnv("FEEDS_PROTOCOL", "https")
	allowRegister := shared.GetEnv("FEEDS_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	sendgridKey := shared.GetEnv("SENDGRID_API_KEY", "")

	intro := "To get started, enter a username and email.\n"
	intro += "Then upload a file containing a list of rss feeds (e.g. ~/feeds.txt)\n"
	intro += "Finally, send your file to us:\n\n"
	intro += fmt.Sprintf("scp ~/feeds.txt %s:/", domain)

	return &shared.ConfigSite{
		Debug:                debug == "1",
		SubdomainsEnabled:    subdomains == "1",
		CustomdomainsEnabled: customdomains == "1",
		SendgridKey:          sendgridKey,
		ConfigCms: config.ConfigCms{
			Domain:        domain,
			Email:         email,
			Port:          port,
			Protocol:      protocol,
			DbURL:         dbURL,
			StorageDir:    storageDir,
			MinioURL:      minioURL,
			MinioUser:     minioUser,
			MinioPass:     minioPass,
			Description:   "an rss-to-email digest service for hackers",
			IntroText:     intro,
			Space:         "feeds",
			AllowedExt:    []string{".txt"},
			HiddenPosts:   []string{"_header.txt", "_readme.txt"},
			Logger:        shared.CreateLogger(),
			AllowRegister: allowRegister == "1",
		},
	}
}
