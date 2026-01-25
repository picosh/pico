package router

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"regexp"
	"strings"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/storage"
)

type Route struct {
	Method      string
	Regex       *regexp.Regexp
	Handler     http.HandlerFunc
	CorsEnabled bool
}

func NewRoute(method, pattern string, handler http.HandlerFunc) Route {
	return Route{
		method,
		regexp.MustCompile("^" + pattern + "$"),
		handler,
		false,
	}
}

func NewCorsRoute(method, pattern string, handler http.HandlerFunc) Route {
	return Route{
		method,
		regexp.MustCompile("^" + pattern + "$"),
		handler,
		true,
	}
}

func CreatePProfRoutes(routes []Route) []Route {
	return append(routes,
		NewRoute("GET", "/debug/pprof/cmdline", pprof.Cmdline),
		NewRoute("GET", "/debug/pprof/profile", pprof.Profile),
		NewRoute("GET", "/debug/pprof/symbol", pprof.Symbol),
		NewRoute("GET", "/debug/pprof/trace", pprof.Trace),
		NewRoute("GET", "/debug/pprof/(.*)", pprof.Index),
		NewRoute("POST", "/debug/pprof/cmdline", pprof.Cmdline),
		NewRoute("POST", "/debug/pprof/profile", pprof.Profile),
		NewRoute("POST", "/debug/pprof/symbol", pprof.Symbol),
		NewRoute("POST", "/debug/pprof/trace", pprof.Trace),
		NewRoute("POST", "/debug/pprof/(.*)", pprof.Index),
	)
}

func CreatePProfRoutesMux(mux *http.ServeMux) {
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("GET /debug/pprof/(.*)", pprof.Index)
	mux.HandleFunc("POST /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("POST /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("POST /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("POST /debug/pprof/trace", pprof.Trace)
	mux.HandleFunc("POST /debug/pprof/(.*)", pprof.Index)
}

type ApiConfig struct {
	Cfg     *shared.ConfigSite
	Dbpool  db.DB
	Storage storage.StorageServe
}

func (hc *ApiConfig) HasPrivilegedAccess(apiToken string) bool {
	user, err := hc.Dbpool.FindUserByToken(apiToken)
	if err != nil {
		return false
	}
	return hc.Dbpool.HasFeatureByUser(user.ID, "auth")
}

func (hc *ApiConfig) HasPlusOrSpace(user *db.User, space string) bool {
	return hc.Dbpool.HasFeatureByUser(user.ID, "plus") || hc.Dbpool.HasFeatureByUser(user.ID, space)
}

func (hc *ApiConfig) CreateCtx(prevCtx context.Context, subdomain string) context.Context {
	ctx := context.WithValue(prevCtx, ctxLoggerKey{}, hc.Cfg.Logger)
	ctx = context.WithValue(ctx, CtxSubdomainKey{}, subdomain)
	ctx = context.WithValue(ctx, ctxDBKey{}, hc.Dbpool)
	ctx = context.WithValue(ctx, ctxStorageKey{}, hc.Storage)
	ctx = context.WithValue(ctx, ctxCfg{}, hc.Cfg)
	return ctx
}

func CreateServeBasic(routes []Route, ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allow []string
		for _, route := range routes {
			matches := route.Regex.FindStringSubmatch(r.URL.Path)
			if len(matches) > 0 {
				if r.Method == "OPTIONS" && route.CorsEnabled {
					CorsHeaders(w.Header())
					w.WriteHeader(http.StatusOK)
					return
				} else if r.Method != route.Method {
					allow = append(allow, route.Method)
					continue
				}

				if route.CorsEnabled {
					CorsHeaders(w.Header())
				}

				finctx := context.WithValue(ctx, ctxKey{}, matches[1:])
				route.Handler(w, r.WithContext(finctx))
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

func GetSubdomainFromRequest(r *http.Request, domain, space string) string {
	hostDomain := strings.ToLower(strings.Split(r.Host, ":")[0])
	appDomain := strings.ToLower(strings.Split(domain, ":")[0])

	if hostDomain != appDomain {
		if strings.Contains(hostDomain, appDomain) {
			subdomain := strings.TrimSuffix(hostDomain, fmt.Sprintf(".%s", appDomain))
			return subdomain
		} else {
			subdomain := GetCustomDomain(hostDomain, space)
			return subdomain
		}
	}

	return ""
}

func findRouteConfig(r *http.Request, routes []Route, subdomainRoutes []Route, cfg *shared.ConfigSite) ([]Route, string) {
	if len(subdomainRoutes) == 0 {
		return routes, ""
	}

	subdomain := GetSubdomainFromRequest(r, cfg.Domain, cfg.Space)
	if subdomain == "" {
		return routes, subdomain
	}
	return subdomainRoutes, subdomain
}

func CreateServe(routes []Route, subdomainRoutes []Route, apiConfig *ApiConfig) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		curRoutes, subdomain := findRouteConfig(r, routes, subdomainRoutes, apiConfig.Cfg)
		ctx := apiConfig.CreateCtx(r.Context(), subdomain)
		router := CreateServeBasic(curRoutes, ctx)
		router(w, r)
	}
}

type ctxDBKey struct{}
type ctxStorageKey struct{}
type ctxLoggerKey struct{}
type ctxCfg struct{}

type CtxSubdomainKey struct{}
type ctxKey struct{}
type CtxSessionKey struct{}

func GetSshCtx(r *http.Request) (*pssh.SSHServerConnSession, error) {
	payload, ok := r.Context().Value(CtxSessionKey{}).(*pssh.SSHServerConnSession)
	if payload == nil || !ok {
		return payload, fmt.Errorf("ssh session not set on `r.Context()` for connection")
	}
	return payload, nil
}

func GetCfg(r *http.Request) *shared.ConfigSite {
	return r.Context().Value(ctxCfg{}).(*shared.ConfigSite)
}

func GetLogger(r *http.Request) *slog.Logger {
	return r.Context().Value(ctxLoggerKey{}).(*slog.Logger)
}

func GetDB(r *http.Request) db.DB {
	return r.Context().Value(ctxDBKey{}).(db.DB)
}

func GetStorage(r *http.Request) storage.StorageServe {
	return r.Context().Value(ctxStorageKey{}).(storage.StorageServe)
}

func GetField(r *http.Request, index int) string {
	fields := r.Context().Value(ctxKey{}).([]string)
	if index >= len(fields) {
		return ""
	}
	return fields[index]
}

func GetSubdomain(r *http.Request) string {
	return r.Context().Value(CtxSubdomainKey{}).(string)
}

var txtCache = expirable.NewLRU[string, string](2048, nil, shared.CacheTimeout)

func GetCustomDomain(host string, space string) string {
	txt := fmt.Sprintf("_%s.%s", space, host)
	record, found := txtCache.Get(txt)
	if found {
		return record
	}

	records, err := net.LookupTXT(txt)
	if err != nil {
		return ""
	}

	for _, v := range records {
		rec := strings.TrimSpace(v)
		txtCache.Add(txt, rec)
		return rec
	}

	return ""
}

func GetApiToken(r *http.Request) string {
	authHeader := r.Header.Get("authorization")
	if authHeader == "" {
		return ""
	}
	return strings.TrimPrefix(authHeader, "Bearer ")
}
