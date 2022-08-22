package shared

import (
	"fmt"
	"html/template"
	"log"
	"net/url"
	"path"
	"strings"

	"git.sr.ht/~erock/pico/wish/cms/config"
	"go.uber.org/zap"
)

type SitePageData struct {
	Domain  template.URL
	HomeURL template.URL
	Email   string
}

type PageData struct {
	Site SitePageData
}

type ConfigSite struct {
	config.ConfigCms
	config.ConfigURL
	SubdomainsEnabled    bool
	CustomdomainsEnabled bool
	StorageDir           string
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

func (c *ConfigSite) PostURL(username, slug string) string {
	fname := url.PathEscape(slug)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/%s/%s", username, fname)

}

func (c *ConfigSite) FullPostURL(username, slug string, onSubdomain bool, withUserName bool) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
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

func (c *ConfigSite) RssBlogURL(username string, onSubdomain bool, withUserName bool, tag string) string {
	url := ""
	if c.IsSubdomains() && onSubdomain {
		url = fmt.Sprintf("%s://%s.%s/rss", c.Protocol, username, c.Domain)
	} else if withUserName {
		url = fmt.Sprintf("/%s/rss", username)
	} else {
		url = "/rss"
	}

	if tag != "" {
		return fmt.Sprintf("%s?tag=%s", url, tag)
	}

	return url
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

func (c *ConfigSite) RawPostURL(username, slug string) string {
	fname := url.PathEscape(slug)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/raw/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/raw/%s/%s", username, fname)
}

func (c *ConfigSite) ImgURL(username string, slug string, onSubdomain bool, withUserName bool) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
	if c.IsSubdomains() && onSubdomain {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	if withUserName {
		return fmt.Sprintf("/%s/%s", username, fname)
	}

	return fmt.Sprintf("/%s", fname)
}

func (c *ConfigSite) TagURL(username, tag string, onSubdomain, withUserName bool) string {
	tg := url.PathEscape(tag)
	return fmt.Sprintf("%s?tag=%s", c.FullBlogURL(username, onSubdomain, withUserName), tg)
}

func CreateLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}
