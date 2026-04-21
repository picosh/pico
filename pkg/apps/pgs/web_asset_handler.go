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

	"github.com/picosh/pico/pkg/storage"
)

type ApiAssetHandler struct {
	*WebRouter
	Logger *slog.Logger

	Username       string
	UserID         string
	Subdomain      string
	ProjectDir     string
	Filepath       string
	Bucket         storage.Bucket
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

	redirectsCacheKey := filepath.Join(getSurrogateKey(h.UserID, h.ProjectDir), "_redirects")
	logger.Info("looking for _redirects in lru cache", "key", redirectsCacheKey)
	if cachedRedirects, found := h.RedirectsCache.Get(redirectsCacheKey); found {
		logger.Info("_redirects found in lru cache", "key", redirectsCacheKey)
		redirects = cachedRedirects
	} else {
		logger.Info("_redirects not found in lru cache", "key", redirectsCacheKey)
		redirectFp, redirectInfo, err := h.Cfg.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_redirects"))
		if err == nil {
			if redirectInfo != nil && redirectInfo.Size > h.Cfg.MaxSpecialFileSize {
				_ = redirectFp.Close()
				errMsg := fmt.Sprintf("_redirects file is too large (%d > %d)", redirectInfo.Size, h.Cfg.MaxSpecialFileSize)
				logger.Error(errMsg)
				http.Error(w, errMsg, http.StatusInternalServerError)
				return
			}
			buf := new(strings.Builder)
			lr := io.LimitReader(redirectFp, h.Cfg.MaxSpecialFileSize)
			_, err := io.Copy(buf, lr)
			_ = redirectFp.Close()
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

		h.RedirectsCache.Add(redirectsCacheKey, redirects)
	}

	fpath := h.Filepath
	if isSpecialFile(fpath) {
		logger.Info("special file names are not allowed to be served over http")
		fpath = "404.html"
	}

	routes := calcRoutes(h.ProjectDir, fpath, redirects)

	var contents io.ReadSeekCloser
	assetFilepath := ""
	var info *storage.ObjectInfo
	status := http.StatusOK
	attempts := []string{}
	for _, fp := range routes {
		logger.Info("attemptming to serve route", "route", fp.Filepath, "status", fp.Status, "query", fp.Query)
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
				obj, _, err := h.Cfg.Storage.GetObject(h.Bucket, next)
				if err != nil {
					continue
				}
				_ = obj.Close()
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

			proxy := &httputil.ReverseProxy{
				Rewrite: func(r *httputil.ProxyRequest) {
					r.SetURL(destUrl)
					r.Out.Header.Set("Host", destUrl.Host)
				},
				ModifyResponse: func(resp *http.Response) error {
					resp.Header.Set("cache-control", "no-cache")
					return nil
				},
			}
			proxy.ServeHTTP(w, r)
			return
		}

		fpath := fp.Filepath
		attempts = append(attempts, fpath)
		logger = logger.With("object", fpath)

		imgproxy := storage.NewImgProxy(fpath, h.ImgProcessOpts)
		err = imgproxy.CanServe()
		if err == nil {
			logger.Info("serving image with imgproxy")
			imgproxy.ServeHTTP(w, r)
			return
		} else {
			var c io.ReadSeekCloser
			c, info, err = h.Cfg.Storage.GetObject(
				h.Bucket,
				fpath,
			)
			if err != nil {
				logger.Error("serving object", "err", err)
			} else {
				contents = c
				assetFilepath = fp.Filepath
				status = fp.Status
				break
			}
		}
	}

	if assetFilepath == "" {
		if shouldGenerateListing(h.Cfg.Storage, h.Bucket, h.ProjectDir, "/"+fpath) {
			logger.Info(
				"generating directory listing",
				"path", fpath,
			)
			dirPath := h.ProjectDir + "/" + fpath
			entries, err := h.Cfg.Storage.ListObjects(h.Bucket, dirPath, false)
			if err == nil {
				requestPath := "/" + fpath
				if !strings.HasSuffix(requestPath, "/") {
					requestPath += "/"
				}

				html := generateDirectoryHTML(requestPath, entries)
				w.Header().Set("content-type", "text/html")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(html))
				return
			}
		}

		logger.Info(
			"asset not found in bucket",
			"routes", strings.Join(attempts, ", "),
			"status", http.StatusNotFound,
		)
		http.Error(w, "404 not found", http.StatusNotFound)
		return
	}
	defer func() {
		_ = contents.Close()
	}()

	var headers []*HeaderRule

	headersCacheKey := filepath.Join(getSurrogateKey(h.UserID, h.ProjectDir), "_headers")
	logger.Info("looking for _headers in lru cache", "key", headersCacheKey)
	if cachedHeaders, found := h.HeadersCache.Get(headersCacheKey); found {
		logger.Info("_headers found in lru", "key", headersCacheKey)
		headers = cachedHeaders
	} else {
		logger.Info("_headers not found in lru cache", "key", headersCacheKey)
		headersFp, headersInfo, err := h.Cfg.Storage.GetObject(h.Bucket, filepath.Join(h.ProjectDir, "_headers"))
		if err == nil {
			if headersInfo != nil && headersInfo.Size > h.Cfg.MaxSpecialFileSize {
				_ = headersFp.Close()
				errMsg := fmt.Sprintf("_headers file is too large (%d > %d)", headersInfo.Size, h.Cfg.MaxSpecialFileSize)
				logger.Error(errMsg)
				http.Error(w, errMsg, http.StatusInternalServerError)
				return
			}
			buf := new(strings.Builder)
			lr := io.LimitReader(headersFp, h.Cfg.MaxSpecialFileSize)
			_, err := io.Copy(buf, lr)
			_ = headersFp.Close()
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

		h.HeadersCache.Add(headersCacheKey, headers)
	}

	userHeaders := []*HeaderLine{}
	for _, headerRule := range headers {
		rr := regexp.MustCompile(headerRule.Path)
		match := rr.FindStringSubmatch(assetFilepath)
		if len(match) > 0 {
			userHeaders = headerRule.Headers
		}
	}

	contentType := ""
	if info != nil {
		contentType = info.ContentType
		if info.Size != 0 {
			w.Header().Add("content-length", strconv.Itoa(int(info.Size)))
		}
		if info.ETag != "" {
			// Minio SDK trims off the mandatory quotes (RFC 7232 § 2.3)
			w.Header().Add("etag", fmt.Sprintf("\"%s\"", info.ETag))
		}

		if !info.LastModified.IsZero() {
			w.Header().Add("last-modified", info.LastModified.UTC().Format(http.TimeFormat))
		}
	}

	// Default cache:
	//   short TTL for private caches (browser),
	//   long TTL for shared cache (our cache),
	//   then must revalidate using ETag
	cc := fmt.Sprintf(
		"max-age=60, s-maxage=%0.f, must-revalidate",
		h.Cfg.CacheTTL.Seconds(),
	)
	w.Header().Set("cache-control", cc)

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
	if status != http.StatusOK {
		w.WriteHeader(status)
		_, err := io.Copy(w, contents)
		if err != nil {
			logger.Error("io copy", "err", err.Error())
		}
		return
	}
	http.ServeContent(w, r, assetFilepath, info.LastModified.UTC(), contents)
}
