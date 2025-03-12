package feeds

import (
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/utils"
)

func NewConfigSite(service string) *shared.ConfigSite {
	debug := utils.GetEnv("FEEDS_DEBUG", "0")
	domain := utils.GetEnv("FEEDS_DOMAIN", "feeds.pico.sh")
	port := utils.GetEnv("FEEDS_WEB_PORT", "3000")
	protocol := utils.GetEnv("FEEDS_PROTOCOL", "https")
	storageDir := utils.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := utils.GetEnv("MINIO_URL", "")
	minioUser := utils.GetEnv("MINIO_ROOT_USER", "")
	minioPass := utils.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := utils.GetEnv("DATABASE_URL", "")
	sendgridKey := utils.GetEnv("SENDGRID_API_KEY", "")

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
		Logger:      shared.CreateLogger(service),
	}
}
