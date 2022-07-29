package pastes

import (
	"fmt"
	"html/template"
	"log"
	"net/url"
	"path"

	"git.sr.ht/~erock/pico/wish/cms/config"
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
	domain := GetEnv("PASTES_DOMAIN", "pastes.sh")
	email := GetEnv("PASTES_EMAIL", "hello@pastes.sh")
	subdomains := GetEnv("PASTES_SUBDOMAINS", "0")
	port := GetEnv("PASTES_WEB_PORT", "3000")
	dbURL := GetEnv("DATABASE_URL", "")
	protocol := GetEnv("PASTES_PROTOCOL", "https")
	subdomainsEnabled := false
	if subdomains == "1" {
		subdomainsEnabled = true
	}

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/pastes).\n"
	intro += "Then write your paste post (e.g. feature.patch).\n"
	intro += "Finally, send your files to us:\n\n"
	intro += fmt.Sprintf("scp ~/pastes/* %s:/", domain)

	return &ConfigSite{
		SubdomainsEnabled: subdomainsEnabled,
		ConfigCms: config.ConfigCms{
			Domain:      domain,
			Port:        port,
			Protocol:    protocol,
			Email:       email,
			DbURL:       dbURL,
			Description: "a pastebin for hackers.",
			IntroText:   intro,
			Space:       "pastes",
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

func (c *ConfigSite) RawPostURL(username, filename string) string {
	fname := url.PathEscape(filename)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/raw/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/raw/%s/%s", username, fname)
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
		return fmt.Sprintf("//%s", c.Domain)
	}

	return "/"
}

func (c *ConfigSite) ReadURL() string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s/read", c.Protocol, c.Domain)
	}

	return "/read"
}

func (c *ConfigSite) StaticPath(fname string) string {
	return path.Join(c.Space, fname)
}

func CreateLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}
