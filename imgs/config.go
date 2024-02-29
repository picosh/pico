package imgs

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("IMGS_DEBUG", "0")
	domain := shared.GetEnv("IMGS_DOMAIN", "prose.sh")
	email := shared.GetEnv("IMGS_EMAIL", "hello@prose.sh")
	subdomains := shared.GetEnv("IMGS_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("IMGS_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("IMGS_WEB_PORT", "3000")
	protocol := shared.GetEnv("IMGS_PROTOCOL", "https")
	allowRegister := shared.GetEnv("IMGS_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	useImgProxy := shared.GetEnv("USE_IMGPROXY", "1")

	intro := "To get started, enter a username.\n"
	intro += "To learn next steps go to our docs at https://pico.sh/imgs\n"

	cfg := shared.ConfigSite{
		Debug:                debug == "1",
		SubdomainsEnabled:    subdomains == "1",
		CustomdomainsEnabled: customdomains == "1",
		UseImgProxy:          useImgProxy == "1",
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
			Description:   "An image hosting service for hackers.",
			IntroText:     intro,
			Space:         "imgs",
			AllowedExt:    []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg", ".ico"},
			Logger:        shared.CreateLogger(debug == "1"),
			AllowRegister: allowRegister == "1",
		},
	}

	return &cfg
}
