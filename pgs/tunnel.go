package pgs

import (
	"encoding/json"
	"net/http"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/plus"
	"github.com/picosh/pico/shared"
)

func allowPerm(proj *db.Project) bool {
	return true
}

type CtxHttpBridge = func(ssh.Context) http.Handler

func createHttpHandler(httpCtx *shared.HttpCtx) CtxHttpBridge {
	return func(ctx ssh.Context) http.Handler {
		subdomain := ctx.User()
		dbh := httpCtx.Dbpool
		logger := httpCtx.Cfg.Logger
		log := logger.With(
			"subdomain", subdomain,
		)

		pubkey, err := shared.GetPublicKeyCtx(ctx)
		if err != nil {
			log.Error(err.Error(), "subdomain", subdomain)
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}
		pubkeyStr, err := shared.KeyForKeyText(pubkey)
		if err != nil {
			log.Error(err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}
		log = log.With(
			"pubkey", pubkeyStr,
		)

		props, err := getProjectFromSubdomain(subdomain)
		if err != nil {
			log.Error(err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		owner, err := dbh.FindUserForName(props.Username)
		if err != nil {
			log.Error(err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}
		log = log.With(
			"owner", owner.Name,
		)

		project, err := dbh.FindProjectByName(owner.ID, props.ProjectName)
		if err != nil {
			log.Error(err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		requester, _ := dbh.FindUserForKey("", pubkeyStr)
		if requester != nil {
			log = logger.With(
				"requester", requester.Name,
			)
		}

		if !HasProjectAccess(project, owner, requester, pubkey) {
			log.Error("no access")
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		log.Info("user has access to site")

		routes := []shared.Route{
			// special API endpoint for tunnel users accessing site
			shared.NewCorsRoute("GET", "/api/current_user", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				pico := shared.NewUserApi(requester, pubkey)
				err := json.NewEncoder(w).Encode(pico)
				if err != nil {
					log.Error(err.Error())
				}
			}),
		}

		if subdomain == "pico-ui" || subdomain == "erock-ui" {
			rts := plus.CreateRoutes(httpCtx, ctx)
			routes = append(routes, rts...)
		}

		subdomainRoutes := createSubdomainRoutes(allowPerm)
		routes = append(routes, subdomainRoutes...)
		finctx := httpCtx.CreateCtx(ctx, subdomain)
		httpHandler := shared.CreateServeBasic(routes, finctx)
		httpRouter := http.HandlerFunc(httpHandler)
		return httpRouter
	}
}
