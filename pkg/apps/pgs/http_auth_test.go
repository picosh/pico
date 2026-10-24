package pgs

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
	"golang.org/x/net/html"
)

func loginFormValues(t *testing.T, body string) url.Values {
	t.Helper()
	values := url.Values{}
	tokens := html.NewTokenizer(strings.NewReader(body))
	for {
		switch tokens.Next() {
		case html.ErrorToken:
			if err := tokens.Err(); err != io.EOF {
				t.Fatal(err)
			}
			if values.Get("redirect") == "" {
				t.Fatal("login form is missing redirect")
			}
			return values
		case html.StartTagToken, html.SelfClosingTagToken:
			token := tokens.Token()
			if token.Data != "input" {
				continue
			}
			var name, value string
			for _, attr := range token.Attr {
				switch attr.Key {
				case "name":
					name = attr.Val
				case "value":
					value = attr.Val
				}
			}
			if name != "" {
				values.Set(name, value)
			}
		}
	}
}

func TestLoginPreservesRequestURL(t *testing.T) {
	t.Chdir("../../..")
	logger := slog.Default()
	dbpool := NewPgsDb(logger)
	project, err := dbpool.FindProjectByName(dbpool.Users[0].ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	project.Acl.Type = "http-pass"
	project.Acl.Data = []string{"test-password"}
	st, err := storage.NewStorageMemory(map[string]map[string]string{
		shared.GetAssetBucketName(dbpool.Users[0].ID): {},
	})
	if err != nil {
		t.Fatal(err)
	}
	pubsub := NewPubsubChan()
	t.Cleanup(func() { _ = pubsub.Close() })
	cfg := NewConfig(logger, dbpool, st, pubsub)
	cfg.Domain = "pgs.test"
	router := NewWebRouter(cfg)

	paths := []string{
		"/",
		"/?page=2&sort=asc",
		"/docs/page.html?tag=one&tag=two&empty=&q=a+b%26c%3Dd",
		"/docs/a%20b.html?next=%2Fother%3Fx%3D1%26y%3D2",
		"/docs/page.html?redirect=%2Felsewhere&error=invalid",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, dbpool.mkpath(path), nil))
			if response.Code != http.StatusForbidden {
				t.Fatalf("login form status = %d", response.Code)
			}
			form := loginFormValues(t, response.Body.String())
			if got := form.Get("redirect"); got != path {
				t.Fatalf("form redirect = %q, want %q", got, path)
			}

			for _, password := range []string{"wrong-password", "", "test-password"} {
				form.Set("password", password)
				request := httptest.NewRequest(http.MethodPost, dbpool.mkpath("/pgs/login"), strings.NewReader(form.Encode()))
				request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
				response = httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if password != "test-password" {
					if response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "Invalid password") {
						t.Fatalf("invalid password response: %d %s", response.Code, response.Body.String())
					}
					form = loginFormValues(t, response.Body.String())
					if got := form.Get("redirect"); got != path {
						t.Fatalf("retry redirect = %q, want %q", got, path)
					}
					continue
				}
				if response.Code != http.StatusSeeOther {
					t.Fatalf("login status = %d", response.Code)
				}
				if got := response.Header().Get("Location"); got != path {
					t.Errorf("redirect = %q, want %q", got, path)
				}
				cookies := response.Result().Cookies()
				if len(cookies) != 1 || cookies[0].Name != getCookieName(project.Name) || cookies[0].Value != project.ID {
					t.Errorf("login did not set the project session cookie")
				}
			}
		})
	}
}

func TestLoginRejectsExternalRedirect(t *testing.T) {
	t.Chdir("../../..")
	for _, target := range []string{"", "https://example.com/path", "//example.com/path", `/\example.com/path`, "relative/path", "/\nLocation: https://example.com"} {
		t.Run(target, func(t *testing.T) {
			form := url.Values{"project": {"test"}, "redirect": {target}}
			request := httptest.NewRequest(http.MethodPost, "https://example.pgs.test/pgs/login", strings.NewReader(form.Encode()))
			request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			response := httptest.NewRecorder()
			handleLogin(response, request, &Config{Logger: slog.Default()})
			if got := loginFormValues(t, response.Body.String()).Get("redirect"); got != "/" {
				t.Errorf("redirect = %q, want /", got)
			}
		})
	}
}
