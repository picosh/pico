package shared

import (
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/utils"

	pipeLogger "github.com/picosh/pubsub/log"
)

var DefaultEmail = "hello@pico.sh"

type SitePageData struct {
	Domain  template.URL
	HomeURL template.URL
}

type PageData struct {
	Site SitePageData
}

type ConfigSite struct {
	Debug              bool
	SendgridKey        string
	Secret             string
	Domain             string
	Port               string
	PortOverride       string
	Protocol           string
	DbURL              string
	StorageDir         string
	MinioURL           string
	MinioUser          string
	MinioPass          string
	Space              string
	AllowedExt         []string
	HiddenPosts        []string
	MaxSize            uint64
	MaxAssetSize       int64
	MaxSpecialFileSize int64
	Logger             *slog.Logger
}

func NewConfigSite() *ConfigSite {
	return &ConfigSite{}
}

type CreateURL struct {
	Subdomain       bool
	UsernameInRoute bool
	HostDomain      string
	AppDomain       string
	Username        string
	Cfg             *ConfigSite
}

func NewCreateURL(cfg *ConfigSite) *CreateURL {
	return &CreateURL{
		Cfg:       cfg,
		Subdomain: cfg.IsSubdomains(),
	}
}

func CreateURLFromRequest(cfg *ConfigSite, r *http.Request) *CreateURL {
	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(cfg.Domain, ":")[0]

	onSubdomain := cfg.IsSubdomains() && strings.Contains(hostDomain, appDomain)
	withUserName := !cfg.IsCustomdomains() || (!onSubdomain && hostDomain == appDomain)

	return &CreateURL{
		Cfg:             cfg,
		AppDomain:       appDomain,
		HostDomain:      hostDomain,
		Subdomain:       onSubdomain,
		UsernameInRoute: withUserName,
	}
}

func (c *ConfigSite) GetSiteData() *SitePageData {
	return &SitePageData{
		Domain:  template.URL(c.Domain),
		HomeURL: template.URL(c.HomeURL()),
	}
}

func (c *ConfigSite) IsSubdomains() bool {
	return true
}

func (c *ConfigSite) IsCustomdomains() bool {
	return true
}

func (c *ConfigSite) HomeURL() string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return fmt.Sprintf("//%s", c.Domain)
	}

	return "/"
}

func (c *ConfigSite) ReadURL() string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return fmt.Sprintf("%s://%s", c.Protocol, c.Domain)
	}

	return "/"
}

func (c *ConfigSite) StaticPath(fname string) string {
	return path.Join(c.Space, fname)
}

func (c *ConfigSite) BlogURL(username string) string {
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
	}

	return fmt.Sprintf("/%s", username)
}

func (c *ConfigSite) CssURL(username string) string {
	if c.IsSubdomains() || c.IsCustomdomains() {
		return "/_styles.css"
	}

	return fmt.Sprintf("/%s/styles.css", username)
}

func (c *ConfigSite) PostURL(username, slug string) string {
	fname := url.PathEscape(slug)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/%s/%s", username, fname)

}

func (c *ConfigSite) RawPostURL(username, slug string) string {
	fname := url.PathEscape(slug)
	if c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/raw/%s", c.Protocol, username, c.Domain, fname)
	}

	return fmt.Sprintf("/raw/%s/%s", username, fname)
}

func (c *ConfigSite) ImgFullURL(username, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
	return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
}

func (c *ConfigSite) FullBlogURL(curl *CreateURL, username string) string {
	if c.IsSubdomains() && curl.Subdomain {
		return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
	}

	if curl.UsernameInRoute {
		return fmt.Sprintf("/%s", username)
	}

	return fmt.Sprintf("%s://%s", c.Protocol, curl.HostDomain)
}

func (c *ConfigSite) FullPostURL(curl *CreateURL, username, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))

	if curl.Subdomain && c.IsSubdomains() {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	if curl.UsernameInRoute {
		return fmt.Sprintf("%s://%s/%s/%s", c.Protocol, c.Domain, username, fname)
	}

	return fmt.Sprintf("%s://%s/%s", c.Protocol, curl.HostDomain, fname)
}

func (c *ConfigSite) RssBlogURL(curl *CreateURL, username, tag string) string {
	url := ""
	if c.IsSubdomains() && curl.Subdomain {
		url = fmt.Sprintf("%s://%s.%s/rss", c.Protocol, username, c.Domain)
	} else if curl.UsernameInRoute {
		url = fmt.Sprintf("/%s/rss", username)
	} else {
		url = "/rss"
	}

	if tag != "" {
		return fmt.Sprintf("%s?tag=%s", url, tag)
	}

	return url
}

func (c *ConfigSite) ImgURL(curl *CreateURL, username string, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
	if c.IsSubdomains() && curl.Subdomain {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	if curl.UsernameInRoute {
		return fmt.Sprintf("/%s/%s", username, fname)
	}

	return fmt.Sprintf("/%s", fname)
}

func (c *ConfigSite) ImgPostURL(curl *CreateURL, username string, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
	if c.IsSubdomains() && curl.Subdomain {
		return fmt.Sprintf("%s://%s.%s/p/%s", c.Protocol, username, c.Domain, fname)
	}

	if curl.UsernameInRoute {
		return fmt.Sprintf("/%s/p/%s", username, fname)
	}

	return fmt.Sprintf("/p/%s", fname)
}

func (c *ConfigSite) ImgOrigURL(curl *CreateURL, username string, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))
	if c.IsSubdomains() && curl.Subdomain {
		return fmt.Sprintf("%s://%s.%s/o/%s", c.Protocol, username, c.Domain, fname)
	}

	if curl.UsernameInRoute {
		return fmt.Sprintf("/%s/o/%s", username, fname)
	}

	return fmt.Sprintf("/o/%s", fname)
}

func (c *ConfigSite) TagURL(curl *CreateURL, username, tag string) string {
	tg := url.PathEscape(tag)
	return fmt.Sprintf("%s?tag=%s", c.FullBlogURL(curl, username), tg)
}

func (c *ConfigSite) AssetURL(username, projectName, fpath string) string {
	if username == projectName {
		return fmt.Sprintf(
			"%s://%s.%s/%s",
			c.Protocol,
			username,
			c.Domain,
			fpath,
		)
	}

	return fmt.Sprintf(
		"%s://%s-%s.%s/%s",
		c.Protocol,
		username,
		projectName,
		c.Domain,
		fpath,
	)
}

func CreateLogger(space string) *slog.Logger {
	opts := &slog.HandlerOptions{
		AddSource: true,
	}
	log := slog.New(
		slog.NewTextHandler(os.Stdout, opts),
	)

	newLogger := log

	if strings.ToLower(utils.GetEnv("PICO_PIPE_ENABLED", "true")) == "true" {
		newLog, err := pipeLogger.SendLogRegister(log, &pipeLogger.PubSubConnectionInfo{
			RemoteHost:     utils.GetEnv("PICO_PIPE_ENDPOINT", "pipe.pico.sh:22"),
			KeyLocation:    utils.GetEnv("PICO_PIPE_KEY", "ssh_data/term_info_ed25519"),
			KeyPassphrase:  utils.GetEnv("PICO_PIPE_PASSPHRASE", ""),
			RemoteHostname: utils.GetEnv("PICO_PIPE_REMOTE_HOST", "pipe.pico.sh"),
			RemoteUser:     utils.GetEnv("PICO_PIPE_USER", "pico"),
		}, 100)

		if err == nil {
			newLogger = newLog
		} else {
			slog.Error("unable to start send logger", "error", err)
		}
	}

	return newLogger.With("service", space)
}

func LoggerWithUser(logger *slog.Logger, user *db.User) *slog.Logger {
	return logger.With("user", user.Name, "userId", user.ID)
}
