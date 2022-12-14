package shared

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/pprof"
	"regexp"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/imgs/storage"
	"go.uber.org/zap"
)

type Route struct {
	method  string
	regex   *regexp.Regexp
	handler http.HandlerFunc
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

type ServeFn func(http.ResponseWriter, *http.Request)

func CreateServe(routes []Route, subdomainRoutes []Route, cfg *ConfigSite, dbpool db.DB, st storage.ObjectStorage, logger *zap.SugaredLogger) ServeFn {
	return func(w http.ResponseWriter, r *http.Request) {
		var allow []string
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

		for _, route := range curRoutes {
			matches := route.regex.FindStringSubmatch(r.URL.Path)
			if len(matches) > 0 {
				if r.Method != route.method {
					allow = append(allow, route.method)
					continue
				}
				loggerCtx := context.WithValue(r.Context(), ctxLoggerKey{}, logger)
				subdomainCtx := context.WithValue(loggerCtx, ctxSubdomainKey{}, subdomain)
				dbCtx := context.WithValue(subdomainCtx, ctxDBKey{}, dbpool)
				storageCtx := context.WithValue(dbCtx, ctxStorageKey{}, st)
				cfgCtx := context.WithValue(storageCtx, ctxCfg{}, cfg)
				ctx := context.WithValue(cfgCtx, ctxKey{}, matches[1:])
				route.handler(w, r.WithContext(ctx))
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

type ctxDBKey struct{}
type ctxStorageKey struct{}
type ctxKey struct{}
type ctxLoggerKey struct{}
type ctxSubdomainKey struct{}
type ctxCfg struct{}

func GetCfg(r *http.Request) *ConfigSite {
	return r.Context().Value(ctxCfg{}).(*ConfigSite)
}

func GetLogger(r *http.Request) *zap.SugaredLogger {
	return r.Context().Value(ctxLoggerKey{}).(*zap.SugaredLogger)
}

func GetDB(r *http.Request) db.DB {
	return r.Context().Value(ctxDBKey{}).(db.DB)
}

func GetStorage(r *http.Request) storage.ObjectStorage {
	return r.Context().Value(ctxStorageKey{}).(storage.ObjectStorage)
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
	records, err := net.LookupTXT(fmt.Sprintf("_%s.%s", space, host))
	if err != nil {
		return ""
	}

	for _, v := range records {
		return strings.TrimSpace(v)
	}

	return ""
}
