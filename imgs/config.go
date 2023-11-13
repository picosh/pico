package imgs

import (
	"fmt"

	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

type ImgsLinkify struct {
	Cfg          *shared.ConfigSite
	Username     string
	OnSubdomain  bool
	WithUsername bool
}

func NewImgsLinkify(username string) *ImgsLinkify {
	cfg := NewConfigSite()
	return &ImgsLinkify{
		Cfg:      cfg,
		Username: username,
	}
}

func (i *ImgsLinkify) Create(fname string) string {
	return i.Cfg.ImgFullURL(i.Username, fname)
}

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
	intro += "Then create a folder locally (e.g. ~/imgs).\n"
	intro += "Finally, send your images to us:\n\n"
	intro += fmt.Sprintf("scp ~/imgs/*.jpg %s:/", domain)

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
