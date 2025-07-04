package pgs

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"golang.org/x/crypto/ssh"
)

type TunnelWebRouter struct {
	*WebRouter
	subdomain string
}

func (web *TunnelWebRouter) InitRouter() {
	router := http.NewServeMux()
	router.HandleFunc("GET /{fname...}", web.AssetRequest(tunnelPerm))
	router.HandleFunc("GET /{$}", web.AssetRequest(tunnelPerm))
	web.UserRouter = router
}

func tunnelPerm(proj *db.Project) bool {
	return true
}

func (web *TunnelWebRouter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	ctx = context.WithValue(ctx, shared.CtxSubdomainKey{}, web.subdomain)
	web.UserRouter.ServeHTTP(w, r.WithContext(ctx))
}

type CtxHttpBridge = func(*pssh.SSHServerConnSession) http.Handler

func getInfoFromUser(user string) (string, string) {
	if strings.Contains(user, "__") {
		results := strings.SplitN(user, "__", 2)
		return results[0], results[1]
	}

	return "", user
}

func CreateHttpHandler(cfg *PgsConfig) CtxHttpBridge {
	return func(ctx *pssh.SSHServerConnSession) http.Handler {
		logger := cfg.Logger
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

		owner, err := cfg.DB.FindUserByName(props.Username)
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

		project, err := cfg.DB.FindProjectByName(owner.ID, props.ProjectName)
		if err != nil {
			log.Error("could not get project by name", "project", props.ProjectName, "err", err.Error())
			return http.HandlerFunc(shared.UnauthorizedHandler)
		}

		requester, _ := cfg.DB.FindUserByPubkey(pubkey)
		if requester != nil {
			log = log.With(
				"requester", requester.Name,
			)
		}

		// impersonation logic
		if asUser != "" {
			isAdmin := false
			ff, _ := cfg.DB.FindFeature(requester.ID, "admin")
			if ff != nil {
				if ff.ExpiresAt.Before(time.Now()) {
					isAdmin = true
				}
			}

			if !isAdmin {
				log.Error("impersonation attempt failed")
				return http.HandlerFunc(shared.UnauthorizedHandler)
			}
			requester, _ = cfg.DB.FindUserByName(asUser)
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

		routes := NewWebRouter(cfg)
		tunnelRouter := TunnelWebRouter{routes, subdomain}
		tunnelRouter.InitRouter()
		return &tunnelRouter
	}
}
