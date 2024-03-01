package prose

import (
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PROSE_DEBUG", "0")
	domain := shared.GetEnv("PROSE_DOMAIN", "prose.sh")
	email := shared.GetEnv("PROSE_EMAIL", "hello@prose.sh")
	subdomains := shared.GetEnv("PROSE_SUBDOMAINS", "0")
	customdomains := shared.GetEnv("PROSE_CUSTOMDOMAINS", "0")
	port := shared.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := shared.GetEnv("PROSE_PROTOCOL", "https")
	allowRegister := shared.GetEnv("PROSE_ALLOW_REGISTER", "1")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	useImgProxy := shared.GetEnv("USE_IMGPROXY", "1")
	maxSize := uint64(500 * shared.MB)
	maxImgSize := int64(10 * shared.MB)

	intro := "To get started, enter a username.\n"
	intro += "To learn next steps go to our docs at https://pico.sh/prose\n"

	return &shared.ConfigSite{
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
			Description: "A blog platform for hackers.",
			IntroText:   intro,
			Space:       "prose",
			AllowedExt: []string{
				".md",
				".jpg",
				".jpeg",
				".png",
				".gif",
				".webp",
				".svg",
			},
			HiddenPosts:   []string{"_readme.md", "_styles.css", "_footer.md", "_404.md"},
			Logger:        shared.CreateLogger(debug == "1"),
			AllowRegister: allowRegister == "1",
			MaxSize:       maxSize,
			MaxAssetSize:  maxImgSize,
		},
	}
}
