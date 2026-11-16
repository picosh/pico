package auth

import (
	"log/slog"
	"strings"

	"github.com/picosh/pico/pkg/shared"
)

type Config struct {
	Debug         bool
	DbURL         string
	Issuer        string
	Domain        string
	Port          string
	Secret        string
	SecretWebhook string
	// RedirectHosts are the hosts /redirect may send an API token to.
	RedirectHosts []string
	Logger        *slog.Logger
}

func NewConfig() *Config {
	debug := shared.GetEnv("AUTH_DEBUG", "0")
	withPipe := strings.ToLower(shared.GetEnv("PICO_PIPE_ENABLED", "true")) == "true"

	return &Config{
		DbURL:         shared.GetEnv("DATABASE_URL", ""),
		Debug:         debug == "1",
		Issuer:        shared.GetEnv("AUTH_ISSUER", "pico.sh"),
		Domain:        shared.GetEnv("AUTH_DOMAIN", "http://0.0.0.0:3000"),
		Port:          shared.GetEnv("AUTH_WEB_PORT", "3000"),
		Secret:        shared.GetEnv("PICO_SECRET", ""),
		SecretWebhook: shared.GetEnv("PICO_SECRET_WEBHOOK", ""),
		RedirectHosts: parseList(shared.GetEnv("AUTH_REDIRECT_HOSTS", "chat.pico.sh")),
		Logger:        shared.CreateLogger("auth-web", withPipe),
	}
}

func parseList(s string) []string {
	list := []string{}
	for _, item := range strings.Split(s, ",") {
		if item = strings.TrimSpace(item); item != "" {
			list = append(list, item)
		}
	}
	return list
}
