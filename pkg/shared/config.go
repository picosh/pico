package shared

import (
	"context"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"

	pipeLogger "github.com/picosh/utils/pipe/log"
)

var DefaultEmail = "hello@pico.sh"

type SitePageData struct {
	Domain  template.URL
	HomeURL template.URL
}

type PageData struct {
	Site SitePageData
}

// CreateURL builds links to users' sites on Domain, as seen from the host a
// request came in on.
type CreateURL struct {
	Protocol        string
	Domain          string
	Subdomain       bool
	UsernameInRoute bool
	HostDomain      string
}

// NewCreateURL builds links that point at users' subdomains.
func NewCreateURL(protocol, domain string) *CreateURL {
	return &CreateURL{
		Protocol:  protocol,
		Domain:    domain,
		Subdomain: true,
	}
}

func CreateURLFromRequest(protocol, domain string, r *http.Request) *CreateURL {
	hostDomain := strings.Split(r.Host, ":")[0]
	appDomain := strings.Split(domain, ":")[0]

	onSubdomain := strings.Contains(hostDomain, appDomain)
	withUserName := !onSubdomain && hostDomain == appDomain

	return &CreateURL{
		Protocol:        protocol,
		Domain:          domain,
		HostDomain:      hostDomain,
		Subdomain:       onSubdomain,
		UsernameInRoute: withUserName,
	}
}

func (c *CreateURL) FullBlogURL(username string) string {
	if c.Subdomain {
		return fmt.Sprintf("%s://%s.%s", c.Protocol, username, c.Domain)
	}

	if c.UsernameInRoute {
		return fmt.Sprintf("/%s", username)
	}

	return fmt.Sprintf("%s://%s", c.Protocol, c.HostDomain)
}

func (c *CreateURL) FullPostURL(username, slug string) string {
	fname := url.PathEscape(strings.TrimLeft(slug, "/"))

	if c.Subdomain {
		return fmt.Sprintf("%s://%s.%s/%s", c.Protocol, username, c.Domain, fname)
	}

	if c.UsernameInRoute {
		return fmt.Sprintf("%s://%s/%s/%s", c.Protocol, c.Domain, username, fname)
	}

	return fmt.Sprintf("%s://%s/%s", c.Protocol, c.HostDomain, fname)
}

func (c *CreateURL) RssBlogURL(username, tag string) string {
	url := ""
	if c.Subdomain {
		url = fmt.Sprintf("%s://%s.%s/rss", c.Protocol, username, c.Domain)
	} else if c.UsernameInRoute {
		url = fmt.Sprintf("/%s/rss", username)
	} else {
		url = "/rss"
	}

	if tag != "" {
		return fmt.Sprintf("%s?tag=%s", url, tag)
	}

	return url
}

func CreateLogger(space string, withPipe bool) *slog.Logger {
	logger := slog.New(
		slog.NewTextHandler(
			os.Stdout,
			&slog.HandlerOptions{
				AddSource: true,
				Level:     slog.LevelInfo,
			},
		),
	)

	if withPipe {
		conn := NewPicoPipeClient()
		logger = pipeLogger.RegisterReconnectLogger(context.Background(), logger, conn, 100, 10*time.Millisecond)
	}

	logger = logger.With("service", space)

	hostname, err := os.Hostname()
	if err == nil && hostname != "" {
		logger = logger.With("hostname", hostname)
	}

	nodename := os.Getenv("NODENAME")
	if nodename != "" {
		logger = logger.With("nodename", nodename)
	}

	slog.SetDefault(logger)

	return logger
}

func LoggerWithUser(logger *slog.Logger, user *db.User) *slog.Logger {
	return logger.With("user", user.Name, "userId", user.ID)
}
