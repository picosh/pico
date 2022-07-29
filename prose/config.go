package prose

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
	SubdomainsEnabled    bool
	CustomdomainsEnabled bool
}

func NewConfigSite() *ConfigSite {
	domain := GetEnv("PROSE_DOMAIN", "prose.sh")
	email := GetEnv("PROSE_EMAIL", "hello@prose.sh")
	subdomains := GetEnv("PROSE_SUBDOMAINS", "0")
	customdomains := GetEnv("PROSE_CUSTOMDOMAINS", "0")
	port := GetEnv("PROSE_WEB_PORT", "3000")
	protocol := GetEnv("PROSE_PROTOCOL", "https")
	dbURL := GetEnv("DATABASE_URL", "")
	subdomainsEnabled := false
	if subdomains == "1" {
		subdomainsEnabled = true
	}

	customdomainsEnabled := false
	if customdomains == "1" {
		customdomainsEnabled = true
	}

	intro := "To get started, enter a username.\n"
	intro += "Then create a folder locally (e.g. ~/blog).\n"
	intro += "Then write your post in markdown files (e.g. hello-world.md).\n"
	intro += "Finally, send your files to us:\n\n"
	intro += fmt.Sprintf("scp ~/blog/*.md %s:/", domain)

	return &ConfigSite{
		SubdomainsEnabled:    subdomainsEnabled,
		CustomdomainsEnabled: customdomainsEnabled,
		ConfigCms: config.ConfigCms{
			Domain:      domain,
			Email:       email,
			Port:        port,
			Protocol:    protocol,
			DbURL:       dbURL,
			Description: "a blog platform for hackers.",
			IntroText:   intro,
			Space:       "prose",
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

func (c *ConfigSite) FullBlogURL(username string, onSubdomain bool, withUserName bool) string {
	if c.IsSubdomains() && onSubdomain {
		return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
	}

	if withUserName {
		return fmt.Sprintf("/%s", username)
	}

	return "/"
}

func (c *ConfigSite) PostURL(username, filename string) string {
	fname := url.PathEscape(filename)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/%s/%s", username, fname)

}

func (c *ConfigSite) FullPostURL(username, filename string, onSubdomain bool, withUserName bool) string {
	fname := url.PathEscape(filename)
	if c.IsSubdomains() && onSubdomain {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	if withUserName {
		return fmt.Sprintf("/%s/%s", username, fname)
	}

	return fmt.Sprintf("/%s", fname)
}

func (c *ConfigSite) IsSubdomains() bool {
	return c.SubdomainsEnabled
}

func (c *ConfigSite) IsCustomdomains() bool {
	return c.CustomdomainsEnabled
}

func (c *ConfigSite) RssBlogURL(username string, onSubdomain bool, withUserName bool) string {
	if c.IsSubdomains() && onSubdomain {
		return fmt.Sprintf("%s://%s.%s/rss", c.Protocol, username, c.Domain)
	}

	if withUserName {
		return fmt.Sprintf("/%s/rss", username)
	}

	return "/rss"
}

func (c *ConfigSite) HomeURL() string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return fmt.Sprintf("//%s", c.Domain)
	}

	return "/"
}

func (c *ConfigSite) ReadURL() string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return fmt.Sprintf("%s://%s/read", c.Protocol, c.Domain)
	}

	return "/read"
}

func (c *ConfigSite) CssURL(username string) string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return fmt.Sprintf("%s://%s.%s/_styles.css", c.Protocol, username, c.Domain)
	}

	return fmt.Sprintf("/%s/styles.css", username)
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
