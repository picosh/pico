package auth

import (
	"bufio"
	"context"
	"crypto/hmac"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
	"github.com/picosh/utils/pipe"
)

type Client struct {
	Cfg    *AuthCfg
	Dbpool db.DB
	Logger *slog.Logger
}

func (client *Client) hasPrivilegedAccess(apiToken string) bool {
	user, err := client.Dbpool.FindUserForToken(apiToken)
	if err != nil {
		return false
	}
	return client.Dbpool.HasFeatureForUser(user.ID, "auth")
}

type ctxClient struct{}
type ctxKey struct{}

func getClient(r *http.Request) *Client {
	return r.Context().Value(ctxClient{}).(*Client)
}

func getField(r *http.Request, index int) string {
	fields := r.Context().Value(ctxKey{}).([]string)
	if index >= len(fields) {
		return ""
	}
	return fields[index]
}

func getApiToken(r *http.Request) string {
	authHeader := r.Header.Get("authorization")
	if authHeader == "" {
		return ""
	}
	return strings.TrimPrefix(authHeader, "Bearer ")
}

type oauth2Server struct {
	Issuer                                    string   `json:"issuer"`
	IntrospectionEndpoint                     string   `json:"introspection_endpoint"`
	IntrospectionEndpointAuthMethodsSupported []string `json:"introspection_endpoint_auth_methods_supported"`
	AuthorizationEndpoint                     string   `json:"authorization_endpoint"`
	TokenEndpoint                             string   `json:"token_endpoint"`
	ResponseTypesSupported                    []string `json:"response_types_supported"`
}

func generateURL(cfg *AuthCfg, path string) string {
	return fmt.Sprintf("%s/%s", cfg.Domain, path)
}

func wellKnownHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	p := oauth2Server{
		Issuer:                client.Cfg.Issuer,
		IntrospectionEndpoint: generateURL(client.Cfg, "introspect"),
		IntrospectionEndpointAuthMethodsSupported: []string{
			"none",
		},
		AuthorizationEndpoint:  generateURL(client.Cfg, "authorize"),
		TokenEndpoint:          generateURL(client.Cfg, "token"),
		ResponseTypesSupported: []string{"code"},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err := json.NewEncoder(w).Encode(p)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type oauth2Introspection struct {
	Active   bool   `json:"active"`
	Username string `json:"username"`
}

func introspectHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)
	token := r.FormValue("token")
	client.Logger.Info("introspect token", "token", token)

	user, err := client.Dbpool.FindUserForToken(token)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	p := oauth2Introspection{
		Active:   true,
		Username: user.Name,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(p)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func authorizeHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	responseType := r.URL.Query().Get("response_type")
	clientID := r.URL.Query().Get("client_id")
	redirectURI := r.URL.Query().Get("redirect_uri")
	scope := r.URL.Query().Get("scope")

	client.Logger.Info(
		"authorize handler",
		"responseType", responseType,
		"clientID", clientID,
		"redirectURI", redirectURI,
		"scope", scope,
	)

	ts, err := template.ParseFiles(
		"auth/html/redirect.page.tmpl",
		"auth/html/footer.partial.tmpl",
		"auth/html/marketing-footer.partial.tmpl",
		"auth/html/base.layout.tmpl",
	)

	if err != nil {
		client.Logger.Error(err.Error())
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
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}
}

func redirectHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	token := r.FormValue("token")
	redirectURI := r.FormValue("redirect_uri")
	responseType := r.FormValue("response_type")

	client.Logger.Info("redirect handler",
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

type oauth2Token struct {
	AccessToken string `json:"access_token"`
}

func tokenHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	token := r.FormValue("code")
	redirectURI := r.FormValue("redirect_uri")
	grantType := r.FormValue("grant_type")

	client.Logger.Info(
		"handle token",
		"token", token,
		"redirectURI", redirectURI,
		"grantType", grantType,
	)

	_, err := client.Dbpool.FindUserForToken(token)
	if err != nil {
		client.Logger.Error(err.Error())
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
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

type sishData struct {
	PublicKey     string `json:"auth_key"`
	Username      string `json:"user"`
	RemoteAddress string `json:"remote_addr"`
}

func keyHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	var data sishData

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	space := r.URL.Query().Get("space")
	if space == "" {
		spaceErr := fmt.Errorf("Must provide `space` query parameter")
		client.Logger.Error(spaceErr.Error())
		http.Error(w, spaceErr.Error(), http.StatusUnprocessableEntity)
	}

	client.Logger.Info(
		"handle key",
		"remoteAddress", data.RemoteAddress,
		"user", data.Username,
		"space", space,
		"publicKey", data.PublicKey,
	)

	user, err := client.Dbpool.FindUserForKey(data.Username, data.PublicKey)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	if space == "tuns" {
		if !client.Dbpool.HasFeatureForUser(user.ID, "plus") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	} else if !client.Dbpool.HasFeatureForUser(user.ID, space) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	if !client.hasPrivilegedAccess(getApiToken(r)) {
		w.WriteHeader(http.StatusOK)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(user)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func userHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)

	if !client.hasPrivilegedAccess(getApiToken(r)) {
		w.WriteHeader(http.StatusForbidden)
		return
	}

	var data sishData

	err := json.NewDecoder(r.Body).Decode(&data)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	client.Logger.Info(
		"handle key",
		"remoteAddress", data.RemoteAddress,
		"user", data.Username,
		"publicKey", data.PublicKey,
	)

	user, err := client.Dbpool.FindUserForName(data.Username)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	keys, err := client.Dbpool.FindKeysForUser(user)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	err = json.NewEncoder(w).Encode(keys)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusInternalServerError)
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

func rssHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)
	apiToken, err := url.PathUnescape(getField(r, 0))
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	user, err := client.Dbpool.FindUserForToken(apiToken)
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, "invalid token", http.StatusNotFound)
		return
	}

	href := fmt.Sprintf("https://auth.pico.sh/rss/%s", apiToken)

	feed := &feeds.Feed{
		Title:       "pico+",
		Link:        &feeds.Link{Href: href},
		Description: "get notified of important membership updates",
		Author:      &feeds.Author{Name: "team pico"},
		Created:     time.Now(),
	}
	var feedItems []*feeds.Item

	now := time.Now()
	ff, err := client.Dbpool.FindFeatureForUser(user.ID, "plus")
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

		oneDayWarning := ff.ExpiresAt.AddDate(0, 0, -1)
		day := genFeedItem(now, *ff.ExpiresAt, oneDayWarning, "1-day")
		if day != nil {
			feedItems = append(feedItems, day)
		}
	}

	feed.Items = feedItems

	rss, err := feed.ToAtom()
	if err != nil {
		client.Logger.Error(err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		client.Logger.Error(err.Error())
	}
}

type OrderEvent struct {
	Meta *struct {
		EventName  string `json:"event_name"`
		CustomData *struct {
			PicoUsername string `json:"username"`
		} `json:"custom_data"`
	} `json:"meta"`
	Data *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Attr *struct {
			OrderNumber int       `json:"order_number"`
			Identifier  string    `json:"identifier"`
			UserName    string    `json:"user_name"`
			UserEmail   string    `json:"user_email"`
			CreatedAt   time.Time `json:"created_at"`
			Status      string    `json:"status"` // `paid`, `refund`
		} `json:"attributes"`
	} `json:"data"`
}

// Status code must be 200 or else lemonsqueezy will keep retrying
// https://docs.lemonsqueezy.com/help/webhooks
func paymentWebhookHandler(secret string) func(http.ResponseWriter, *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		client := getClient(r)
		dbpool := client.Dbpool
		logger := client.Logger
		const MaxBodyBytes = int64(65536)
		r.Body = http.MaxBytesReader(w, r.Body, MaxBodyBytes)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			logger.Error("error reading request body", "err", err.Error())
			w.WriteHeader(http.StatusOK)
			return
		}

		event := OrderEvent{}

		if err := json.Unmarshal(payload, &event); err != nil {
			logger.Error("failed to parse webhook body JSON", "err", err.Error())
			w.WriteHeader(http.StatusOK)
			return
		}

		hash := shared.HmacString(secret, string(payload))
		sig := r.Header.Get("X-Signature")
		if !hmac.Equal([]byte(hash), []byte(sig)) {
			logger.Error("invalid signature X-Signature")
			w.WriteHeader(http.StatusOK)
			return
		}

		if event.Meta == nil {
			logger.Error("no meta field found")
			w.WriteHeader(http.StatusOK)
			return
		}

		if event.Meta.EventName != "order_created" {
			logger.Error("event not order_created", "event", event.Meta.EventName)
			w.WriteHeader(http.StatusOK)
			return
		}

		if event.Meta.CustomData == nil {
			logger.Error("no custom data found")
			w.WriteHeader(http.StatusOK)
			return
		}

		username := event.Meta.CustomData.PicoUsername

		if event.Data == nil || event.Data.Attr == nil {
			logger.Error("no data or data.attributes fields found")
			w.WriteHeader(http.StatusOK)
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
			return
		}

		if status != "paid" {
			log.Error("status not paid")
			w.WriteHeader(http.StatusOK)
			return
		}

		err = dbpool.AddPicoPlusUser(username, "lemonsqueezy", txID)
		if err != nil {
			log.Error("failed to add pico+ user", "err", err)
		} else {
			log.Info("successfully added pico+ user")
		}

		w.WriteHeader(http.StatusOK)
	}
}

// URL shortener for out pico+ URL.
func checkoutHandler(w http.ResponseWriter, r *http.Request) {
	username, err := url.PathUnescape(getField(r, 0))
	if err != nil {
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	link := "https://checkout.pico.sh/buy/73c26cf9-3fac-44c3-b744-298b3032a96b"
	url := fmt.Sprintf(
		"%s?discount=0&checkout[custom][username]=%s",
		link,
		username,
	)
	http.Redirect(w, r, url, http.StatusMovedPermanently)
}

func createMainRoutes() []shared.Route {
	fileServer := http.FileServer(http.Dir("auth/public"))
	secret := os.Getenv("PICO_SECRET_WEBHOOK")
	if secret == "" {
		panic("must provide PICO_SECRET_WEBHOOK environment variable")
	}

	routes := []shared.Route{
		shared.NewRoute("GET", "/checkout/(.+)", checkoutHandler),
		shared.NewRoute("GET", "/.well-known/oauth-authorization-server", wellKnownHandler),
		shared.NewRoute("POST", "/introspect", introspectHandler),
		shared.NewRoute("GET", "/authorize", authorizeHandler),
		shared.NewRoute("POST", "/token", tokenHandler),
		shared.NewRoute("POST", "/key", keyHandler),
		shared.NewRoute("POST", "/user", userHandler),
		shared.NewRoute("GET", "/rss/(.+)", rssHandler),
		shared.NewRoute("POST", "/redirect", redirectHandler),
		shared.NewRoute("POST", "/webhook", paymentWebhookHandler(secret)),
		shared.NewRoute("GET", "/main.css", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/card.png", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/favicon-16x16.png", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/favicon-32x32.png", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/apple-touch-icon.png", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/favicon.ico", fileServer.ServeHTTP),
		shared.NewRoute("GET", "/robots.txt", fileServer.ServeHTTP),
	}

	return routes
}

func handler(routes []shared.Route, client *Client) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allow []string

		for _, route := range routes {
			matches := route.Regex.FindStringSubmatch(r.URL.Path)
			if len(matches) > 0 {
				if r.Method != route.Method {
					allow = append(allow, route.Method)
					continue
				}
				clientCtx := context.WithValue(r.Context(), ctxClient{}, client)
				ctx := context.WithValue(clientCtx, ctxKey{}, matches[1:])
				route.Handler(w, r.WithContext(ctx))
				return
			}
		}
		if len(allow) > 0 {
			w.Header().Set("Allow", strings.Join(allow, ", "))
			http.Error(w, "405 method not allowed", http.StatusMethodNotAllowed)
			return
		}
		http.NotFound(w, r)
	}
}

func metricDrainSub(ctx context.Context, dbpool db.DB, logger *slog.Logger, secret string) {
	conn := shared.NewPicoPipeClient()
	stdoutPipe, err := pipe.Sub("sub metric-drain -k", ctx, conn)

	if err != nil {
		logger.Error("could not sub to metric-drain", "err", err)
		return
	}

	scanner := bufio.NewScanner(stdoutPipe)
	for scanner.Scan() {
		line := scanner.Text()
		visit := db.AnalyticsVisits{}
		err := json.Unmarshal([]byte(line), &visit)
		if err != nil {
			logger.Error("json unmarshal", "err", err)
			continue
		}

		err = shared.AnalyticsVisitFromVisit(&visit, dbpool, secret)
		if err != nil {
			if !errors.Is(err, shared.ErrAnalyticsDisabled) {
				logger.Info("could not record analytics visit", "reason", err)
			}
		}

		logger.Info("inserting visit", "visit", visit)
		err = dbpool.InsertVisit(&visit)
		if err != nil {
			logger.Error("could not insert visit record", "err", err)
		}
	}
}

type AuthCfg struct {
	Debug  bool
	Port   string
	DbURL  string
	Domain string
	Issuer string
	Secret string
}

func StartApiServer() {
	debug := utils.GetEnv("AUTH_DEBUG", "0")
	cfg := &AuthCfg{
		DbURL:  utils.GetEnv("DATABASE_URL", ""),
		Debug:  debug == "1",
		Issuer: utils.GetEnv("AUTH_ISSUER", "pico.sh"),
		Domain: utils.GetEnv("AUTH_DOMAIN", "http://0.0.0.0:3000"),
		Port:   utils.GetEnv("AUTH_WEB_PORT", "3000"),
		Secret: utils.GetEnv("PICO_SECRET", ""),
	}
	if cfg.Secret == "" {
		panic("must provide PICO_SECRET environment variable")
	}

	logger := shared.CreateLogger("auth")
	db := postgres.NewDB(cfg.DbURL, logger)
	defer db.Close()

	client := &Client{
		Cfg:    cfg,
		Dbpool: db,
		Logger: logger,
	}

	ctx := context.Background()
	// gather metrics in the auth service
	go metricDrainSub(ctx, db, logger, cfg.Secret)
	defer ctx.Done()

	routes := createMainRoutes()

	if cfg.Debug {
		routes = shared.CreatePProfRoutes(routes)
	}

	router := http.HandlerFunc(handler(routes, client))

	portStr := fmt.Sprintf(":%s", cfg.Port)
	client.Logger.Info("starting server on port", "port", cfg.Port)
	err := http.ListenAndServe(portStr, router)
	if err != nil {
		client.Logger.Info("http-serve", "err", err.Error())
	}
}
