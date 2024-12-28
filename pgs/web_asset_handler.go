package pgs

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"net/http/httputil"
	_ "net/http/pprof"

	"github.com/picosh/pico/shared/storage"
	sst "github.com/picosh/pobj/storage"
)

type ApiAssetHandler struct {
	*WebRouter
	Logger *slog.Logger

	Username       string
	UserID         string
	Subdomain      string
	ProjectDir     string
	Filepath       string
	Bucket         sst.Bucket
	ImgProcessOpts *storage.ImgProcessOpts
	ProjectID      string
	HasPicoPlus    bool
}

func hasProtocol(url string) bool {
	isFullUrl := strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://")
	return isFullUrl
}

func (h *ApiAssetHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := h.Logger
	var redirects []*RedirectRule
	redirectFp, redirectInfo, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_redirects"))
	if err == nil {
		defer redirectFp.Close()
		if redirectInfo != nil && redirectInfo.Size > h.Cfg.MaxSpecialFileSize {
			errMsg := fmt.Sprintf("_redirects file is too large (%d > %d)", redirectInfo.Size, h.Cfg.MaxSpecialFileSize)
			logger.Error(errMsg)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		buf := new(strings.Builder)
		lr := io.LimitReader(redirectFp, h.Cfg.MaxSpecialFileSize)
		_, err := io.Copy(buf, lr)
		if err != nil {
			logger.Error("io copy", "err", err.Error())
			http.Error(w, "cannot read _redirects file", http.StatusInternalServerError)
			return
		}

		redirects, err = parseRedirectText(buf.String())
		if err != nil {
			logger.Error("could not parse redirect text", "err", err.Error())
		}
	}

	routes := calcRoutes(h.ProjectDir, h.Filepath, redirects)

	var contents io.ReadCloser
	contentType := ""
	assetFilepath := ""
	info := &sst.ObjectInfo{}
	status := http.StatusOK
	attempts := []string{}
	for _, fp := range routes {
		destUrl, err := url.Parse(fp.Filepath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		destUrl.RawQuery = r.URL.RawQuery

		if checkIsRedirect(fp.Status) {
			// hack: check to see if there's an index file in the requested directory
			// before redirecting, this saves a hop that will just end up a 404
			if !hasProtocol(fp.Filepath) && strings.HasSuffix(fp.Filepath, "/") {
				next := filepath.Join(h.ProjectDir, fp.Filepath, "index.html")
				_, _, err := h.Storage.GetObject(h.Bucket, next)
				if err != nil {
					continue
				}
			}
			logger.Info(
				"redirecting request",
				"destination", destUrl.String(),
				"status", fp.Status,
			)
			http.Redirect(w, r, destUrl.String(), fp.Status)
			return
		} else if hasProtocol(fp.Filepath) {
			if !h.HasPicoPlus {
				msg := "must be pico+ user to fetch content from external source"
				logger.Error(
					msg,
					"destination", destUrl.String(),
					"status", fp.Status,
				)
				http.Error(w, msg, http.StatusUnauthorized)
				return
			}

			logger.Info(
				"fetching content from external service",
				"destination", destUrl.String(),
				"status", fp.Status,
			)

			proxy := httputil.NewSingleHostReverseProxy(destUrl)
			oldDirector := proxy.Director
			proxy.Director = func(r *http.Request) {
				oldDirector(r)
				r.Host = destUrl.Host
				r.URL = destUrl
			}
			// Disable caching
			proxy.ModifyResponse = func(r *http.Response) error {
				r.Header.Set("cache-control", "no-cache")
				return nil
			}
			proxy.ServeHTTP(w, r)
			return
		}

		attempts = append(attempts, fp.Filepath)
		mimeType := storage.GetMimeType(fp.Filepath)
		logger = logger.With("filename", fp.Filepath)
		var c io.ReadCloser
		if strings.HasPrefix(mimeType, "image/") {
			c, contentType, err = h.Storage.ServeObject(
				h.Bucket,
				fp.Filepath,
				h.ImgProcessOpts,
			)
		} else {
			c, info, err = h.Storage.GetObject(h.Bucket, fp.Filepath)
		}
		if err == nil {
			contents = c
			assetFilepath = fp.Filepath
			status = fp.Status
			break
		}
	}

	if assetFilepath == "" {
		logger.Info(
			"asset not found in bucket",
			"routes", strings.Join(attempts, ", "),
			"status", http.StatusNotFound,
		)
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	}
	defer contents.Close()

	if contentType == "" {
		contentType = storage.GetMimeType(assetFilepath)
	}

	var headers []*HeaderRule
	headersFp, headersInfo, err := h.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_headers"))
	if err == nil {
		defer headersFp.Close()
		if headersInfo != nil && headersInfo.Size > h.Cfg.MaxSpecialFileSize {
			errMsg := fmt.Sprintf("_headers file is too large (%d > %d)", headersInfo.Size, h.Cfg.MaxSpecialFileSize)
			logger.Error(errMsg)
			http.Error(w, errMsg, http.StatusInternalServerError)
			return
		}
		buf := new(strings.Builder)
		lr := io.LimitReader(headersFp, h.Cfg.MaxSpecialFileSize)
		_, err := io.Copy(buf, lr)
		if err != nil {
			logger.Error("io copy", "err", err.Error())
			http.Error(w, "cannot read _headers file", http.StatusInternalServerError)
			return
		}

		headers, err = parseHeaderText(buf.String())
		if err != nil {
			logger.Error("could not parse header text", "err", err.Error())
		}
	}

	userHeaders := []*HeaderLine{}
	for _, headerRule := range headers {
		rr := regexp.MustCompile(headerRule.Path)
		match := rr.FindStringSubmatch(assetFilepath)
		if len(match) > 0 {
			userHeaders = headerRule.Headers
		}
	}

	if info != nil {
		if info.Size != 0 {
			w.Header().Add("content-length", strconv.Itoa(int(info.Size)))
		}
		if info.ETag != "" {
			w.Header().Add("etag", info.ETag)
		}

		if !info.LastModified.IsZero() {
			w.Header().Add("last-modified", info.LastModified.Format(http.TimeFormat))
		}
	}

	for _, hdr := range userHeaders {
		w.Header().Add(hdr.Name, hdr.Value)
	}
	if w.Header().Get("content-type") == "" {
		w.Header().Set("content-type", contentType)
	}

	// Allows us to invalidate the cache when files are modified
	w.Header().Set("surrogate-key", h.Subdomain)

	finContentType := w.Header().Get("content-type")

	logger.Info(
		"serving asset",
		"asset", assetFilepath,
		"status", status,
		"contentType", finContentType,
	)

	w.WriteHeader(status)
	_, err = io.Copy(w, contents)

	if err != nil {
		logger.Error("io copy", "err", err.Error())
	}
}
