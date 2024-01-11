package lists

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("LISTS_DEBUG", "0")
	domain := shared.GetEnv("LISTS_DOMAIN", "lists.sh")
	email := shared.GetEnv("LISTS_EMAIL", "support@lists.sh")
	subdomains := shared.GetEnv("LISTS_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("LISTS_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("LISTS_WEB_PORT", "3000")
	protocol := shared.GetEnv("LISTS_PROTOCOL", "https")
	allowRegister := shared.GetEnv("LISTS_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	useImgProxy := shared.GetEnv("USE_IMGPROXY", "1")

	intro := "To get started, enter a username.\n"
	intro += "To learn next steps go to our docs at https://pico.sh/lists\n"

	return &shared.ConfigSite{
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
			Description:   "A microblog for your lists",
			IntroText:     intro,
			Space:         "lists",
			AllowedExt:    []string{".txt"},
			HiddenPosts:   []string{"_header.txt", "_readme.txt"},
			Logger:        shared.CreateLogger(debug == "1"),
			AllowRegister: allowRegister == "1",
		},
	}
}
