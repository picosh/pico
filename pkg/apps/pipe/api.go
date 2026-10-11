package pipe

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
	"github.com/picosh/utils/pipe"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	cleanRegex = regexp.MustCompile(`[^0-9a-zA-Z,/]`)
	sshClient  *pipe.Client
	upgrader   = websocket.Upgrader{
		CheckOrigin: func(r *http.Request) bool {
			return true
		},
	}
)

type writeFlusher struct {
	responseWriter http.ResponseWriter
	controller     *http.ResponseController
}

func (w writeFlusher) Write(p []byte) (n int, err error) {
	n, err = w.responseWriter.Write(p)
	if err == nil {
		err = w.controller.Flush()
	}
	return
}

var _ io.Writer = writeFlusher{}

func (web *webRouter) handleSub(pubsub bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := web.cfg.Logger

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(r.PathValue("topic"))

		topic = cleanRegex.ReplaceAllString(topic, "")

		logger.Info("sub", "topic", topic, "info", clientInfo, "pubsub", pubsub)

		params := "-p"
		if r.URL.Query().Get("persist") == "true" {
			params += " -k"
		}

		if accessList := r.URL.Query().Get("access"); accessList != "" {
			logger.Info("adding access list", "topic", topic, "info", clientInfo, "access", accessList)
			cleanList := cleanRegex.ReplaceAllString(accessList, "")
			params += fmt.Sprintf(" -a=%s", cleanList)
		}

		id := uuid.NewString()

		p, err := sshClient.AddSession(id, fmt.Sprintf("sub %s %s", params, topic), 0, -1, -1)
		if err != nil {
			logger.Error("sub error", "topic", topic, "info", clientInfo, "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		go func() {
			<-r.Context().Done()
			err := sshClient.RemoveSession(id)
			if err != nil {
				logger.Error("sub remove error", "topic", topic, "info", clientInfo, "err", err.Error())
			}
		}()

		if mime := r.URL.Query().Get("mime"); mime != "" {
			w.Header().Add("Content-Type", r.URL.Query().Get("mime"))
		}

		w.WriteHeader(http.StatusOK)

		_, err = io.Copy(writeFlusher{w, http.NewResponseController(w)}, p)
		if err != nil {
			logger.Error("sub copy error", "topic", topic, "info", clientInfo, "err", err.Error())
			return
		}
	}
}

func (web *webRouter) handlePub(pubsub bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := web.cfg.Logger

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(r.PathValue("topic"))

		topic = cleanRegex.ReplaceAllString(topic, "")

		logger.Info("pub", "topic", topic, "info", clientInfo)

		params := "-p"
		if pubsub {
			params += " -b=false"
		}

		if accessList := r.URL.Query().Get("access"); accessList != "" {
			logger.Info("adding access list", "topic", topic, "info", clientInfo, "access", accessList)
			cleanList := cleanRegex.ReplaceAllString(accessList, "")
			params += fmt.Sprintf(" -a=%s", cleanList)
		}

		prefix := r.URL.Query().Get("prefix")

		var wg sync.WaitGroup

		reader := bufio.NewReaderSize(r.Body, 1)

		first := make([]byte, 1)

		nFirst, err := reader.Read(first)
		if err != nil && !errors.Is(err, io.EOF) {
			logger.Error("pub peek error", "topic", topic, "info", clientInfo, "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		if nFirst == 0 {
			params += " -e"
		}

		id := uuid.NewString()

		p, err := sshClient.AddSession(id, fmt.Sprintf("pub %s %s", params, topic), 0, -1, -1)
		if err != nil {
			logger.Error("pub error", "topic", topic, "info", clientInfo, "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		go func() {
			<-r.Context().Done()
			err := sshClient.RemoveSession(id)
			if err != nil {
				logger.Error("pub remove error", "topic", topic, "info", clientInfo, "err", err.Error())
			}
		}()

		var scanErr error
		scanStatus := http.StatusInternalServerError

		wg.Add(1)

		go func() {
			defer wg.Done()

			s := bufio.NewScanner(p)
			s.Buffer(make([]byte, 32*1024), 32*1024)

			for s.Scan() {
				if s.Text() == "sending msg ..." {
					time.Sleep(10 * time.Millisecond)
					break
				}

				if strings.HasPrefix(s.Text(), "  ssh ") {
					f := strings.Fields(s.Text())
					if len(f) > 1 && f[len(f)-1] != topic {
						scanErr = fmt.Errorf("pub is not same as used, expected `%s` and received `%s`", topic, f[len(f)-1])
						scanStatus = http.StatusUnauthorized
						return
					}
				}
			}

			if err := s.Err(); err != nil {
				scanErr = err
				return
			}
		}()

		wg.Wait()

		if scanErr != nil {
			logger.Error("pub scan error", "topic", topic, "info", clientInfo, "err", scanErr.Error())

			msg := "server error"
			if scanStatus == http.StatusUnauthorized {
				msg = "access denied"
			}

			http.Error(w, msg, scanStatus)
			return
		}

	outer:
		for {
			select {
			case <-r.Context().Done():
				break outer
			default:
				messageToWrite := first
				if prefix != "" {
					messageToWrite = append([]byte(prefix), messageToWrite...)
				}

				n, err := p.Write(messageToWrite)
				if err != nil {
					logger.Error("pub write error", "topic", topic, "info", clientInfo, "err", err.Error())
					http.Error(w, "server error", http.StatusInternalServerError)
					return
				}

				if n > 0 {
					break outer
				}

				time.Sleep(10 * time.Millisecond)
			}
		}

		_, err = io.Copy(p, reader)
		if err != nil {
			logger.Error("pub copy error", "topic", topic, "info", clientInfo, "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)

		time.Sleep(10 * time.Millisecond)
	}
}

func (web *webRouter) handlePipe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := web.cfg.Logger

		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("pipe upgrade error", "err", err.Error())
			return
		}

		defer func() {
			_ = c.Close()
		}()

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(r.PathValue("topic"))

		topic = cleanRegex.ReplaceAllString(topic, "")

		logger.Info("pipe", "topic", topic, "info", clientInfo)

		params := "-p -c"
		if r.URL.Query().Get("status") == "true" {
			params = params[:len(params)-3]
		}

		if r.URL.Query().Get("replay") == "true" {
			params += " -r"
		}

		messageType := websocket.TextMessage
		if r.URL.Query().Get("binary") == "true" {
			messageType = websocket.BinaryMessage
		}

		if accessList := r.URL.Query().Get("access"); accessList != "" {
			logger.Info("adding access list", "topic", topic, "info", clientInfo, "access", accessList)
			cleanList := cleanRegex.ReplaceAllString(accessList, "")
			params += fmt.Sprintf(" -a=%s", cleanList)
		}

		prefix := r.URL.Query().Get("prefix")

		id := uuid.NewString()

		p, err := sshClient.AddSession(id, fmt.Sprintf("pipe %s %s", params, topic), 0, -1, -1)
		if err != nil {
			logger.Error("pipe error", "topic", topic, "info", clientInfo, "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
			return
		}

		go func() {
			<-r.Context().Done()
			err := sshClient.RemoveSession(id)
			if err != nil {
				logger.Error("pipe remove error", "topic", topic, "info", clientInfo, "err", err.Error())
			}
			_ = c.Close()
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer func() {
				_ = p.Close()
				_ = c.Close()
				wg.Done()
			}()

			for {
				_, message, err := c.ReadMessage()
				if err != nil {
					logger.Error("pipe read error", "topic", topic, "info", clientInfo, "err", err.Error())
					break
				}

				_, err = p.Write(message)
				if err != nil {
					logger.Error("pipe write error", "topic", topic, "info", clientInfo, "err", err.Error())
					break
				}
			}
		}()

		go func() {
			defer func() {
				_ = p.Close()
				_ = c.Close()
				wg.Done()
			}()

			var messageBuffer []byte

			for {
				buf := make([]byte, 32*1024)

				n, err := p.Read(buf)
				if err != nil {
					logger.Error("pipe read error", "topic", topic, "info", clientInfo, "err", err.Error())
					break
				}

				messageBuffer = append(messageBuffer, buf[:n]...)

				if prefix != "" {
					// Buffer and split on prefix boundaries
					for {
						firstIdx := bytes.Index(messageBuffer, []byte(prefix))
						if firstIdx == -1 {
							// No prefix found, clear buffer (shouldn't happen in normal use)
							messageBuffer = nil
							break
						}

						// Look for next prefix after the first one
						secondIdx := bytes.Index(messageBuffer[firstIdx+len(prefix):], []byte(prefix))
						if secondIdx == -1 {
							// No complete message yet, keep buffer as is
							break
						}

						// We have a complete message, extract and send it
						messageToSend := messageBuffer[firstIdx : firstIdx+len(prefix)+secondIdx]
						err = c.WriteMessage(messageType, messageToSend)
						if err != nil {
							logger.Error("pipe write error", "topic", topic, "info", clientInfo, "err", err.Error())
							break
						}

						// Update buffer to remove sent message
						messageBuffer = messageBuffer[firstIdx+len(prefix)+secondIdx:]
					}
				} else {
					// No prefix set, send all data as-is
					if len(messageBuffer) > 0 {
						err = c.WriteMessage(messageType, messageBuffer)
						if err != nil {
							logger.Error("pipe write error", "topic", topic, "info", clientInfo, "err", err.Error())
							break
						}
						messageBuffer = nil
					}
				}
			}
		}()

		wg.Wait()
	}
}

func rssHandler(cfg *shared.ConfigSite, dbpool DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		apiToken, _ := url.PathUnescape(r.PathValue("token"))
		user, err := dbpool.FindUserByToken(apiToken)
		if err != nil {
			cfg.Logger.Error(
				"could not find user for token",
				"err", err.Error(),
				"token", apiToken,
			)
			http.Error(w, "invalid token", http.StatusNotFound)
			return
		}
		rss, err := MonitorRss(dbpool, user, cfg.Domain)
		if err != nil {
			cfg.Logger.Error(
				"error generating monitor rss feed",
				"err", err,
				"token", apiToken,
			)
			http.Error(w, "error generating monitor rss feed", http.StatusInternalServerError)
			return
		}

		_, err = w.Write([]byte(rss))
		if err != nil {
			cfg.Logger.Error(
				"error with rss response writer",
				"err", err,
				"token", apiToken,
			)
			http.Error(w, "error generating monitor rss feederror with rss response writer", http.StatusInternalServerError)
			return
		}
	}
}

type webRouter struct {
	cfg *shared.ConfigSite
	db  DB
}

func (web *webRouter) static(mux *http.ServeMux) {
	cfg := web.cfg
	mux.HandleFunc("GET /favicon-16x16.png", router.ServeFile(cfg, "favicon-16x16.png", "image/png"))
	mux.HandleFunc("GET /apple-touch-icon.png", router.ServeFile(cfg, "apple-touch-icon.png", "image/png"))
	mux.HandleFunc("GET /favicon.ico", router.ServeFile(cfg, "favicon.ico", "image/x-icon"))
	mux.HandleFunc("GET /robots.txt", router.ServeFile(cfg, "robots.txt", "text/plain"))
	mux.HandleFunc("GET /anim.js", router.ServeFile(cfg, "anim.js", "text/javascript"))
	if cfg.Debug {
		router.CreatePProfRoutesMux(mux)
	}
}

func (web *webRouter) mainMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /{$}", router.CreatePageHandler(web.cfg, "html/marketing.page.tmpl"))
	mux.HandleFunc("GET /check", router.CheckHandler(web.cfg, web.db))
	mux.HandleFunc("GET /rss/{token...}", rssHandler(web.cfg, web.db))
	mux.Handle("GET /_metrics", promhttp.Handler())

	mux.HandleFunc("GET /topic/{topic...}", router.Cors(web.handleSub(false)))
	mux.HandleFunc("POST /topic/{topic...}", router.Cors(web.handlePub(false)))
	mux.HandleFunc("OPTIONS /topic/{topic...}", router.CorsPreflight)
	mux.HandleFunc("GET /pubsub/{topic...}", router.Cors(web.handleSub(true)))
	mux.HandleFunc("POST /pubsub/{topic...}", router.Cors(web.handlePub(true)))
	mux.HandleFunc("OPTIONS /pubsub/{topic...}", router.CorsPreflight)
	mux.HandleFunc("GET /pipe/{topic...}", router.Cors(web.handlePipe()))
	mux.HandleFunc("OPTIONS /pipe/{topic...}", router.CorsPreflight)

	web.static(mux)
	return mux
}

// sitesMux serves only static files on subdomains.
func (web *webRouter) sitesMux() *http.ServeMux {
	mux := http.NewServeMux()
	web.static(mux)
	return mux
}

func StartApiServer() {
	cfg := NewConfigSite("pipe-web")
	db := NewDB(postgres.NewDB(cfg.DbURL, cfg.Logger))
	defer func() {
		_ = db.Close()
	}()
	logger := cfg.Logger

	info := shared.NewPicoPipeClient()

	client, err := pipe.NewClient(context.Background(), logger.With("info", info), info)
	if err != nil {
		panic(err)
	}

	sshClient = client

	pingSession, err := sshClient.AddSession("ping", "pub -b=false -c ping", 0, -1, -1)
	if err != nil {
		panic(err)
	}

	go func() {
		for {
			_, err := fmt.Fprintf(pingSession, "%s: pipe-web ping\n", time.Now().UTC().Format(time.RFC3339))
			if err != nil {
				logger.Error("pipe ping error", "err", err.Error())
			}

			time.Sleep(5 * time.Second)
		}
	}()

	web := &webRouter{cfg: cfg, db: db}
	handler := router.NewHostRouter(cfg, web.mainMux(), web.sitesMux())

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error("listen", "err", http.ListenAndServe(portStr, handler).Error())
}
