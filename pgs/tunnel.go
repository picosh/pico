package pgs

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/ui"
)

func allowPerm(proj *db.Project) bool {
	return true
}

type CtxHttpBridge = func(ssh.Context) http.Handler

func getInfoFromUser(user string) (string, string) {
	if strings.Contains(user, "__") {
		results := strings.SplitN(user, "__", 2)
		return results[0], results[1]
	}

	return "", user
}

func createHttpHandler(apiConfig *shared.ApiConfig) CtxHttpBridge {
	return func(ctx ssh.Context) http.Handler {
		dbh := apiConfig.Dbpool
		logger := apiConfig.Cfg.Logger
		asUser, subdomain := getInfoFromUser(ctx.User())
		log := logger.With(
			"subdomain", subdomain,
			"impersonating", asUser,
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
			log = log.With(
				"requester", requester.Name,
			)
		}

		// impersonation logic
		if asUser != "" {
			isAdmin := dbh.HasFeatureForUser(requester.ID, "admin")
			if !isAdmin {
				log.Error("impersonation attempt failed")
				return http.HandlerFunc(shared.UnauthorizedHandler)
			}
			requester, _ = dbh.FindUserForName(asUser)
		}

		shared.SetUserCtx(ctx, requester)

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
			rts := ui.CreateRoutes(apiConfig, ctx)
			routes = append(routes, rts...)
		}

		subdomainRoutes := createSubdomainRoutes(allowPerm)
		routes = append(routes, subdomainRoutes...)
		finctx := apiConfig.CreateCtx(ctx, subdomain)
		httpHandler := shared.CreateServeBasic(routes, finctx)
		httpRouter := http.HandlerFunc(httpHandler)
		return httpRouter
	}
}
