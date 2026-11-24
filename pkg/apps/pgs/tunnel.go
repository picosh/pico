package pgs

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
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
	ctx := router.WithSubdomain(r.Context(), web.subdomain)
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

// tunnelRequester is the user a tunnel acts as: the key's owner, or for an
// admin, the user named in the ssh username.
func tunnelRequester(cfg *Config, pubkey, asUser string) (*db.User, error) {
	requester, err := cfg.DB.FindUserByPubkey(pubkey)
	if err != nil {
		return nil, fmt.Errorf("find user for pubkey: %w", err)
	}
	if asUser == "" {
		return requester, nil
	}
	if !db.HasFeature(cfg.DB, requester.ID, "admin") {
		return nil, fmt.Errorf("%s is not an admin and cannot impersonate %s", requester.Name, asUser)
	}
	impersonated, err := cfg.DB.FindUserByName(asUser)
	if err != nil {
		return nil, fmt.Errorf("find user to impersonate: %w", err)
	}
	return impersonated, nil
}

// checkTunnelScopes limits a tunnel to the projects its connection's scopes
// cover. Scopes name the account's own projects, so reaching anyone else's
// takes a scope on everything.
func checkTunnelScopes(scopes shared.Scopes, asUser string, owner, requester *db.User, project *db.Project) error {
	if asUser != "" {
		if err := scopes.CheckFull("impersonation"); err != nil {
			return err
		}
	}
	name := project.Name
	if owner.ID != requester.ID {
		name = ""
	}
	return scopes.Check("reading", name, roleRead, roleDeploy)
}

func CreateHttpHandler(cfg *Config) CtxHttpBridge {
	// Tunnels share one set of caches, since each cache runs a goroutine
	// for as long as it exists.
	base := newWebRouter(cfg)
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
			return http.HandlerFunc(router.UnauthorizedHandler)
		}

		log = log.With(
			"pubkey", pubkey,
		)

		props, err := router.GetProjectFromSubdomain(subdomain)
		if err != nil {
			log.Error("could not get project from subdomain", "err", err.Error())
			return http.HandlerFunc(router.UnauthorizedHandler)
		}

		owner, err := cfg.DB.FindUserByName(props.Username)
		if err != nil {
			log.Error(
				"could not find user from name",
				"name", props.Username,
				"err", err.Error(),
			)
			return http.HandlerFunc(router.UnauthorizedHandler)
		}
		log = log.With(
			"owner", owner.Name,
		)

		project, err := cfg.DB.FindProjectByName(owner.ID, props.ProjectName)
		if err != nil {
			log.Error("could not get project by name", "project", props.ProjectName, "err", err.Error())
			return http.HandlerFunc(router.UnauthorizedHandler)
		}

		requester, err := tunnelRequester(cfg, pubkey, asUser)
		if err != nil {
			log.Error("tunnel requester", "err", err)
			return http.HandlerFunc(router.UnauthorizedHandler)
		}
		log = log.With(
			"requester", requester.Name,
		)

		if err := checkTunnelScopes(shared.SessionScopes(ctx), asUser, owner, requester, project); err != nil {
			log.Error("tunnel scopes", "err", err)
			return http.HandlerFunc(router.UnauthorizedHandler)
		}

		publicKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey))
		if err != nil {
			log.Error("could not parse public key", "pubkey", pubkey, "err", err)
			return http.HandlerFunc(router.UnauthorizedHandler)
		}
		if !HasProjectAccess(project, owner, requester, publicKey) {
			log.Error("no access")
			return http.HandlerFunc(router.UnauthorizedHandler)
		}

		log.Info("user has access to site")

		routes := *base
		tunnelRouter := TunnelWebRouter{&routes, subdomain}
		tunnelRouter.InitRouter()
		return &tunnelRouter
	}
}
