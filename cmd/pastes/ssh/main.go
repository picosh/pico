package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"git.sr.ht/~erock/pico/filehandlers"
	"git.sr.ht/~erock/pico/pastes"
	"git.sr.ht/~erock/pico/shared"
	"git.sr.ht/~erock/pico/wish/cms"
	"git.sr.ht/~erock/pico/wish/cms/db/postgres"
	"git.sr.ht/~erock/pico/wish/proxy"
	"git.sr.ht/~erock/pico/wish/send/scp"
	"git.sr.ht/~erock/pico/wish/send/sftp"
	"git.sr.ht/~erock/pico/wish/send/utils"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	lm "github.com/charmbracelet/wish/logging"
	"github.com/gliderlabs/ssh"
)

type SSHServer struct{}

func (me *SSHServer) authHandler(ctx ssh.Context, key ssh.PublicKey) bool {
	return true
}

func createRouter(handler *filehandlers.ScpUploadHandler) proxy.Router {
	return func(sh ssh.Handler, s ssh.Session) []wish.Middleware {
		cmd := s.Command()
		mdw := []wish.Middleware{}

		if len(cmd) > 0 && cmd[0] == "scp" {
			mdw = append(mdw, scp.Middleware(handler))
		} else {
			mdw = append(mdw,
				pasteMiddleware(handler),
				bm.Middleware(cms.Middleware(&handler.Cfg.ConfigCms, handler.Cfg)),
				lm.Middleware(),
			)
		}

		return mdw
	}
}

func pasteMiddleware(writeHandler *filehandlers.ScpUploadHandler) wish.Middleware {
	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				_ = session.Close()
				return
			}

			err := writeHandler.Validate(session)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			name := strings.TrimSpace(strings.Join(session.Command(), " "))
			postTime := time.Now()

			if name == "" {
				name = strconv.Itoa(int(postTime.UnixNano()))
			}

			result, err := writeHandler.Write(session, &utils.FileEntry{
				Name:     name,
				Filepath: name,
				Mode:     fs.FileMode(0777),
				Size:     0,
				Mtime:    postTime.Unix(),
				Atime:    postTime.Unix(),
				Reader:   session,
			})
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			if result != "" {
				_, err = session.Write([]byte(fmt.Sprintf("%s\n", result)))
				if err != nil {
					utils.ErrorHandler(session, err)
				}
				return
			}

			sshHandler(session)
		}
	}
}

func withProxy(handler *filehandlers.ScpUploadHandler) ssh.Option {
	return func(server *ssh.Server) error {
		err := sftp.SSHOption(handler)(server)
		if err != nil {
			return err
		}

		return proxy.WithProxy(createRouter(handler))(server)
	}
}

func main() {
	host := shared.GetEnv("PASTES_HOST", "0.0.0.0")
	port := shared.GetEnv("PASTES_SSH_PORT", "2222")
	cfg := pastes.NewConfigSite()
	logger := cfg.Logger
	dbh := postgres.NewDB(&cfg.ConfigCms)
	defer dbh.Close()
	hooks := &pastes.FileHooks{
		Cfg: cfg,
	}
	handler := filehandlers.NewScpPostHandler(dbh, cfg, hooks)

	sshServer := &SSHServer{}
	s, err := wish.NewServer(
		wish.WithAddress(fmt.Sprintf("%s:%s", host, port)),
		wish.WithHostKeyPath("ssh_data/term_info_ed25519"),
		wish.WithPublicKeyAuth(sshServer.authHandler),
		withProxy(handler),
	)
	if err != nil {
		logger.Fatal(err)
	}

	done := make(chan os.Signal, 1)
	signal.Notify(done, os.Interrupt, syscall.SIGINT, syscall.SIGTERM)
	logger.Infof("Starting SSH server on %s:%s", host, port)
	go func() {
		if err = s.ListenAndServe(); err != nil {
			logger.Fatal(err)
		}
	}()

	<-done
	logger.Info("Stopping SSH server")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer func() { cancel() }()
	if err := s.Shutdown(ctx); err != nil {
		logger.Fatal(err)
	}
}
