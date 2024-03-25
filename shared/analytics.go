package shared

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"

	"github.com/picosh/pico/db"
	"github.com/simplesurance/go-ip-anonymizer/ipanonymizer"
	"github.com/x-way/crawlerdetect"
)

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
	// Create an anonymizer with a /16 IPv6 subnet mask and
	// a /64 IPv6 // subnet mask.
	anonymizer := ipanonymizer.NewWithMask(
		net.CIDRMask(16, 32),
		net.CIDRMask(64, 128),
	)
	anonIp, err := anonymizer.IPString(host)
	return anonIp, err
}

func cleanUrl(curl *url.URL) string {
	// we don't want query params in the url
	return fmt.Sprintf("%s%s", curl.Host, curl.Path)
}

func cleanUserAgent(ua string) string {
	// clip user-agent because http headers have no text limit
	if len(ua) > 1000 {
		return ua[:1000]
	}
	return ua
}

func AnalyticsVisitFromRequest(r *http.Request, userID string) (*db.AnalyticsVisits, error) {
	err := trackableRequest(r)
	if err != nil {
		return nil, err
	}

	ipAddress, err := cleanIpAddress(r.RemoteAddr)
	if err != nil {
		return nil, err
	}

	return &db.AnalyticsVisits{
		UserID:    userID,
		Url:       cleanUrl(r.URL),
		IpAddress: ipAddress,
		UserAgent: cleanUserAgent(r.UserAgent()),
	}, nil
}

func AnalyticsCollect(ch chan *db.AnalyticsVisits, dbpool db.DB, logger *slog.Logger) {
	for view := range ch {
		err := dbpool.InsertView(view)
		if err != nil {
			logger.Error("could not insert view record", "err", err)
		}
	}
}
