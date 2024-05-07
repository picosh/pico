package prose

import (
	"github.com/picosh/pico/shared"
)

func NewConfigSite() *shared.ConfigSite {
	debug := shared.GetEnv("PROSE_DEBUG", "0")
	domain := shared.GetEnv("PROSE_DOMAIN", "prose.sh")
	port := shared.GetEnv("PROSE_WEB_PORT", "3000")
	protocol := shared.GetEnv("PROSE_PROTOCOL", "https")
	storageDir := shared.GetEnv("IMGS_STORAGE_DIR", ".storage")
	minioURL := shared.GetEnv("MINIO_URL", "")
	minioUser := shared.GetEnv("MINIO_ROOT_USER", "")
	minioPass := shared.GetEnv("MINIO_ROOT_PASSWORD", "")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	maxSize := uint64(500 * shared.MB)
	maxImgSize := int64(10 * shared.MB)
	secret := shared.GetEnv("PICO_SECRET", "")
	if secret == "" {
		panic("must provide PICO_SECRET environment variable")
	}

	return &shared.ConfigSite{
		Debug:      debug == "1",
		Secret:     secret,
		Domain:     domain,
		Port:       port,
		Protocol:   protocol,
		DbURL:      dbURL,
		StorageDir: storageDir,
		MinioURL:   minioURL,
		MinioUser:  minioUser,
		MinioPass:  minioPass,
		Space:      "prose",
		AllowedExt: []string{
			".md",
			".jpg",
			".jpeg",
			".png",
			".gif",
			".webp",
			".svg",
			".ico",
		},
		HiddenPosts:  []string{"_readme.md", "_styles.css", "_footer.md", "_404.md"},
		Logger:       shared.CreateLogger(debug == "1"),
		MaxSize:      maxSize,
		MaxAssetSize: maxImgSize,
	}
}
