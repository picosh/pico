package pgs

import (
	"context"
	"net/http"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

type TunnelWebRouter struct {
	*WebRouter
	subdomain string
}

func (web *TunnelWebRouter) InitRouter() {
	router := http.NewServeMux()
	router.HandleFunc("GET /{fname...}", web.AssetRequest)
	router.HandleFunc("GET /{$}", web.AssetRequest)
	web.UserRouter = router
}

func (web *TunnelWebRouter) Perm(proj *db.Project) bool {
	return true
}

func (web *TunnelWebRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx = context.WithValue(ctx, shared.CtxSubdomainKey{}, web.subdomain)
	web.UserRouter.ServeHTTP(w, r.WithContext(ctx))
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
			log.Error("could not get project from subdomain", "err", err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		owner, err := dbh.FindUserForName(props.Username)
		if err != nil {
			log.Error(
				"could not find user from name",
				"name", props.Username,
				"err", err.Error(),
			)
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}
		log = log.With(
			"owner", owner.Name,
		)

		project, err := dbh.FindProjectByName(owner.ID, props.ProjectName)
		if err != nil {
			log.Error("could not get project by name", "project", props.ProjectName, "err", err.Error())
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
		publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey))
		if err != nil {
			log.Error("could not parse public key", "pubkey", pubkey, "err", err)
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
		tunnelRouter := TunnelWebRouter{routes, subdomain}
		tunnelRouter.initRouters()
		return &tunnelRouter
	}
}
