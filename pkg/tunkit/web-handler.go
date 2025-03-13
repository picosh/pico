package tunkit

import (
	"fmt"
	"log/slog"
	"net"
	"os"

	"github.com/picosh/pico/pkg/pssh"
)

type ctxAddressKey struct{}

func getAddressCtx(ctx *pssh.SSHServerConnSession) (string, error) {
	address, ok := ctx.Value(ctxAddressKey{}).(string)
	if address == "" || !ok {
		return address, fmt.Errorf("address not set on `*pssh.SSHServerConnSession()` for connection")
	}
	return address, nil
}
func setAddressCtx(ctx *pssh.SSHServerConnSession, address string) {
	ctx.SetValue(ctxAddressKey{}, address)
}

type WebTunnelHandler struct {
	HttpHandler HttpHandlerFn
	Logger      *slog.Logger
}

func NewWebTunnelHandler(handler HttpHandlerFn, logger *slog.Logger) *WebTunnelHandler {
	return &WebTunnelHandler{
		HttpHandler: handler,
		Logger:      logger,
	}
}

func (wt *WebTunnelHandler) GetLogger() *slog.Logger {
	return wt.Logger
}

func (wt *WebTunnelHandler) GetHttpHandler() HttpHandlerFn {
	return wt.HttpHandler
}

func (wt *WebTunnelHandler) Close(ctx *pssh.SSHServerConnSession) error {
	listener, err := getListenerCtx(ctx)
	if err != nil {
		return err
	}

	if listener != nil {
		_ = listener.Close()
		setListenerCtx(ctx, nil)
	}

	return nil
}

func (wt *WebTunnelHandler) CreateListener(ctx *pssh.SSHServerConnSession) (net.Listener, error) {
	tempFile, err := os.CreateTemp("", "")
	if err != nil {
		return nil, err
	}

	tempFile.Close()
	address := tempFile.Name()
	os.Remove(address)

	connListener, err := net.Listen("unix", address)
	if err != nil {
		return nil, err
	}
	setAddressCtx(ctx, address)
	setListenerCtx(ctx, connListener)

	return connListener, nil
}

func (wt *WebTunnelHandler) CreateConn(ctx *pssh.SSHServerConnSession) (net.Conn, error) {
	_, err := httpServe(wt, ctx, wt.GetLogger())
	if err != nil {
		wt.GetLogger().Info("unable to create listener", "err", err)
		return nil, err
	}

	address, err := getAddressCtx(ctx)
	if err != nil {
		return nil, err
	}

	return net.Dial("unix", address)
}
