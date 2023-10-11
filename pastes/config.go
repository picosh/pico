package pastes

import (
	"fmt"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PASTES_DEBUG", "0")
	domain := shared.GetEnv("PASTES_DOMAIN", "pastes.sh")
	email := shared.GetEnv("PASTES_EMAIL", "hello@pastes.sh")
	subdomains := shared.GetEnv("PASTES_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("PASTES_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	protocol := shared.GetEnv("PASTES_PROTOCOL", "https")
	allowRegister := shared.GetEnv("PASTES_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")

	intro := "To get started, enter a username.\n"
	intro += "Then all you need to do is send your pastes to us:\n\n"
	intro += fmt.Sprintf("scp my.patch %s:/", domain)

	return &shared.ConfigSite{
		Debug:                debug == "1",
		SubdomainsEnabled:    subdomains == "1",
		CustomdomainsEnabled: customdomains == "1",
		ConfigCms: config.ConfigCms{
			Domain:        domain,
			Port:          port,
			Protocol:      protocol,
			Email:         email,
			DbURL:         dbURL,
			StorageDir:    storageDir,
			MinioURL:      minioURL,
			MinioUser:     minioUser,
			MinioPass:     minioPass,
			Description:   "a pastebin for hackers.",
			IntroText:     intro,
			Space:         "pastes",
			Logger:        shared.CreateLogger(),
			AllowRegister: allowRegister == "1",
		},
	}
}
