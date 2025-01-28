package pipe

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/shared"
	"github.com/picosh/utils/pipe"
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

func serveFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)
		cfg := shared.GetCfg(r)

		contents, err := os.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error("could not read statis file", "err", err.Error())
			http.Error(w, "file not found", 404)
		}
		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error("could not write static file", "err", err.Error())
			http.Error(w, "server error", http.StatusInternalServerError)
		}
	}
}

func createStaticRoutes() []shared.Route {
	return []shared.Route{
		shared.NewRoute("GET", "/main.css", serveFile("main.css", "text/css")),
		shared.NewRoute("GET", "/smol.css", serveFile("smol.css", "text/css")),
		shared.NewRoute("GET", "/syntax.css", serveFile("syntax.css", "text/css")),
		shared.NewRoute("GET", "/card.png", serveFile("card.png", "image/png")),
		shared.NewRoute("GET", "/favicon-16x16.png", serveFile("favicon-16x16.png", "image/png")),
		shared.NewRoute("GET", "/favicon-32x32.png", serveFile("favicon-32x32.png", "image/png")),
		shared.NewRoute("GET", "/apple-touch-icon.png", serveFile("apple-touch-icon.png", "image/png")),
		shared.NewRoute("GET", "/favicon.ico", serveFile("favicon.ico", "image/x-icon")),
		shared.NewRoute("GET", "/robots.txt", serveFile("robots.txt", "text/plain")),
		shared.NewRoute("GET", "/anim.js", serveFile("anim.js", "text/javascript")),
	}
}

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

func handleSub(pubsub bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(shared.GetField(r, 0))

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

func handlePub(pubsub bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(shared.GetField(r, 0))

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
				n, err := p.Write(first)
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

func handlePipe() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := shared.GetLogger(r)

		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			logger.Error("pipe upgrade error", "err", err.Error())
			return
		}

		defer c.Close()

		clientInfo := shared.NewPicoPipeClient()
		topic, _ := url.PathUnescape(shared.GetField(r, 0))

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
			c.Close()
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer func() {
				p.Close()
				c.Close()
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
				p.Close()
				c.Close()
				wg.Done()
			}()

			for {
				buf := make([]byte, 32*1024)

				n, err := p.Read(buf)
				if err != nil {
					logger.Error("pipe read error", "topic", topic, "info", clientInfo, "err", err.Error())
					break
				}

				buf = buf[:n]

				err = c.WriteMessage(messageType, buf)
				if err != nil {
					logger.Error("pipe write error", "topic", topic, "info", clientInfo, "err", err.Error())
					break
				}
			}
		}()

		wg.Wait()
	}
}

func createMainRoutes(staticRoutes []shared.Route) []shared.Route {
	routes := []shared.Route{
		shared.NewRoute("GET", "/", shared.CreatePageHandler("html/marketing.page.tmpl")),
		shared.NewRoute("GET", "/check", shared.CheckHandler),
	}

	pipeRoutes := []shared.Route{
		shared.NewRoute("GET", "/topic/(.+)", handleSub(false)),
		shared.NewRoute("POST", "/topic/(.+)", handlePub(false)),
		shared.NewRoute("GET", "/pubsub/(.+)", handleSub(true)),
		shared.NewRoute("POST", "/pubsub/(.+)", handlePub(true)),
		shared.NewRoute("GET", "/pipe/(.+)", handlePipe()),
	}

	for _, route := range pipeRoutes {
		route.CorsEnabled = true
		routes = append(routes, route)
	}

	routes = append(
		routes,
		staticRoutes...,
	)

	return routes
}

func StartApiServer() {
	cfg := NewConfigSite()
	db := postgres.NewDB(cfg.DbURL, cfg.Logger)
	defer db.Close()
	logger := cfg.Logger

	staticRoutes := createStaticRoutes()

	if cfg.Debug {
		staticRoutes = shared.CreatePProfRoutes(staticRoutes)
	}

	mainRoutes := createMainRoutes(staticRoutes)
	subdomainRoutes := staticRoutes

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
			_, err := pingSession.Write([]byte(fmt.Sprintf("%s: pipe-web ping\n", time.Now().UTC().Format(time.RFC3339))))
			if err != nil {
				logger.Error("pipe ping error", "err", err.Error())
			}

			time.Sleep(5 * time.Second)
		}
	}()

	apiConfig := &shared.ApiConfig{
		Cfg:    cfg,
		Dbpool: db,
	}
	handler := shared.CreateServe(mainRoutes, subdomainRoutes, apiConfig)
	router := http.HandlerFunc(handler)

	portStr := fmt.Sprintf(":%s", cfg.Port)
	logger.Info(
		"Starting server on port",
		"port", cfg.Port,
		"domain", cfg.Domain,
	)

	logger.Error("listen", "err", http.ListenAndServe(portStr, router).Error())
}
