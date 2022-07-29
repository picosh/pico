package internal

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"git.sr.ht/~erock/wish/cms/db"
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

type ServeFn func(http.ResponseWriter, *http.Request)

func CreateServe(routes []Route, subdomainRoutes []Route, cfg *ConfigSite, dbpool db.DB, logger *zap.SugaredLogger) ServeFn {
	return func(w http.ResponseWriter, r *http.Request) {
		var allow []string
		curRoutes := routes

		hostDomain := strings.ToLower(strings.Split(r.Host, ":")[0])
		appDomain := strings.ToLower(strings.Split(cfg.ConfigCms.Domain, ":")[0])

		subdomain := ""
		if hostDomain != appDomain && strings.Contains(hostDomain, appDomain) {
			subdomain = strings.TrimSuffix(hostDomain, fmt.Sprintf(".%s", appDomain))
		}

		if cfg.IsSubdomains() && subdomain != "" {
			curRoutes = subdomainRoutes
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
				cfgCtx := context.WithValue(dbCtx, ctxCfg{}, cfg)
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

func GetField(r *http.Request, index int) string {
	fields := r.Context().Value(ctxKey{}).([]string)
	return fields[index]
}

func GetSubdomain(r *http.Request) string {
	return r.Context().Value(ctxSubdomainKey{}).(string)
}
