package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"go.uber.org/zap"
)

type Client struct {
	Cfg    *AuthCfg
	Dbpool db.DB
	Logger *zap.SugaredLogger
}

type ctxClient struct{}

func getClient(r *http.Request) *Client {
	return r.Context().Value(ctxClient{}).(*Client)
}

type oauth2Server struct {
	Issuer                                    string   `json:"issuer"`
	IntrospectionEndpoint                     string   `json:"introspection_endpoint"`
	IntrospectionEndpointAuthMethodsSupported []string `json:"introspection_endpoint_auth_methods_supported"`
}

func getIntrospectURL(cfg *AuthCfg) string {
	return fmt.Sprintf("%s/introspect", cfg.Domain)
}

func wellKnownHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)
	introspectURL := getIntrospectURL(client.Cfg)

	p := oauth2Server{
		Issuer:                client.Cfg.Issuer,
		IntrospectionEndpoint: introspectURL,
		IntrospectionEndpointAuthMethodsSupported: []string{
			"none",
		},
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

type oauth2Introspection struct {
	Active   bool   `json:"active"`
	Username string `json:"username"`
}

type AuthBody struct {
	token string
}

func introspectHandler(w http.ResponseWriter, r *http.Request) {
	client := getClient(r)
	token := r.FormValue("token")
	client.Logger.Infof("introspect token (%s)", token)

	user, err := client.Dbpool.FindUserForToken(token)
	if err != nil {
		client.Logger.Error(err)
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return
	}

	p := oauth2Introspection{
		Active:   true,
		Username: user.Name,
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(p)
}

func createMainRoutes() []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/.well-known/oauth-authorization-server", wellKnownHandler),
		shared.NewRoute("POST", "/introspect", introspectHandler),
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

	logger := shared.CreateLogger()
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
	client.Logger.Infof("Starting server on port %s", cfg.Port)
	client.Logger.Fatal(http.ListenAndServe(portStr, router))
}
