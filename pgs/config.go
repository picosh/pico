package pgs

import (
	"fmt"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

var maxSize = 1 * shared.GB
var maxAssetSize = 50 * shared.MB

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PGS_DEBUG", "0")
	domain := shared.GetEnv("PGS_DOMAIN", "pgs.sh")
	email := shared.GetEnv("PGS_EMAIL", "hello@prose.sh")
	subdomains := shared.GetEnv("PGS_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("PGS_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("PGS_WEB_PORT", "3000")
	protocol := shared.GetEnv("PGS_PROTOCOL", "https")
	allowRegister := shared.GetEnv("PGS_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("PGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	useImgProxy := shared.GetEnv("USE_IMGPROXY", "1")

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/sites).\n"
	intro += "Finally, send your files to us:\n\n"
	intro += fmt.Sprintf("rsync ~/sites/* %s:/<project_name>", domain)

	cfg := shared.ConfigSite{
		Debug:                debug == "1",
		SubdomainsEnabled:    subdomains == "1",
		CustomdomainsEnabled: customdomains == "1",
		UseImgProxy:          useImgProxy == "1",
		ConfigCms: config.ConfigCms{
			Domain:      domain,
			Email:       email,
			Port:        port,
			Protocol:    protocol,
			DbURL:       dbURL,
			StorageDir:  storageDir,
			MinioURL:    minioURL,
			MinioUser:   minioUser,
			MinioPass:   minioPass,
			Description: "A zero-dependency static site hosting service for hackers.",
			IntroText:   intro,
			Space:       "pgs",
			// IMPORTANT: make sure `shared.GetMimeType` has the extensions being
			// added here.
			AllowedExt: []string{
				".jpg",
				".jpeg",
				".png",
				".gif",
				".webp",
				".svg",
				".ico",
				".html",
				".htm",
				".css",
				".js",
				".pdf",
				".txt",
				".otf",
				".ttf",
				".woff",
				".woff2",
				".json",
				".md",
				".rss",
				".xml",
				".atom",
				".map",
				".webmanifest",
			},
			MaxSize:       maxSize,
			MaxAssetSize:  maxAssetSize,
			Logger:        shared.CreateLogger(debug == "1"),
			AllowRegister: allowRegister == "1",
		},
	}

	return &cfg
}
