package tunkit

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"

	"github.com/picosh/pico/pkg/pssh"
	"golang.org/x/crypto/ssh"
)

type forwardedTCPPayload struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

type Tunnel interface {
	CreateConn(ctx *pssh.SSHServerConnSession) (net.Conn, error)
	GetLogger() *slog.Logger
	Close(ctx *pssh.SSHServerConnSession) error
}

func LocalForwardHandler(handler Tunnel) pssh.SSHServerChannelMiddleware {
	return func(newChan ssh.NewChannel, sc *pssh.SSHServerConn) error {
		check := &forwardedTCPPayload{}
		err := ssh.Unmarshal(newChan.ExtraData(), check)
		logger := handler.GetLogger()
		if err != nil {
			logger.Error(
				"error unmarshaling information",
				"err", err,
			)
			return err
		}

		log := logger.With(
			"addr", check.Addr,
			"port", check.Port,
			"origAddr", check.OriginAddr,
			"origPort", check.OriginPort,
		)
		log.Info("local forward request")

		ch, reqs, err := newChan.Accept()
		if err != nil {
			log.Error("cannot accept new channel", "err", err)
			return err
		}

		origCtx, cancel := context.WithCancel(context.Background())
		ctx := &pssh.SSHServerConnSession{
			Channel:       ch,
			SSHServerConn: sc,
			Ctx:           origCtx,
			CancelFunc:    cancel,
		}

		go ssh.DiscardRequests(reqs)

		go func() {
			downConn, err := handler.CreateConn(ctx)
			if err != nil {
				log.Error("unable to connect to conn", "err", err)
				ch.Close()
				return
			}
			defer downConn.Close()

			var wg sync.WaitGroup
			wg.Add(2)

			go func() {
				defer wg.Done()
				defer func() {
					_ = ch.CloseWrite()
				}()
				defer downConn.Close()
				_, err := io.Copy(ch, downConn)
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						log.Error("io copy", "err", err)
					}
				}
			}()
			go func() {
				defer wg.Done()
				defer ch.Close()
				defer downConn.Close()
				_, err := io.Copy(downConn, ch)
				if err != nil {
					if !errors.Is(err, net.ErrClosed) {
						log.Error("io copy", "err", err)
					}
				}
			}()

			wg.Wait()
		}()

		<-ctx.Done()
		err = handler.Close(ctx)
		if err != nil {
			log.Error("tunnel handler error", "err", err)
		}
		return err
	}
}
