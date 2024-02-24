package pgs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/charmbracelet/promwish"
	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	gocache "github.com/patrickmn/go-cache"
	"github.com/picosh/pico/db/postgres"
	uploadassets "github.com/picosh/pico/filehandlers/assets"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	wsh "github.com/picosh/pico/wish"
	"github.com/picosh/ptun"
	"github.com/picosh/send/list"
	"github.com/picosh/send/pipe"
	"github.com/picosh/send/proxy"
	"github.com/picosh/send/send/auth"
	wishrsync "github.com/picosh/send/send/rsync"
	"github.com/picosh/send/send/scp"
	"github.com/picosh/send/send/sftp"
)

type SSHServer struct{}

type ctxPublicKey struct{}

func getPublicKeyCtx(ctx ssh.Context) (ssh.PublicKey, error) {
	pk, ok := ctx.Value(ctxPublicKey{}).(ssh.PublicKey)
	if !ok {
		return nil, fmt.Errorf("public key not set on `ssh.Context()` for connection")
	}
	return pk, nil
}
func setPublicKeyCtx(ctx ssh.Context, pk ssh.PublicKey) {
	ctx.SetValue(ctxPublicKey{}, pk)
}

func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	setPublicKeyCtx(ctx, key)
	return true
}

func createRouter(cfg *shared.ConfigSite, handler *uploadassets.UploadAssetHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		return []wish.Middleware{
			pipe.Middleware(handler, ""),
			list.Middleware(handler),
			scp.Middleware(handler),
			wishrsync.Middleware(handler),
			auth.Middleware(handler),
			wsh.PtyMdw(bm.Middleware(CmsMiddleware(&cfg.ConfigCms, cfg))),
			WishMiddleware(handler),
			wsh.LogMiddleware(handler.GetLogger()),
		}
	}
}

func withProxy(cfg *shared.ConfigSite, handler *uploadassets.UploadAssetHandler, otherMiddleware ...wish.Middleware) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(cfg, handler), otherMiddleware...)(server)
	}
}

func unauthorizedHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
}

type PicoApi struct {
	UserID    string `json:"user_id"`
	UserName  string `json:"username"`
	PublicKey string `json:"public_key"`
}

func StartSshServer() {
	host := shared.GetEnv("PGS_HOST", "0.0.0.0")
	port := shared.GetEnv("PGS_SSH_PORT", "2222")
	promPort := shared.GetEnv("PGS_PROM_PORT", "9222")
	cfg := NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer dbh.Close()

	var st storage.StorageServe
	var err error
	if cfg.MinioURL == "" {
		st, err = storage.NewStorageFS(cfg.StorageDir)
	} else {
		st, err = storage.NewStorageMinio(cfg.MinioURL, cfg.MinioUser, cfg.MinioPass)
	}

	if err != nil {
		logger.Error(err.Error())
		return
	}

	handler := uploadassets.NewUploadAssetHandler(
		dbh,
		cfg,
		st,
	)
	cache := gocache.New(2*time.Minute, 5*time.Minute)

	webTunnel := &ptun.WebTunnelHandler{
		HttpHandler: func(ctx ssh.Context) http.Handler {
			subdomain := ctx.User()
			log := logger.With(
				"subdomain", subdomain,
			)

			pubkey, err := getPublicKeyCtx(ctx)
			if err != nil {
				log.Error(err.Error(), "subdomain", subdomain)
				return http.HandlerFunc(unauthorizedHandler)
			}
			pubkeyStr, err := shared.KeyForKeyText(pubkey)
			if err != nil {
				log.Error(err.Error())
				return http.HandlerFunc(unauthorizedHandler)
			}
			log = log.With(
				"pubkey", pubkeyStr,
			)

			props, err := getProjectFromSubdomain(subdomain)
			if err != nil {
				log.Error(err.Error())
				return http.HandlerFunc(unauthorizedHandler)
			}

			owner, err := dbh.FindUserForName(props.Username)
			if err != nil {
				log.Error(err.Error())
				return http.HandlerFunc(unauthorizedHandler)
			}
			log = log.With(
				"owner", owner.Name,
			)

			project, err := dbh.FindProjectByName(owner.ID, props.ProjectName)
			if err != nil {
				log.Error(err.Error())
				return http.HandlerFunc(unauthorizedHandler)
			}

			requester, _ := dbh.FindUserForKey("", pubkeyStr)
			if requester != nil {
				log = logger.With(
					"requester", requester.Name,
				)
			}

			if !HasProjectAccess(project, owner, requester, pubkey) {
				log.Error("no access")
				return http.HandlerFunc(unauthorizedHandler)
			}

			log.Info("user has access to site")

			routes := []shared.Route{
				// special API endpoint for tunnel users accessing site
				shared.NewRoute("GET", "/pico", func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					pico := &PicoApi{
						UserID:    "",
						UserName:  "",
						PublicKey: pubkeyStr,
					}
					if requester != nil {
						pico.UserID = requester.ID
						pico.UserName = requester.Name
					}
					err := json.NewEncoder(w).Encode(pico)
					if err != nil {
						log.Error(err.Error())
					}
				}),
			}
			routes = append(routes, subdomainRoutes...)
			httpHandler := shared.CreateServeBasic(
				routes,
				subdomain,
				cfg,
				dbh,
				st,
				logger,
				cache,
			)
			httpRouter := http.HandlerFunc(httpHandler)
			return httpRouter
		},
	}

	sshServer := &SSHServer{}
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshServer.authHandler),
		ptun.WithWebTunnel(webTunnel),
		withProxy(
			cfg,
			handler,
			promwish.Middleware(fmt.Sprintf("%s:%s", host, promPort), "pgs-ssh"),
		),
	)
	if err != nil {
		logger.Error(err.Error())
		return
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Info("Starting SSH server on", "host", host, "port", port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Error(err.Error())
		}
	}()

	<-done
	logger.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Error(err.Error())
	}
}
