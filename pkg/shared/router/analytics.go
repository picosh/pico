package router

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/picosh/pico/pkg/db"
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
	if visit.PostID != "" {
		if !dbpool.HasFeatureByUser(visit.UserID, "plus") && !dbpool.HasFeatureByUser(visit.UserID, "prose") {
			return ErrAnalyticsDisabled
		}
	} else if !dbpool.HasFeatureByUser(visit.UserID, "analytics") {
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
	visit.ContentType = strings.ToLower(visit.ContentType)

	return nil
}
