package pgs

import (
	"net/http"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
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

		pubkey := ctx.Permissions().Extensions["pubkey"]
		if pubkey == "" {
			log.Error("pubkey not found in extensions", "subdomain", subdomain)
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		log = log.With(
			"pubkey", pubkey,
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

		requester, _ := dbh.FindUserForKey("", pubkey)
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

		ctx.Permissions().Extensions["user_id"] = requester.ID
		publicKey, err := ssh.ParsePublicKey([]byte(pubkey))
		if err != nil {
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}
		if !HasProjectAccess(project, owner, requester, publicKey) {
			log.Error("no access")
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		log.Info("user has access to site")

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
		return router
	}
}
