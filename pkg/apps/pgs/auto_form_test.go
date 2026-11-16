package pgs

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/storage"
)

type tokenPgsDb struct {
	*PgsDb
}

func (d tokenPgsDb) FindUserByToken(token string) (*db.User, error) {
	return d.Users[0], nil
}

func TestAutoFormRssEscapesFields(t *testing.T) {
	t.Chdir("../../..")
	logger := slog.New(slog.DiscardHandler)
	dbpool := tokenPgsDb{NewPgsDb(logger)}
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)

	form := url.Values{"<b>name</b>": {"<script>alert(1)</script>"}}
	request := httptest.NewRequest(http.MethodPost, dbpool.mkpath("/pgs/forms/contact"), strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("submit status = %d", response.Code)
	}

	response = httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "https://pgs.test/rss/forms/token", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("rss status = %d", response.Code)
	}
	body := response.Body.String()
	// The feed XML-escapes content once, so raw markup from a field would
	// show up as &lt;script&gt;. Escaped field markup is escaped twice.
	if strings.Contains(body, "&lt;script&gt;") || strings.Contains(body, "&lt;b&gt;") {
		t.Fatalf("form fields reached the feed as markup: %s", body)
	}
	if !strings.Contains(body, "&amp;lt;script&amp;gt;") {
		t.Fatalf("escaped field missing from feed: %s", body)
	}
}

func TestAutoFormRejectsLargeSubmissions(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	dbpool := NewPgsDb(logger)
	st, err := storage.NewStorageMemory(map[string]map[string]string{})
	if err != nil {
		t.Fatal(err)
	}
	cfg := NewConfig(logger, dbpool, st, discardPubsub{})
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)

	form := url.Values{"field": {strings.Repeat("a", maxFormSize)}}
	request := httptest.NewRequest(http.MethodPost, dbpool.mkpath("/pgs/forms/contact"), strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.Code)
	}
	if len(dbpool.FormEntries) != 0 {
		t.Fatal("oversized form was stored")
	}
}
