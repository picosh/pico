package feeds

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("FEEDS_DEBUG", "0")
	domain := shared.GetEnv("FEEDS_DOMAIN", "feeds.sh")
	port := shared.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := shared.GetEnv("FEEDS_PROTOCOL", "https")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	sendgridKey := shared.GetEnv("SENDGRID_API_KEY", "")

	return &shared.ConfigSite{
		Debug:       debug == "1",
		SendgridKey: sendgridKey,
		Domain:      domain,
		Port:        port,
		Protocol:    protocol,
		DbURL:       dbURL,
		StorageDir:  storageDir,
		MinioURL:    minioURL,
		MinioUser:   minioUser,
		MinioPass:   minioPass,
		Space:       "feeds",
		AllowedExt:  []string{".txt"},
		HiddenPosts: []string{"_header.txt", "_readme.txt"},
		Logger:      shared.CreateLogger("feeds"),
	}
}
