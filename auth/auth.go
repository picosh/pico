package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
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

func getClient(r *http.Request) *Client {
	return r.Context().Value(ctxClient{}).(*Client)
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

	if !client.Dbpool.HasFeatureForUser(user.ID, space) {
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

func createMainRoutes() []shared.Route {
	fileServer := http.FileServer(http.Dir("auth/public"))

	routes := []shared.Route{
		shared.NewRoute("GET", "/.well-known/oauth-authorization-server", wellKnownHandler),
		shared.NewRoute("POST", "/introspect", introspectHandler),
		shared.NewRoute("GET", "/authorize", authorizeHandler),
		shared.NewRoute("POST", "/token", tokenHandler),
		shared.NewRoute("POST", "/key", keyHandler),
		shared.NewRoute("POST", "/redirect", redirectHandler),
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

func handler(routes []shared.Route, client *Client) shared.ServeFn {
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
				route.Handler(w, r.WithContext(clientCtx))
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

type AuthCfg struct {
	Debug  bool
	Port   string
	DbURL  string
	Domain string
	Issuer string
}

func StartApiServer() {
	debug := shared.GetEnv("AUTH_DEBUG", "0")
	cfg := &AuthCfg{
		DbURL:  shared.GetEnv("DATABASE_URL", ""),
		Debug:  debug == "1",
		Issuer: shared.GetEnv("AUTH_ISSUER", "pico.sh"),
		Domain: shared.GetEnv("AUTH_DOMAIN", "http://0.0.0.0:3000"),
		Port:   shared.GetEnv("AUTH_WEB_PORT", "3000"),
	}

	logger := shared.CreateLogger(true)
	db := postgres.NewDB(cfg.DbURL, logger)
	defer db.Close()

	client := &Client{
		Cfg:    cfg,
		Dbpool: db,
		Logger: logger,
	}

	routes := createMainRoutes()

	if cfg.Debug {
		routes = shared.CreatePProfRoutes(routes)
	}

	router := http.HandlerFunc(handler(routes, client))

	portStr := fmt.Sprintf(":%s", cfg.Port)
	client.Logger.Info("starting server on port", "port", cfg.Port)
	client.Logger.Error(http.ListenAndServe(portStr, router).Error())
}
