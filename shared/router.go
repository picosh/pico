package shared

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/pprof"
	"regexp"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared/storage"
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

type ServeFn func(http.ResponseWriter, *http.Request)
type ApiConfig struct {
	Cfg            *ConfigSite
	Dbpool         db.DB
	Storage        storage.StorageServe
	AnalyticsQueue chan *db.AnalyticsVisits
}

func (hc *ApiConfig) CreateCtx(prevCtx context.Context, subdomain string) context.Context {
	ctx := context.WithValue(prevCtx, ctxLoggerKey{}, hc.Cfg.Logger)
	ctx = context.WithValue(ctx, ctxSubdomainKey{}, subdomain)
	ctx = context.WithValue(ctx, ctxDBKey{}, hc.Dbpool)
	ctx = context.WithValue(ctx, ctxStorageKey{}, hc.Storage)
	ctx = context.WithValue(ctx, ctxCfg{}, hc.Cfg)
	ctx = context.WithValue(ctx, ctxAnalyticsQueue{}, hc.AnalyticsQueue)
	return ctx
}

func CreateServeBasic(routes []Route, ctx context.Context) ServeFn {
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

func findRouteConfig(r *http.Request, routes []Route, subdomainRoutes []Route, cfg *ConfigSite) ([]Route, string) {
	var subdomain string
	curRoutes := routes

	if cfg.IsCustomdomains() || cfg.IsSubdomains() {
		hostDomain := strings.ToLower(strings.Split(r.Host, ":")[0])
		appDomain := strings.ToLower(strings.Split(cfg.ConfigCms.Domain, ":")[0])

		if hostDomain != appDomain {
			if strings.Contains(hostDomain, appDomain) {
				subdomain = strings.TrimSuffix(hostDomain, fmt.Sprintf(".%s", appDomain))
				if subdomain != "" {
					curRoutes = subdomainRoutes
				}
			} else {
				subdomain = GetCustomDomain(hostDomain, cfg.Space)
				if subdomain != "" {
					curRoutes = subdomainRoutes
				}
			}
		}
	}

	return curRoutes, subdomain
}

func CreateServe(routes []Route, subdomainRoutes []Route, apiConfig *ApiConfig) ServeFn {
	return func(w http.ResponseWriter, r *http.Request) {
		curRoutes, subdomain := findRouteConfig(r, routes, subdomainRoutes, apiConfig.Cfg)
		ctx := apiConfig.CreateCtx(r.Context(), subdomain)
		router := CreateServeBasic(curRoutes, ctx)
		router(w, r)
	}
}

type ctxDBKey struct{}
type ctxStorageKey struct{}
type ctxKey struct{}
type ctxLoggerKey struct{}
type ctxSubdomainKey struct{}
type ctxCfg struct{}
type ctxAnalyticsQueue struct{}

func GetCfg(r *http.Request) *ConfigSite {
	return r.Context().Value(ctxCfg{}).(*ConfigSite)
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
	return r.Context().Value(ctxSubdomainKey{}).(string)
}

func GetCustomDomain(host string, space string) string {
	txt := fmt.Sprintf("_%s.%s", space, host)
	records, err := net.LookupTXT(txt)
	if err != nil {
		return ""
	}

	for _, v := range records {
		return strings.TrimSpace(v)
	}

	return ""
}

func GetAnalyticsQueue(r *http.Request) chan *db.AnalyticsVisits {
	return r.Context().Value(ctxAnalyticsQueue{}).(chan *db.AnalyticsVisits)
}
