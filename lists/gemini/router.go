package gemini

import (
	"context"
	"regexp"

	"git.sr.ht/~adnano/go-gemini"
	"git.sr.ht/~erock/lists.sh/internal"
	"git.sr.ht/~erock/wish/cms/db"
	"go.uber.org/zap"
)

type ctxKey struct{}
type ctxDBKey struct{}
type ctxLoggerKey struct{}
type ctxCfgKey struct{}

func GetLogger(ctx context.Context) *zap.SugaredLogger {
	return ctx.Value(ctxLoggerKey{}).(*zap.SugaredLogger)
}

func GetCfg(ctx context.Context) *internal.ConfigSite {
	return ctx.Value(ctxCfgKey{}).(*internal.ConfigSite)
}

func GetDB(ctx context.Context) db.DB {
	return ctx.Value(ctxDBKey{}).(db.DB)
}

func GetField(ctx context.Context, index int) string {
	fields := ctx.Value(ctxKey{}).([]string)
	return fields[index]
}

type Route struct {
	regex   *regexp.Regexp
	handler gemini.HandlerFunc
}

func NewRoute(pattern string, handler gemini.HandlerFunc) Route {
	return Route{
		regexp.MustCompile("^" + pattern + "$"),
		handler,
	}
}

type ServeFn func(context.Context, gemini.ResponseWriter, *gemini.Request)

func CreateServe(routes []Route, cfg *internal.ConfigSite, dbpool db.DB, logger *zap.SugaredLogger) ServeFn {
	return func(ctx context.Context, w gemini.ResponseWriter, r *gemini.Request) {
		curRoutes := routes

		for _, route := range curRoutes {
			matches := route.regex.FindStringSubmatch(r.URL.Path)
			if len(matches) > 0 {
				ctx = context.WithValue(ctx, ctxLoggerKey{}, logger)
				ctx = context.WithValue(ctx, ctxDBKey{}, dbpool)
				ctx = context.WithValue(ctx, ctxCfgKey{}, cfg)
				ctx = context.WithValue(ctx, ctxKey{}, matches[1:])
				route.handler(ctx, w, r)
				return
			}
		}
		w.WriteHeader(gemini.StatusTemporaryFailure, "Internal Service Error")
	}
}
