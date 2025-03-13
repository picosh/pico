package shared

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils/pipe/metrics"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	"github.com/x-way/crawlerdetect"
)

var internalCrawlers *crawlerdetect.CrawlerDetect

func init() {
	internalCrawlers = crawlerdetect.New()
	internalCrawlers.SetCrawlers([]string{
		`^Azure Traffic Manager Endpoint Monitor$`,
		`^Blackbox Exporter\/`,
		`^Prometheus\/`,
	})
}

func HmacString(secret, data string) string {
	hmacer := hmac.New(sha256.New, []byte(secret))
	hmacer.Write([]byte(data))
	dataHmac := hmacer.Sum(nil)
	return hex.EncodeToString(dataHmac)
}

func trackableUserAgent(agent string) error {
	// dont store requests from bots
	if crawlerdetect.IsCrawler(agent) || internalCrawlers.IsCrawler(agent) {
		return fmt.Errorf(
			"request is likely from a bot (User-Agent: %s)",
			CleanUserAgent(agent),
		)
	}
	return nil
}

func trackableRequest(r *http.Request) error {
	agent := r.UserAgent()
	return trackableUserAgent(agent)
}

func cleanIpAddress(ip string) (string, error) {
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		host = ip
	}
	// /24 IPv4 subnet mask
	// /64 IPv6 subnet mask
	anonymizer := ipanonymizer.NewWithMask(
		net.CIDRMask(24, 32),
		net.CIDRMask(64, 128),
	)
	anonIp, err := anonymizer.IPString(host)
	return anonIp, err
}

func cleanUrl(orig string) (string, string) {
	u, err := url.Parse(orig)
	if err != nil {
		return "", ""
	}
	return u.Host, u.Path
}

func cleanUrlFromRequest(r *http.Request) (string, string) {
	host := r.Header.Get("x-forwarded-host")
	if host == "" {
		host = r.URL.Host
	}
	if host == "" {
		host = r.Host
	}
	// we don't want query params in the url for security reasons
	return host, r.URL.Path
}

func CleanUserAgent(ua string) string {
	// truncate user-agent because http headers have no text limit
	if len(ua) > 1000 {
		return ua[:1000]
	}
	return strings.TrimSpace(ua)
}

func filterIp(host string) (string, error) {
	if host == "" {
		return "", nil
	}
	addr := net.ParseIP(host)
	if addr != nil {
		return "", fmt.Errorf("host is an ip")
	}
	return host, nil
}

func CleanReferer(raw string) (string, error) {
	ref := raw
	if ref == "" {
		return "", nil
	}
	// referer sometimes dont include scheme but we need it
	if !strings.HasPrefix(ref, "http") {
		ref = "https://" + ref
	}
	// we only want to store host for security reasons
	// https://developer.mozilla.org/en-US/docs/Web/Security/Referer_header:_privacy_and_security_concerns
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	hostname := u.Hostname()
	hostname, _ = filterIp(hostname)
	hostname = strings.TrimSpace(strings.ToLower(hostname))
	return hostname, err
}

func CleanHost(raw string) (string, error) {
	prep := strings.TrimSpace(strings.ToLower(raw))
	if prep == "" {
		return "", fmt.Errorf("host is blank")
	}
	// hosts dont usually include scheme but we need it
	if !strings.HasPrefix(prep, "http") {
		prep = "https://" + prep
	}
	// no clue why but our prod data contains periods
	prep = strings.Trim(prep, ".")
	// we only want to store host for security reasons
	// https://developer.mozilla.org/en-US/docs/Web/Security/Referer_header:_privacy_and_security_concerns
	u, err := url.Parse(prep)
	if err != nil {
		return raw, err
	}
	host := u.Hostname()
	host, err = filterIp(host)
	return host, err
}

var ErrAnalyticsDisabled = errors.New("owner does not have site analytics enabled")

func AnalyticsVisitFromVisit(visit *db.AnalyticsVisits, dbpool db.DB, secret string) error {
	if !dbpool.HasFeatureForUser(visit.UserID, "analytics") {
		return ErrAnalyticsDisabled
	}

	err := trackableUserAgent(visit.UserAgent)
	if err != nil {
		return err
	}

	ipAddress, err := cleanIpAddress(visit.IpAddress)
	if err != nil {
		return err
	}
	visit.IpAddress = HmacString(secret, ipAddress)
	_, path := cleanUrl(visit.Path)
	visit.Path = path

	referer, err := CleanReferer(visit.Referer)
	if err != nil {
		return err
	}
	visit.Referer = referer

	hostname, err := CleanHost(visit.Host)
	if err != nil {
		return err
	}
	visit.Host = hostname
	visit.UserAgent = CleanUserAgent(visit.UserAgent)

	return nil
}

func ipFromRequest(r *http.Request) string {
	// https://caddyserver.com/docs/caddyfile/directives/reverse_proxy#defaults
	ipOrig := r.Header.Get("x-forwarded-for")
	if ipOrig == "" {
		ipOrig = r.RemoteAddr
	}
	// probably means this is a web tunnel
	if ipOrig == "" || ipOrig == "@" {
		sshCtx, err := GetSshCtx(r)
		if err == nil {
			ipOrig = sshCtx.RemoteAddr().String()
		}
	}

	return ipOrig
}

func AnalyticsVisitFromRequest(r *http.Request, dbpool db.DB, userID string) (*db.AnalyticsVisits, error) {
	if !dbpool.HasFeatureForUser(userID, "analytics") {
		return nil, ErrAnalyticsDisabled
	}

	err := trackableRequest(r)
	if err != nil {
		return nil, err
	}

	ipAddress := ipFromRequest(r)
	host, path := cleanUrlFromRequest(r)

	return &db.AnalyticsVisits{
		UserID:    userID,
		Host:      host,
		Path:      path,
		IpAddress: ipAddress,
		UserAgent: r.UserAgent(),
		Referer:   r.Referer(),
		Status:    http.StatusOK,
	}, nil
}

func AnalyticsCollect(ch chan *db.AnalyticsVisits, dbpool db.DB, logger *slog.Logger) {
	drain := metrics.RegisterReconnectMetricRecorder(
		context.Background(),
		logger,
		NewPicoPipeClient(),
		100,
		10*time.Millisecond,
	)

	for visit := range ch {
		data, err := json.Marshal(visit)
		if err != nil {
			logger.Error("could not json marshall visit record", "err", err)
			continue
		}

		data = append(data, '\n')

		_, err = drain.Write(data)
		if err != nil {
			logger.Error("could not write to metric-drain", "err", err)
		}
	}
}
