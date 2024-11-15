package pgs

import (
	"net/http"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils"
)

type TunnelWebRouter struct {
	*WebRouter
}

func (web *TunnelWebRouter) Perm(proj *db.Project) bool {
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

		pubkey, err := shared.GetPublicKey(ctx)
		if err != nil {
			log.Error(err.Error(), "subdomain", subdomain)
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		pubkeyStr := utils.KeyForKeyText(pubkey)

		log = log.With(
			"pubkey", pubkeyStr,
		)

		props, err := shared.GetProjectFromSubdomain(subdomain)
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

		shared.SetUser(ctx, requester)

		if !HasProjectAccess(project, owner, requester, pubkey) {
			log.Error("no access")
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		log.Info("user has access to site")

		/* routes := []shared.Route{
			// special API endpoint for tunnel users accessing site
			shared.NewCorsRoute("GET", "/api/current_user", func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				user, err := shared.GetUser(ctx)
				if err != nil {
					logger.Error("could not find user", "err", err.Error())
					shared.JSONError(w, err.Error(), http.StatusNotFound)
					return
				}
				pico := shared.NewUserApi(user, pubkey)
				err = json.NewEncoder(w).Encode(pico)
				if err != nil {
					log.Error(err.Error())
				}
			}),
		} */

		routes := NewWebRouter(
			apiConfig.Cfg,
			logger,
			apiConfig.Dbpool,
			apiConfig.Storage,
		)
		tunnelRouter := TunnelWebRouter{routes}
		router := http.NewServeMux()
		router.HandleFunc("GET /{fname}/{options}...", tunnelRouter.ImageRequest)
		router.HandleFunc("GET /{fname}", tunnelRouter.AssetRequest)
		router.HandleFunc("GET /{$}", tunnelRouter.AssetRequest)

		/* subdomainRoutes := createSubdomainRoutes(allowPerm)
		routes = append(routes, subdomainRoutes...)
		finctx := apiConfig.CreateCtx(context.Background(), subdomain)
		finctx = context.WithValue(finctx, shared.CtxSshKey{}, ctx)
		httpHandler := shared.CreateServeBasic(routes, finctx)
		httpRouter := http.HandlerFunc(httpHandler) */
		return router
	}
}
