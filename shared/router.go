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
	Method  string
	Regex   *regexp.Regexp
	Handler http.HandlerFunc
}

func NewRoute(method, pattern string, handler http.HandlerFunc) Route {
	return Route{
		method,
		regexp.MustCompile("^" + pattern + "$"),
		handler,
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

func WwwRedirect(serve http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Host, "www.") {
			serve(w, r)
			return
		}

		logger := GetLogger(r)
		url := strings.Replace(r.Host, "www.", "", 1)
		logger.Info(
			"redirecting",
			"host", r.Host,
			"url", url,
		)
		http.Redirect(w, r, url, http.StatusMovedPermanently)
	}
}

type HttpCtx struct {
	Cfg     *ConfigSite
	Dbpool  db.DB
	Storage storage.StorageServe
}

func (hc *HttpCtx) CreateCtx(prevCtx context.Context, subdomain string) context.Context {
	loggerCtx := context.WithValue(prevCtx, ctxLoggerKey{}, hc.Cfg.Logger)
	subdomainCtx := context.WithValue(loggerCtx, ctxSubdomainKey{}, subdomain)
	dbCtx := context.WithValue(subdomainCtx, ctxDBKey{}, hc.Dbpool)
	storageCtx := context.WithValue(dbCtx, ctxStorageKey{}, hc.Storage)
	cfgCtx := context.WithValue(storageCtx, ctxCfg{}, hc.Cfg)
	return cfgCtx
}

func CreateServeBasic(routes []Route, ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var allow []string
		for _, route := range routes {
			matches := route.Regex.FindStringSubmatch(r.URL.Path)
			if len(matches) > 0 {
				if r.Method != route.Method {
					allow = append(allow, route.Method)
					continue
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

func CreateServe(routes []Route, subdomainRoutes []Route, httpCtx *HttpCtx) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		curRoutes, subdomain := findRouteConfig(r, routes, subdomainRoutes, httpCtx.Cfg)
		ctx := httpCtx.CreateCtx(r.Context(), subdomain)
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
	finhost := host
	if strings.HasPrefix(host, "www.") {
		finhost = strings.Replace(host, "www.", "", 1)
	}

	txt := fmt.Sprintf("_%s.%s", space, finhost)
	records, err := net.LookupTXT(txt)
	if err != nil {
		return ""
	}

	for _, v := range records {
		return strings.TrimSpace(v)
	}

	return ""
}
