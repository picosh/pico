package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/stub"
	"github.com/picosh/pico/pkg/shared"
)

var testUserID = "user-1"
var testUsername = "user-a"

func TestPaymentWebhook(t *testing.T) {
	apiConfig := setupTest()

	event := OrderEvent{
		Meta: &OrderEventMeta{
			EventName: "order_created",
			CustomData: &CustomDataMeta{
				PicoUsername: testUsername,
			},
		},
		Data: &OrderEventData{
			Attr: &OrderEventDataAttr{
				UserEmail:   "auth@pico.test",
				CreatedAt:   time.Now(),
				Status:      "paid",
				OrderNumber: 1337,
			},
		},
	}
	jso, err := json.Marshal(event)
	bail(err)
	hash := shared.HmacString(apiConfig.Cfg.SecretWebhook, string(jso))
	body := bytes.NewReader(jso)

	request := httptest.NewRequest("POST", mkpath("/webhook"), body)
	request.Header.Add("X-signature", hash)
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	testResponse(t, responseRecorder, 200, "text/plain")

	posts, err := apiConfig.Dbpool.FindPostsForUser(&db.Pager{Num: 1000, Page: 0}, testUserID, "feeds")
	if err != nil {
		t.Error("could not find posts for user")
	}
	for _, post := range posts.Data {
		if post.Filename != "pico-plus" {
			continue
		}
		expectedText := `=: email auth@pico.test
=: cron */10 * * * *
=: inline_content true
=> https://auth.pico.sh/rss/123
=> https://blog.pico.sh/rss`
		if post.Text != expectedText {
			t.Errorf("Want pico plus feed file %s, got %s", expectedText, post.Text)
		}
	}
}

func TestUser(t *testing.T) {
	apiConfig := setupTest()

	data := sishData{
		Username: testUsername,
	}
	jso, err := json.Marshal(data)
	bail(err)
	body := bytes.NewReader(jso)

	request := httptest.NewRequest("POST", mkpath("/user"), body)
	request.Header.Add("Authorization", "Bearer 123")
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	testResponse(t, responseRecorder, 200, "application/json")
}

func TestKey(t *testing.T) {
	apiConfig := setupTest()

	data := sishData{
		Username:  testUsername,
		PublicKey: "zzz",
	}
	jso, err := json.Marshal(data)
	bail(err)
	body := bytes.NewReader(jso)

	request := httptest.NewRequest("POST", mkpath("/key"), body)
	request.Header.Add("Authorization", "Bearer 123")
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	testResponse(t, responseRecorder, 200, "application/json")
}

func TestCheckout(t *testing.T) {
	apiConfig := setupTest()

	request := httptest.NewRequest("GET", mkpath("/checkout/"+testUsername), strings.NewReader(""))
	request.Header.Add("Authorization", "Bearer 123")
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	loc := responseRecorder.Header().Get("Location")
	if loc != "https://checkout.pico.sh/buy/73c26cf9-3fac-44c3-b744-298b3032a96b?discount=0&checkout[custom][username]=user-a" {
		t.Errorf("Have Location %s, want checkout", loc)
	}
	if responseRecorder.Code != http.StatusMovedPermanently {
		t.Errorf("Want status '%d', got '%d'", http.StatusMovedPermanently, responseRecorder.Code)
		return
	}
}

func TestIntrospect(t *testing.T) {
	apiConfig := setupTest()

	request := httptest.NewRequest("POST", mkpath("/introspect?token=123"), strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	testResponse(t, responseRecorder, 200, "application/json")
}

func TestToken(t *testing.T) {
	apiConfig := setupTest()

	request := httptest.NewRequest("POST", mkpath("/token?code=123"), strings.NewReader(""))
	responseRecorder := httptest.NewRecorder()

	mux := authMux(apiConfig)
	mux.ServeHTTP(responseRecorder, request)

	testResponse(t, responseRecorder, 200, "application/json")
}

func TestAuthApi(t *testing.T) {
	apiConfig := setupTest()
	tt := []*ApiExample{
		{
			name:        "authorize",
			path:        "/authorize?response_type=json&client_id=333&redirect_uri=pico.test&scope=admin",
			status:      http.StatusOK,
			contentType: "text/html; charset=utf-8",
			dbpool:      apiConfig.Dbpool,
		},
		{
			name:        "rss",
			path:        "/rss/123",
			status:      http.StatusOK,
			contentType: "application/atom+xml",
			dbpool:      apiConfig.Dbpool,
		},
		{
			name:        "fileserver",
			path:        "/robots.txt",
			status:      http.StatusOK,
			contentType: "text/plain; charset=utf-8",
			dbpool:      apiConfig.Dbpool,
		},
		{
			name:        "well-known",
			path:        "/.well-known/oauth-authorization-server",
			status:      http.StatusOK,
			contentType: "application/json",
			dbpool:      apiConfig.Dbpool,
		},
	}

	for _, tc := range tt {
		t.Run(tc.name, func(t *testing.T) {
			request := httptest.NewRequest("GET", mkpath(tc.path), strings.NewReader(""))
			responseRecorder := httptest.NewRecorder()

			mux := authMux(apiConfig)
			mux.ServeHTTP(responseRecorder, request)

			testResponse(t, responseRecorder, tc.status, tc.contentType)
		})
	}
}

type ApiExample struct {
	name        string
	path        string
	status      int
	contentType string
	dbpool      db.DB
}

type AuthDb struct {
	*stub.StubDB
	Posts []*db.Post
}

func (a *AuthDb) AddPicoPlusUser(username, email, from, txid string) error {
	return nil
}

func (a *AuthDb) FindUserByName(username string) (*db.User, error) {
	return &db.User{ID: testUserID, Name: username}, nil
}

func (a *AuthDb) FindUserForKey(username string, pubkey string) (*db.User, error) {
	return &db.User{ID: testUserID, Name: username}, nil
}

func (a *AuthDb) FindUserForToken(token string) (*db.User, error) {
	if token != "123" {
		return nil, fmt.Errorf("invalid token")
	}
	return &db.User{ID: testUserID, Name: testUsername}, nil
}

func (a *AuthDb) HasFeatureForUser(userID string, feature string) bool {
	return true
}

func (a *AuthDb) FindKeysForUser(user *db.User) ([]*db.PublicKey, error) {
	return []*db.PublicKey{{ID: "1", UserID: user.ID, Name: "my-key", Key: "nice-pubkey", CreatedAt: &time.Time{}}}, nil
}

func (a *AuthDb) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	now := time.Date(2021, 8, 15, 14, 30, 45, 100, time.UTC)
	oneDayWarning := now.AddDate(0, 0, 1)
	return &db.FeatureFlag{ID: "2", UserID: userID, Name: "plus", ExpiresAt: &oneDayWarning, CreatedAt: &now}, nil
}

func (a *AuthDb) InsertPost(post *db.Post) (*db.Post, error) {
	a.Posts = append(a.Posts, post)
	return post, nil
}

func (a *AuthDb) FindPostsForUser(pager *db.Pager, userID, space string) (*db.Paginate[*db.Post], error) {
	return &db.Paginate[*db.Post]{
		Data: a.Posts,
	}, nil
}

func (a *AuthDb) UpsertToken(string, string) (string, error) {
	return "123", nil
}

func NewAuthDb(logger *slog.Logger) *AuthDb {
	sb := stub.NewStubDB(logger)
	return &AuthDb{
		StubDB: sb,
	}
}

func mkpath(path string) string {
	return fmt.Sprintf("https://auth.pico.test%s", path)
}

func setupTest() *shared.ApiConfig {
	logger := shared.CreateLogger("auth-test", false)
	cfg := &shared.ConfigSite{
		Issuer:        "auth.pico.test",
		Domain:        "http://0.0.0.0:3000",
		Port:          "3000",
		Secret:        "",
		SecretWebhook: "my-secret",
	}
	cfg.Logger = logger
	db := NewAuthDb(cfg.Logger)
	apiConfig := &shared.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}

	return apiConfig
}

func testResponse(t *testing.T, responseRecorder *httptest.ResponseRecorder, status int, contentType string) {
	if responseRecorder.Code != status {
		t.Errorf("Want status '%d', got '%d'", status, responseRecorder.Code)
		return
	}

	ct := responseRecorder.Header().Get("content-type")
	if ct != contentType {
		t.Errorf("Want content type '%s', got '%s'", contentType, ct)
		return
	}

	body := strings.TrimSpace(responseRecorder.Body.String())
	snaps.MatchSnapshot(t, body)
}

func bail(err error) {
	if err != nil {
		panic(bail)
	}
}
