package auth

import (
	"bufio"
	"context"
	"crypto/hmac"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/feeds"
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

func genFeedItem(now time.Time, expiresAt time.Time, warning time.Time, txt string) *feeds.Item {
	if now.After(warning) {
		content := fmt.Sprintf(
			"Your pico+ membership is going to expire on %s",
			expiresAt.Format("2006-01-02 15:04:05"),
		)
		return &feeds.Item{
			Id:          fmt.Sprintf("%d", warning.Unix()),
			Title:       fmt.Sprintf("pico+ %s expiration notice", txt),
			Link:        &feeds.Link{Href: "https://pico.sh"},
			Content:     content,
			Created:     warning,
			Updated:     warning,
			Description: content,
			Author:      &feeds.Author{Name: "team pico"},
		}
	}

	return nil
}

func rssHandler(apiConfig *shared.ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiToken := r.PathValue("token")
		user, err := apiConfig.Dbpool.FindUserForToken(apiToken)
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, "invalid token", http.StatusNotFound)
			return
		}

		href := fmt.Sprintf("https://auth.pico.sh/rss/%s", apiToken)

		feed := &feeds.Feed{
			Title:       "pico+",
			Link:        &feeds.Link{Href: href},
			Description: "get notified of important membership updates",
			Author:      &feeds.Author{Name: "team pico"},
		}
		var feedItems []*feeds.Item

		now := time.Now()
		ff, err := apiConfig.Dbpool.FindFeatureForUser(user.ID, "plus")
		if err != nil {
			// still want to send an empty feed
		} else {
			createdAt := ff.CreatedAt
			createdAtStr := createdAt.Format("2006-01-02 15:04:05")
			id := fmt.Sprintf("pico-plus-activated-%d", createdAt.Unix())
			content := `Thanks for joining pico+! You now have access to all our premium services for exactly one year.  We will send you pico+ expiration notifications through this RSS feed.  Go to <a href="https://pico.sh/getting-started#next-steps">pico.sh/getting-started#next-steps</a> to start using our services.`
			plus := &feeds.Item{
				Id:          id,
				Title:       fmt.Sprintf("pico+ membership activated on %s", createdAtStr),
				Link:        &feeds.Link{Href: "https://pico.sh"},
				Content:     content,
				Created:     *createdAt,
				Updated:     *createdAt,
				Description: content,
				Author:      &feeds.Author{Name: "team pico"},
			}
			feedItems = append(feedItems, plus)

			oneMonthWarning := ff.ExpiresAt.AddDate(0, -1, 0)
			mo := genFeedItem(now, *ff.ExpiresAt, oneMonthWarning, "1-month")
			if mo != nil {
				feedItems = append(feedItems, mo)
			}

			oneWeekWarning := ff.ExpiresAt.AddDate(0, 0, -7)
			wk := genFeedItem(now, *ff.ExpiresAt, oneWeekWarning, "1-week")
			if wk != nil {
				feedItems = append(feedItems, wk)
			}

			oneDayWarning := ff.ExpiresAt.AddDate(0, 0, -2)
			day := genFeedItem(now, *ff.ExpiresAt, oneDayWarning, "1-day")
			if day != nil {
				feedItems = append(feedItems, day)
			}
		}

		feed.Items = feedItems

		rss, err := feed.ToAtom()
		if err != nil {
			apiConfig.Cfg.Logger.Error(err.Error())
			http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
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

		log := logger.With(
			"username", username,
			"email", email,
			"created", created,
			"paymentStatus", status,
			"txId", txID,
		)
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

		err = dbpool.AddPicoPlusUser(username, "lemonsqueezy", txID)
		if err != nil {
			log.Error("failed to add pico+ user", "err", err)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("status not paid"))
			return
		}

		log.Info("successfully added pico+ user")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("successfully added pico+ user"))
	}
}

// URL shortener for out pico+ URL.
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
			visit := db.AnalyticsVisits{}
			err := json.Unmarshal([]byte(line), &visit)
			if err != nil {
				logger.Error("json unmarshal", "err", err)
				continue
			}

			user := slog.Any("userId", visit.UserID)

			err = shared.AnalyticsVisitFromVisit(&visit, dbpool, secret)
			if err != nil {
				if !errors.Is(err, shared.ErrAnalyticsDisabled) {
					logger.Info("could not record analytics visit", "reason", err, "visit", visit, user)
					continue
				}
			}

			logger.Info("inserting visit", "visit", visit, user)
			err = dbpool.InsertVisit(&visit)
			if err != nil {
				logger.Error("could not insert visit record", "err", err, "visit", visit, user)
			}
		}

		if scanner.Err() != nil {
			logger.Error("scanner error", "err", scanner.Err())
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
