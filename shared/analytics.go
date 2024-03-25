package shared

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"github.com/picosh/pico/db"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	"github.com/x-way/crawlerdetect"
)

func hmacString(secret, data string) string {
	hmacer := hmac.New(sha256.New, []byte(secret))
	hmacer.Write([]byte(data))
	dataHmac := hmacer.Sum(nil)
	return hex.EncodeToString(dataHmac)
}

func trackableRequest(r *http.Request) error {
	agent := r.UserAgent()
	// dont store requests from bots
	if crawlerdetect.IsCrawler(agent) {
		return fmt.Errorf(
			"request is likely from a bot (User-Agent: %s)",
			cleanUserAgent(agent),
		)
	}
	return nil
}

func cleanIpAddress(ip string) (string, error) {
	host, _, err := net.SplitHostPort(ip)
	if err != nil {
		return "", err
	}
	// /16 IPv4 subnet mask
	// /64 IPv6 subnet mask
	anonymizer := ipanonymizer.NewWithMask(
		net.CIDRMask(16, 32),
		net.CIDRMask(64, 128),
	)
	anonIp, err := anonymizer.IPString(host)
	return anonIp, err
}

func cleanUrl(curl *url.URL) (string, string) {
	// we don't want query params in the url for security reasons
	return curl.Host, curl.Path
}

func cleanUserAgent(ua string) string {
	// truncate user-agent because http headers have no text limit
	if len(ua) > 1000 {
		return ua[:1000]
	}
	return ua
}

func cleanReferer(ref string) (string, error) {
	// we only want to store host for security reasons
	// https://developer.mozilla.org/en-US/docs/Web/Security/Referer_header:_privacy_and_security_concerns
	u, err := url.Parse(ref)
	if err != nil {
		return "", err
	}
	return u.Host, nil
}

var ErrAnalyticsDisabled = errors.New("owner does not have site analytics enabled")

func AnalyticsVisitFromRequest(r *http.Request, userID string, secret string) (*db.AnalyticsVisits, error) {
	dbpool := GetDB(r)
	if !dbpool.HasFeatureForUser(userID, "analytics") {
		return nil, ErrAnalyticsDisabled
	}

	err := trackableRequest(r)
	if err != nil {
		return nil, err
	}

	ipAddress, err := cleanIpAddress(r.RemoteAddr)
	if err != nil {
		return nil, err
	}
	host, path := cleanUrl(r.URL)

	referer, err := cleanReferer(r.Referer())
	if err != nil {
		return nil, err
	}

	return &db.AnalyticsVisits{
		UserID:    userID,
		Host:      host,
		Path:      path,
		IpAddress: hmacString(secret, ipAddress),
		UserAgent: cleanUserAgent(r.UserAgent()),
		Referer:   referer,
		Status:    http.StatusOK,
	}, nil
}

func AnalyticsCollect(ch chan *db.AnalyticsVisits, dbpool db.DB, logger *slog.Logger) {
	for view := range ch {
		err := dbpool.InsertVisit(view)
		if err != nil {
			logger.Error("could not insert view record", "err", err)
		}
	}
}
