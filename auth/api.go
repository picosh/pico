package auth

import (
	"bufio"
	"context"
	"crypto/hmac"
	"embed"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
	"github.com/picosh/utils/pipe/metrics"
)

//go:embed html/* public/*
var embedFS embed.FS

type oauth2Server struct {
	Issuer                                    string   `json:"issuer"`
	IntrospectionEndpoint                     string   `json:"introspection_endpoint"`
	IntrospectionEndpointAuthMethodsSupported []string `json:"introspection_endpoint_auth_methods_supported"`
	AuthorizationEndpoint                     string   `json:"authorization_endpoint"`
	TokenEndpoint                             string   `json:"token_endpoint"`
	ResponseTypesSupported                    []string `json:"response_types_supported"`
}

func generateURL(cfg *shared.ConfigSite, path string, space string) string {
	query := ""

	if space != "" {
		query = fmt.Sprintf("?space=%s", space)
	}

	return fmt.Sprintf("%s/%s%s", cfg.Domain, path, query)
}

func wellKnownHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		space := r.PathValue("space")
		if space == "" {
			space = r.URL.Query().Get("space")
		}

		p := oauth2Server{
			Issuer:                apiConfig.Cfg.Issuer,
			IntrospectionEndpoint: generateURL(apiConfig.Cfg, "introspect", space),
			IntrospectionEndpointAuthMethodsSupported: []string{
				"none",
			},
			AuthorizationEndpoint:  generateURL(apiConfig.Cfg, "authorize", ""),
			TokenEndpoint:          generateURL(apiConfig.Cfg, "token", ""),
			ResponseTypesSupported: []string{"code"},
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(p)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

type oauth2Introspection struct {
	Active   bool   `json:"active"`
	Username string `json:"username"`
}

func introspectHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.FormValue("token")
		apiConfig.Cfg.Logger.Info("introspect token", "token", token)

		user, err := apiConfig.Dbpool.FindUserForToken(token)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		p := oauth2Introspection{
			Active:   true,
			Username: user.Name,
		}

		space := r.URL.Query().Get("space")
		if space != "" {
			if !apiConfig.HasPlusOrSpace(user, space) {
				p.Active = false
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(p)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func authorizeHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		responseType := r.URL.Query().Get("response_type")
		clientID := r.URL.Query().Get("client_id")
		redirectURI := r.URL.Query().Get("redirect_uri")
		scope := r.URL.Query().Get("scope")

		apiConfig.Cfg.Logger.Info(
			"authorize handler",
			"responseType", responseType,
			"clientID", clientID,
			"redirectURI", redirectURI,
			"scope", scope,
		)

		ts, err := template.ParseFS(
			embedFS,
			"html/redirect.page.tmpl",
			"html/footer.partial.tmpl",
			"html/marketing-footer.partial.tmpl",
			"html/base.layout.tmpl",
		)

		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		err = ts.Execute(w, map[string]any{
			"response_type": responseType,
			"client_id":     clientID,
			"redirect_uri":  redirectURI,
			"scope":         scope,
		})

		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
	}
}

func redirectHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.FormValue("token")
		redirectURI := r.FormValue("redirect_uri")
		responseType := r.FormValue("response_type")

		apiConfig.Cfg.Logger.Info("redirect handler",
			"token", token,
			"redirectURI", redirectURI,
			"responseType", responseType,
		)

		if token == "" || redirectURI == "" || responseType != "code" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		url, err := url.Parse(redirectURI)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		urlQuery := url.Query()
		urlQuery.Add("code", token)

		url.RawQuery = urlQuery.Encode()

		http.Redirect(w, r, url.String(), http.StatusFound)
	}
}

type oauth2Token struct {
	AccessToken string `json:"access_token"`
}

func tokenHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		token := r.FormValue("code")
		redirectURI := r.FormValue("redirect_uri")
		grantType := r.FormValue("grant_type")

		apiConfig.Cfg.Logger.Info(
			"handle token",
			"token", token,
			"redirectURI", redirectURI,
			"grantType", grantType,
		)

		_, err := apiConfig.Dbpool.FindUserForToken(token)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}

		p := oauth2Token{
			AccessToken: token,
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(p)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

type sishData struct {
	PublicKey     string `json:"auth_key"`
	Username      string `json:"user"`
	RemoteAddress string `json:"remote_addr"`
}

func keyHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var data sishData

		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		space := r.URL.Query().Get("space")

		apiConfig.Cfg.Logger.Info(
			"handle key",
			"remoteAddress", data.RemoteAddress,
			"user", data.Username,
			"space", space,
			"publicKey", data.PublicKey,
		)

		user, err := apiConfig.Dbpool.FindUserForKey(data.Username, data.PublicKey)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if !apiConfig.HasPlusOrSpace(user, space) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		if !apiConfig.HasPrivilegedAccess(shared.GetApiToken(r)) {
			w.WriteHeader(http.StatusOK)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(user)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func userHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !apiConfig.HasPrivilegedAccess(shared.GetApiToken(r)) {
			w.WriteHeader(http.StatusForbidden)
			return
		}

		var data sishData

		err := json.NewDecoder(r.Body).Decode(&data)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		apiConfig.Cfg.Logger.Info(
			"handle key",
			"remoteAddress", data.RemoteAddress,
			"user", data.Username,
			"publicKey", data.PublicKey,
		)

		user, err := apiConfig.Dbpool.FindUserForName(data.Username)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		keys, err := apiConfig.Dbpool.FindKeysForUser(user)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(keys)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}

func rssHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiToken := r.PathValue("token")
		user, err := apiConfig.Dbpool.FindUserForToken(apiToken)
		if err != nil {
			apiConfig.Cfg.Logger.Error(
				"could not find user for token",
				"err", err.Error(),
				"token", apiToken,
			)
			http.Error(w, "invalid token", http.StatusNotFound)
			return
		}

		feed, err := shared.UserFeed(apiConfig.Dbpool, user.ID, apiToken)
		if err != nil {
			return
		}

		rss, err := feed.ToAtom()
		if err != nil {
			apiConfig.Cfg.Logger.Error("could not generate atom rss feed", "err", err.Error())
			http.Error(w, "could not generate atom rss feed", http.StatusInternalServerError)
		}

		w.Header().Add("Content-Type", "application/atom+xml")
		_, err = w.Write([]byte(rss))
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
		}
	}
}

type CustomDataMeta struct {
	PicoUsername string `json:"username"`
}

type OrderEventMeta struct {
	EventName  string          `json:"event_name"`
	CustomData *CustomDataMeta `json:"custom_data"`
}

type OrderEventData struct {
	Type string              `json:"type"`
	ID   string              `json:"id"`
	Attr *OrderEventDataAttr `json:"attributes"`
}

type OrderEventDataAttr struct {
	OrderNumber int       `json:"order_number"`
	Identifier  string    `json:"identifier"`
	UserName    string    `json:"user_name"`
	UserEmail   string    `json:"user_email"`
	CreatedAt   time.Time `json:"created_at"`
	Status      string    `json:"status"` // `paid`, `refund`
}

type OrderEvent struct {
	Meta *OrderEventMeta `json:"meta"`
	Data *OrderEventData `json:"data"`
}

// Status code must be 200 or else lemonsqueezy will keep retrying
// https://docs.lemonsqueezy.com/help/webhooks
func paymentWebhookHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		dbpool := apiConfig.Dbpool
		logger := apiConfig.Cfg.Logger
		const MaxBodyBytes = int64(65536)
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		payload, err := io.ReadAll(r.Body)

		w.Header().Add("content-type", "text/plain")

		if err != nil {
			logger.Error("error reading request body", "err", err.Error())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("error reading request body %s", err.Error())))
			return
		}

		event := OrderEvent{}

		if err := json.Unmarshal(payload, &event); err != nil {
			logger.Error("failed to parse webhook body JSON", "err", err.Error())
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(fmt.Sprintf("failed to parse webhook body JSON %s", err.Error())))
			return
		}

		hash := shared.HmacString(apiConfig.Cfg.SecretWebhook, string(payload))
		sig := r.Header.Get("X-Signature")
		if !hmac.Equal([]byte(hash), []byte(sig)) {
			logger.Error("invalid signature X-Signature")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("invalid signature x-signature"))
			return
		}

		if event.Meta == nil {
			logger.Error("no meta field found")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("no meta field found"))
			return
		}

		if event.Meta.EventName != "order_created" {
			logger.Error("event not order_created", "event", event.Meta.EventName)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event not order_created"))
			return
		}

		if event.Meta.CustomData == nil {
			logger.Error("no custom data found")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("no custom data found"))
			return
		}

		username := event.Meta.CustomData.PicoUsername

		if event.Data == nil || event.Data.Attr == nil {
			logger.Error("no data or data.attributes fields found")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("no data or data.attributes fields found"))
			return
		}

		email := event.Data.Attr.UserEmail
		created := event.Data.Attr.CreatedAt
		status := event.Data.Attr.Status
		txID := fmt.Sprint(event.Data.Attr.OrderNumber)

		user, err := apiConfig.Dbpool.FindUserForName(username)
		if err != nil {
			logger.Error("no user found with username", "username", username)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("no user found with username"))
			return
		}

		log := logger.With(
			"username", username,
			"email", email,
			"created", created,
			"paymentStatus", status,
			"txId", txID,
		)
		log = shared.LoggerWithUser(log, user)

		log.Info(
			"order_created event",
		)

		// https://checkout.pico.sh/buy/35b1be57-1e25-487f-84dd-5f09bb8783ec?discount=0&checkout[custom][username]=erock
		if username == "" {
			log.Error("no `?checkout[custom][username]=xxx` found in URL, cannot add pico+ membership")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("no `?checkout[custom][username]=xxx` found in URL, cannot add pico+ membership"))
			return
		}

		if status != "paid" {
			log.Error("status not paid")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("status not paid"))
			return
		}

		err = dbpool.AddPicoPlusUser(username, email, "lemonsqueezy", txID)
		if err != nil {
			log.Error("failed to add pico+ user", "err", err)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("status not paid"))
			return
		}

		err = AddPlusFeedForUser(dbpool, user.ID, email)
		if err != nil {
			log.Error("failed to add feed for user", "err", err)
		}

		log.Info("successfully added pico+ user")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("successfully added pico+ user"))
	}
}

func AddPlusFeedForUser(dbpool db.DB, userID, email string) error {
	// check if they already have a post grepping for the auth rss url
	posts, err := dbpool.FindPostsForUser(&db.Pager{Num: 1000, Page: 0}, userID, "feeds")
	if err != nil {
		return err
	}

	found := false
	for _, post := range posts.Data {
		if strings.Contains(post.Text, "https://auth.pico.sh/rss/") {
			found = true
		}
	}

	// don't need to do anything, they already have an auth post
	if found {
		return nil
	}

	token, err := dbpool.UpsertToken(userID, "pico-rss")
	if err != nil {
		return err
	}

	href := fmt.Sprintf("https://auth.pico.sh/rss/%s", token)
	text := fmt.Sprintf(`=: email %s
=: digest_interval 1day
=> %s`, email, href)
	now := time.Now()
	_, err = dbpool.InsertPost(&db.Post{
		UserID:    userID,
		Text:      text,
		Space:     "feeds",
		Slug:      "pico-plus",
		Filename:  "pico-plus",
		PublishAt: &now,
	})
	return err
}

// URL shortener for our pico+ URL.
func checkoutHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.PathValue("username")
		link := "https://checkout.pico.sh/buy/73c26cf9-3fac-44c3-b744-298b3032a96b"
		url := fmt.Sprintf(
			"%s?discount=0&checkout[custom][username]=%s",
			link,
			username,
		)
		http.Redirect(w, r, url, http.StatusMovedPermanently)
	}
}

type AccessLog struct {
	Status      int               `json:"status"`
	ServerID    string            `json:"server_id"`
	Request     AccessLogReq      `json:"request"`
	RespHeaders AccessRespHeaders `json:"resp_headers"`
}

type AccessLogReqHeaders struct {
	UserAgent []string `json:"User-Agent"`
	Referer   []string `json:"Referer"`
}

type AccessLogReq struct {
	ClientIP string              `json:"client_ip"`
	Method   string              `json:"method"`
	Host     string              `json:"host"`
	Uri      string              `json:"uri"`
	Headers  AccessLogReqHeaders `json:"headers"`
}

type AccessRespHeaders struct {
	ContentType []string `json:"Content-Type"`
}

func deserializeCaddyAccessLog(dbpool db.DB, access *AccessLog) (*db.AnalyticsVisits, error) {
	spaceRaw := strings.SplitN(access.ServerID, ".", 2)
	space := spaceRaw[0]
	host := access.Request.Host
	path := access.Request.Uri
	subdomain := ""

	// grab subdomain based on host
	if strings.HasSuffix(host, "tuns.sh") {
		subdomain = strings.TrimSuffix(host, ".tuns.sh")
	} else if strings.HasSuffix(host, "pgs.sh") {
		subdomain = strings.TrimSuffix(host, ".pgs.sh")
	} else if strings.HasSuffix(host, "prose.sh") {
		subdomain = strings.TrimSuffix(host, ".prose.sh")
	} else {
		subdomain = shared.GetCustomDomain(host, space)
	}

	// get user and namespace details from subdomain
	props, err := shared.GetProjectFromSubdomain(subdomain)
	if err != nil {
		return nil, fmt.Errorf("could not get project from subdomain %s: %w", subdomain, err)
	}

	// get user ID
	user, err := dbpool.FindUserForName(props.Username)
	if err != nil {
		return nil, fmt.Errorf("could not find user for name %s: %w", props.Username, err)
	}

	projectID := ""
	postID := ""
	if space == "pgs" { // figure out project ID
		project, err := dbpool.FindProjectByName(user.ID, props.ProjectName)
		if err != nil {
			return nil, fmt.Errorf(
				"could not find project by name, (user:%s, project:%s): %w",
				user.ID,
				props.ProjectName,
				err,
			)
		}
		projectID = project.ID
	} else if space == "prose" { // figure out post ID
		if path == "" || path == "/" {
			// ignore
		} else {
			cleanPath := strings.TrimPrefix(path, "/")
			post, err := dbpool.FindPostWithSlug(cleanPath, user.ID, space)
			if err != nil {
				return nil, fmt.Errorf(
					"could not find post with slug (path:%s, userId:%s, space:%s): %w",
					cleanPath,
					user.ID,
					space,
					err,
				)
			}
			postID = post.ID
		}
	}

	return &db.AnalyticsVisits{
		UserID:      user.ID,
		ProjectID:   projectID,
		PostID:      postID,
		Namespace:   space,
		Host:        host,
		Path:        path,
		IpAddress:   access.Request.ClientIP,
		UserAgent:   strings.Join(access.Request.Headers.UserAgent, " "),
		Referer:     strings.Join(access.Request.Headers.Referer, " "),
		ContentType: strings.Join(access.RespHeaders.ContentType, " "),
		Status:      access.Status,
	}, nil
}

func accessLogToVisit(dbpool db.DB, line string) (*db.AnalyticsVisits, error) {
	accessLog := AccessLog{}
	err := json.Unmarshal([]byte(line), &accessLog)
	if err != nil {
		return nil, fmt.Errorf("could not unmarshal line: %w", err)
	}

	return deserializeCaddyAccessLog(dbpool, &accessLog)
}

func metricDrainSub(ctx context.Context, dbpool db.DB, logger *slog.Logger, secret string) {
	drain := metrics.ReconnectReadMetrics(
		ctx,
		logger,
		shared.NewPicoPipeClient(),
		100,
		-1,
	)

	for {
		scanner := bufio.NewScanner(drain)
		for scanner.Scan() {
			line := scanner.Text()
			clean := strings.TrimSpace(line)

			fmt.Println(line)

			visit, err := accessLogToVisit(dbpool, clean)
			if err != nil {
				logger.Info("could not convert access log to a visit", "err", err)
				continue
			}

			logger.Info("received visit", "visit", visit)
			err = shared.AnalyticsVisitFromVisit(visit, dbpool, secret)
			if err != nil {
				logger.Info("could not record analytics visit", "err", err)
				continue
			}

			if !strings.HasPrefix(visit.ContentType, "text/html") {
				logger.Info("invalid content type", "contentType", visit.ContentType)
				continue
			}

			logger.Info("inserting visit", "visit", visit)
			err = dbpool.InsertVisit(visit)
			if err != nil {
				logger.Error("could not insert visit record", "err", err)
			}
		}
	}
}

func authMux(apiConfig *shared.ApiConfig) *http.ServeMux {
	serverRoot, err := fs.Sub(embedFS, "public")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServerFS(serverRoot)

	mux := http.NewServeMux()
	// ensure legacy router is disabled
	// GODEBUG=httpmuxgo121=0
	mux.Handle("GET /checkout/{username}", checkoutHandler())
	mux.Handle("GET /.well-known/oauth-authorization-server", wellKnownHandler(apiConfig))
	mux.Handle("GET /.well-known/oauth-authorization-server/{space}", wellKnownHandler(apiConfig))
	mux.Handle("POST /introspect", introspectHandler(apiConfig))
	mux.Handle("GET /authorize", authorizeHandler(apiConfig))
	mux.Handle("POST /token", tokenHandler(apiConfig))
	mux.Handle("POST /key", keyHandler(apiConfig))
	mux.Handle("POST /user", userHandler(apiConfig))
	mux.Handle("GET /rss/{token}", rssHandler(apiConfig))
	mux.Handle("POST /redirect", redirectHandler(apiConfig))
	mux.Handle("POST /webhook", paymentWebhookHandler(apiConfig))
	mux.HandleFunc("GET /main.css", fileServer.ServeHTTP)
	mux.HandleFunc("GET /card.png", fileServer.ServeHTTP)
	mux.HandleFunc("GET /favicon-16x16.png", fileServer.ServeHTTP)
	mux.HandleFunc("GET /favicon-32x32.png", fileServer.ServeHTTP)
	mux.HandleFunc("GET /apple-touch-icon.png", fileServer.ServeHTTP)
	mux.HandleFunc("GET /favicon.ico", fileServer.ServeHTTP)
	mux.HandleFunc("GET /robots.txt", fileServer.ServeHTTP)

	if apiConfig.Cfg.Debug {
		shared.CreatePProfRoutesMux(mux)
	}

	return mux
}

func StartApiServer() {
	debug := utils.GetEnv("AUTH_DEBUG", "0")

	cfg := &shared.ConfigSite{
		DbURL:         utils.GetEnv("DATABASE_URL", ""),
		Debug:         debug == "1",
		Issuer:        utils.GetEnv("AUTH_ISSUER", "pico.sh"),
		Domain:        utils.GetEnv("AUTH_DOMAIN", "http://0.0.0.0:3000"),
		Port:          utils.GetEnv("AUTH_WEB_PORT", "3000"),
		Secret:        utils.GetEnv("PICO_SECRET", ""),
		SecretWebhook: utils.GetEnv("PICO_SECRET_WEBHOOK", ""),
	}

	if cfg.SecretWebhook == "" {
		panic("must provide PICO_SECRET_WEBHOOK environment variable")
	}

	if cfg.Secret == "" {
		panic("must provide PICO_SECRET environment variable")
	}

	logger := shared.CreateLogger("auth")

	cfg.Logger = logger

	db := postgres.NewDB(cfg.DbURL, logger)
	defer db.Close()

	ctx := context.Background()

	// gather metrics in the auth service
	go metricDrainSub(ctx, db, logger, cfg.Secret)

	defer ctx.Done()

	apiConfig := &shared.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}

	mux := authMux(apiConfig)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info("starting server on port", "port", cfg.Port)

	err := http.ListenAndServe(portStr, mux)
	if err != nil {
		logger.Info("http-serve", "err", err.Error())
	}
}
