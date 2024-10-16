package pgs

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/stub"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
)

var testUserID = "user-1"
var testUsername = "user"

type ApiExample struct {
	name        string
	path        string
	want        string
	status      int
	contentType string

	dbpool  db.DB
	storage map[string]map[string]string
}

type PgsDb struct {
	*stub.StubDB
}

func NewPgsDb(logger *slog.Logger) *PgsDb {
	sb := stub.NewStubDB(logger)
	return &PgsDb{
		StubDB: sb,
	}
}

func (p *PgsDb) FindUserForName(name string) (*db.User, error) {
	return &db.User{
		ID:   testUserID,
		Name: testUsername,
	}, nil
}

func (p *PgsDb) FindProjectByName(userID, name string) (*db.Project, error) {
	return &db.Project{
		ID:         "project-1",
		UserID:     userID,
		Name:       name,
		ProjectDir: name,
		Username:   testUsername,
		Acl: db.ProjectAcl{
			Type: "public",
		},
	}, nil
}

func mkpath(path string) string {
	return fmt.Sprintf("https://%s-test.pgs.test%s", testUsername, path)
}

func TestApiBasic(t *testing.T) {
	bucketName := shared.GetAssetBucketName(testUserID)
	cfg := NewConfigSite()
	cfg.Domain = "pgs.test"
	tt := []*ApiExample{
		{
			name:        "basic",
			path:        "/",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/index.html": "hello world!",
				},
			},
		},
		{
			name:        "direct-file",
			path:        "/test.html",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/test.html": "hello world!",
				},
			},
		},
		{
			name:        "subdir-301-redirect",
			path:        "/subdir",
			want:        `<a href="/subdir/">Moved Permanently</a>.`,
			status:      http.StatusMovedPermanently,
			contentType: "text/html; charset=utf-8",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/subdir/index.html": "hello world!",
				},
			},
		},
		{
			name:        "redirects-file-301",
			path:        "/anything",
			want:        `<a href="/about.html">Moved Permanently</a>.`,
			status:      http.StatusMovedPermanently,
			contentType: "text/html; charset=utf-8",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/_redirects": "/anything /about.html 301",
					"test/about.html": "hello world!",
				},
			},
		},
		{
			name:        "subdir-direct",
			path:        "/subdir/index.html",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/subdir/index.html": "hello world!",
				},
			},
		},
		{
			name:        "spa",
			path:        "/anything",
			want:        "hello world!",
			status:      http.StatusOK,
			contentType: "text/html",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/_redirects": "/* /index.html 200",
					"test/index.html": "hello world!",
				},
			},
		},
		{
			name:        "not-found",
			path:        "/anything",
			want:        "404 not found",
			status:      http.StatusNotFound,
			contentType: "text/plain; charset=utf-8",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {},
			},
		},
		{
			name:        "not-found-custom",
			path:        "/anything",
			want:        "boom!",
			status:      http.StatusNotFound,
			contentType: "text/html",

			dbpool: NewPgsDb(cfg.Logger),
			storage: map[string]map[string]string{
				bucketName: {
					"test/404.html": "boom!",
				},
			},
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", mkpath(tc.path), strings.NewReader(""))
			responseRecorder := httptest.NewRecorder()

			st, _ := storage.NewStorageMemory(tc.storage)
			ch := make(chan *db.AnalyticsVisits)
			apiConfig := &shared.ApiConfig{
				Cfg:            cfg,
				Dbpool:         tc.dbpool,
				Storage:        st,
				AnalyticsQueue: ch,
			}
			handler := shared.CreateServe(mainRoutes, createSubdomainRoutes(publicPerm), apiConfig)
			router := http.HandlerFunc(handler)
			router(responseRecorder, request)

			if responseRecorder.Code != tc.status {
				t.Errorf("Want status '%d', got '%d'", tc.status, responseRecorder.Code)
			}

			ct := responseRecorder.Header().Get("content-type")
			if ct != tc.contentType {
				t.Errorf("Want status '%s', got '%s'", tc.contentType, ct)
			}

			body := strings.TrimSpace(responseRecorder.Body.String())
			if body != tc.want {
				t.Errorf("Want '%s', got '%s'", tc.want, body)
			}
		})
	}
}
