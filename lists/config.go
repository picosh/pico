package lists

import (
	"fmt"

	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms/config"
)

func NewConfigSite() *shared.ConfigSite {
	domain := shared.GetEnv("LISTS_DOMAIN", "lists.sh")
	email := shared.GetEnv("LISTS_EMAIL", "support@lists.sh")
	subdomains := shared.GetEnv("LISTS_SUBDOMAINS", "0")
	port := shared.GetEnv("LISTS_WEB_PORT", "3000")
	protocol := shared.GetEnv("LISTS_PROTOCOL", "https")
	dbURL := shared.GetEnv("DATABASE_URL", "")
	subdomainsEnabled := false
	if subdomains == "1" {
		subdomainsEnabled = true
	}

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/blog).\n"
	intro += "Then write your lists in plain text files (e.g. hello-world.txt).\n"
	intro += "Finally, send your list files to us:\n\n"
	intro += fmt.Sprintf("scp ~/blog/*.txt %s:/\n\n", domain)

	return &shared.ConfigSite{
		SubdomainsEnabled: subdomainsEnabled,
		ConfigCms: config.ConfigCms{
			Domain:      domain,
			Email:       email,
			Port:        port,
			Protocol:    protocol,
			DbURL:       dbURL,
			Description: "A microblog for your lists.",
			IntroText:   intro,
			Space:       "lists",
			AllowedExt:  []string{".txt"},
			HiddenPosts: []string{"_header.txt", "_readme.txt"},
			Logger:      shared.CreateLogger(),
		},
	}
}
