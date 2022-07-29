package internal

import (
	"fmt"
	"html/template"
	"log"
	"net/url"

	"git.sr.ht/~erock/wish/cms/config"
	"go.uber.org/zap"
)

type SitePageData struct {
	Domain  template.URL
	HomeURL template.URL
	Email   string
}

type ConfigSite struct {
	config.ConfigCms
	config.ConfigURL
	SubdomainsEnabled bool
}

func NewConfigSite() *ConfigSite {
	domain := GetEnv("LISTS_DOMAIN", "lists.sh")
	email := GetEnv("LISTS_EMAIL", "support@lists.sh")
	subdomains := GetEnv("LISTS_SUBDOMAINS", "0")
	port := GetEnv("LISTS_WEB_PORT", "3000")
	protocol := GetEnv("LISTS_PROTOCOL", "https")
	dbURL := GetEnv("DATABASE_URL", "")
	subdomainsEnabled := false
	if subdomains == "1" {
		subdomainsEnabled = true
	}

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/blog).\n"
	intro += "Then write your lists in plain text files (e.g. hello-world.txt).\n"
	intro += "Finally, send your list files to us:\n\n"
	intro += fmt.Sprintf("scp ~/blog/*.txt %s:/\n\n", domain)

	return &ConfigSite{
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
			Logger:      CreateLogger(),
		},
	}
}

func (c *ConfigSite) GetSiteData() *SitePageData {
	return &SitePageData{
		Domain:  template.URL(c.Domain),
		HomeURL: template.URL(c.HomeURL()),
		Email:   c.Email,
	}
}

func (c *ConfigSite) BlogURL(username string) string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
	}

	return fmt.Sprintf("/%s", username)
}

func (c *ConfigSite) PostURL(username, filename string) string {
	fname := url.PathEscape(filename)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/%s/%s", username, fname)
}

func (c *ConfigSite) IsSubdomains() bool {
	return c.SubdomainsEnabled
}

func (c *ConfigSite) RssBlogURL(username string) string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/rss", c.Protocol, username, c.Domain)
	}

	return fmt.Sprintf("/%s/rss", username)
}

func (c *ConfigSite) HomeURL() string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s", c.Protocol, c.Domain)
	}

	return "/"
}

func (c *ConfigSite) ReadURL() string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s/read", c.Protocol, c.Domain)
	}

	return "/read"
}

func CreateLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}
