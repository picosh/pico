package links

import (
	"log/slog"

	"github.com/picosh/utils"
)

type LinksConfig struct {
	SshHost  string
	SshPort  string
	WebPort  string
	PromPort string

	// Database layer; it's just an interface that could be implemented
	// with anything.
	DB     DB
	Logger *slog.Logger
}

func NewLinksConfig(logger *slog.Logger, dbpool DB) *LinksConfig {
	port := utils.GetEnv("LINKS_WEB_PORT", "3000")
	sshHost := utils.GetEnv("LINKS_SSH_HOST", "0.0.0.0")
	sshPort := utils.GetEnv("LINKS_SSH_PORT", "2222")
	promPort := utils.GetEnv("LINKS_PROM_PORT", "9222")

	cfg := LinksConfig{
		SshHost:  sshHost,
		SshPort:  sshPort,
		WebPort:  port,
		PromPort: promPort,

		DB:     dbpool,
		Logger: logger,
	}

	return &cfg
}
