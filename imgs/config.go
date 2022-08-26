package imgs

import (
	"fmt"

	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/config"
)

func ImgBaseURL(username string) string {
	cfg := NewConfigSite()
	if cfg.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s", cfg.Protocol, username, cfg.Domain)
	}

	return "/"
}

func NewConfigSite() *shared.ConfigSite {
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
	subdomainsEnabled := false
	if subdomains == "1" {
		subdomainsEnabled = true
	}

	customdomainsEnabled := false
	if customdomains == "1" {
		customdomainsEnabled = true
	}

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/imgs).\n"
	intro += "Finally, send your images to us:\n\n"
	intro += fmt.Sprintf("scp ~/imgs/*.jpg %s:/", domain)

	cfg := shared.ConfigSite{
		SubdomainsEnabled:    subdomainsEnabled,
		CustomdomainsEnabled: customdomainsEnabled,
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
			Description:   "a premium image hosting service for hackers.",
			IntroText:     intro,
			Space:         "imgs",
			AllowedExt:    []string{".jpg", ".jpeg", ".png", ".gif", ".webp", ".svg"},
			Logger:        shared.CreateLogger(),
			AllowRegister: allowRegister == "1",
		},
	}

	return &cfg
}
