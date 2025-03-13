package tunkit

import (
	"fmt"
	"log/slog"
	"net"
	"net/http"

	"github.com/picosh/pico/pkg/pssh"
)

type HttpHandlerFn = func(ctx *pssh.SSHServerConnSession) http.Handler

type WebTunnel interface {
	GetHttpHandler() HttpHandlerFn
	CreateListener(ctx *pssh.SSHServerConnSession) (net.Listener, error)
	CreateConn(ctx *pssh.SSHServerConnSession) (net.Conn, error)
	GetLogger() *slog.Logger
	Close(ctx *pssh.SSHServerConnSession) error
}

type ctxListenerKey struct{}

func getListenerCtx(ctx *pssh.SSHServerConnSession) (net.Listener, error) {
	listener, ok := ctx.Value(ctxListenerKey{}).(net.Listener)
	if listener == nil || !ok {
		return nil, fmt.Errorf("listener not set on `*pssh.SSHServerConnSession()` for connection")
	}
	return listener, nil
}

func setListenerCtx(ctx *pssh.SSHServerConnSession, listener net.Listener) {
	ctx.SetValue(ctxListenerKey{}, listener)
}

func httpServe(handler WebTunnel, ctx *pssh.SSHServerConnSession, log *slog.Logger) (net.Listener, error) {
	cached, _ := getListenerCtx(ctx)
	if cached != nil {
		return cached, nil
	}

	listener, err := handler.CreateListener(ctx)
	if err != nil {
		return nil, err
	}
	setListenerCtx(ctx, listener)

	go func() {
		httpHandler := handler.GetHttpHandler()
		err := http.Serve(listener, httpHandler(ctx))
		if err != nil {
			log.Error("serving http content", "err", err)
		}
	}()

	return listener, nil
}
