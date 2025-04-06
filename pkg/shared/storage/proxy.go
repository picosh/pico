package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/pobj/storage"
)

func GetMimeType(fpath string) string {
	ext := filepath.Ext(fpath)
	if ext == ".svg" {
		return "image/svg+xml"
	} else if ext == ".css" {
		return "text/css"
	} else if ext == ".js" {
		return "text/javascript"
	} else if ext == ".ico" {
		return "image/x-icon"
	} else if ext == ".pdf" {
		return "application/pdf"
	} else if ext == ".html" || ext == ".htm" {
		return "text/html"
	} else if ext == ".jpg" || ext == ".jpeg" {
		return "image/jpeg"
	} else if ext == ".png" {
		return "image/png"
	} else if ext == ".gif" {
		return "image/gif"
	} else if ext == ".webp" {
		return "image/webp"
	} else if ext == ".otf" {
		return "font/otf"
	} else if ext == ".woff" {
		return "font/woff"
	} else if ext == ".woff2" {
		return "font/woff2"
	} else if ext == ".ttf" {
		return "font/ttf"
	} else if ext == ".md" {
		return "text/markdown; charset=UTF-8"
	} else if ext == ".json" || ext == ".map" {
		return "application/json"
	} else if ext == ".rss" {
		return "application/rss+xml"
	} else if ext == ".atom" {
		return "application/atom+xml"
	} else if ext == ".webmanifest" {
		return "application/manifest+json"
	} else if ext == ".xml" {
		return "application/xml"
	} else if ext == ".xsl" {
		return "application/xml"
	} else if ext == ".avif" {
		return "image/avif"
	} else if ext == ".heif" {
		return "image/heif"
	} else if ext == ".heic" {
		return "image/heif"
	} else if ext == ".opus" {
		return "audio/opus"
	} else if ext == ".wav" {
		return "audio/wav"
	} else if ext == ".mp3" {
		return "audio/mpeg"
	} else if ext == ".mp4" {
		return "video/mp4"
	} else if ext == ".mpeg" {
		return "video/mpeg"
	} else if ext == ".wasm" {
		return "application/wasm"
	} else if ext == ".opml" {
		return "text/x-opml"
	} else if ext == ".eot" {
		return "application/vnd.ms-fontobject"
	} else if ext == ".yml" || ext == ".yaml" {
		return "text/x-yaml"
	} else if ext == ".zip" {
		return "application/zip"
	} else if ext == ".rar" {
		return "application/vnd.rar"
	} else if ext == ".txt" {
		return "text/plain"
	}

	return "text/plain"
}

func UriToImgProcessOpts(uri string) (*ImgProcessOpts, error) {
	opts := &ImgProcessOpts{}
	parts := strings.Split(uri, "/")

	for _, part := range parts {
		ratio, err := GetRatio(part)
		if err != nil {
			return opts, err
		}

		if ratio != nil {
			opts.Ratio = ratio
		}

		if strings.HasPrefix(part, "s:") {
			segs := strings.SplitN(part, ":", 4)
			r := &Ratio{}
			for idx, sg := range segs {
				if sg == "" {
					continue
				}
				if idx == 1 {
					r.Width, err = strconv.Atoi(sg)
					if err != nil {
						return opts, err
					}
				} else if idx == 2 {
					r.Height, err = strconv.Atoi(sg)
					if err != nil {
						return opts, err
					}
				}
			}
			opts.Ratio = r
		}

		if strings.HasPrefix(part, "q:") {
			quality := strings.Replace(part, "q:", "", 1)
			opts.Quality, err = strconv.Atoi(quality)
			if err != nil {
				return opts, err
			}
		}

		if strings.HasPrefix(part, "rt:") {
			angle := strings.Replace(part, "rt:", "", 1)
			opts.Rotate, err = strconv.Atoi(angle)
			if err != nil {
				return opts, err
			}
		}

		if strings.HasPrefix(part, "ext:") {
			ext := strings.Replace(part, "ext:", "", 1)
			opts.Ext = ext
			if err != nil {
				return opts, err
			}
		}
	}

	return opts, nil
}

type ImgProcessOpts struct {
	Quality int
	Ratio   *Ratio
	Rotate  int
	Ext     string
	NoRaw   bool
}

func (img *ImgProcessOpts) String() string {
	processOpts := ""

	// https://docs.imgproxy.net/usage/processing#quality
	if img.Quality != 0 {
		processOpts = fmt.Sprintf("%s/q:%d", processOpts, img.Quality)
	}

	// https://docs.imgproxy.net/usage/processing#size
	if img.Ratio != nil {
		processOpts = fmt.Sprintf(
			"%s/size:%d:%d",
			processOpts,
			img.Ratio.Width,
			img.Ratio.Height,
		)
	}

	// https://docs.imgproxy.net/usage/processing#rotate
	// Only 0, 90, 180, 270, etc., degree angles are supported.
	if img.Rotate != 0 {
		rot := img.Rotate
		if rot == 90 || rot == 180 || rot == 280 {
			processOpts = fmt.Sprintf(
				"%s/rotate:%d",
				processOpts,
				rot,
			)
		}
	}

	// https://docs.imgproxy.net/usage/processing#format
	if img.Ext != "" {
		processOpts = fmt.Sprintf("%s/ext:%s", processOpts, img.Ext)
	}

	if processOpts == "" && !img.NoRaw {
		processOpts = fmt.Sprintf("%s/raw:true", processOpts)
	}

	return processOpts
}

func HandleProxy(logger *slog.Logger, dataURL string, opts *ImgProcessOpts) (io.ReadCloser, *storage.ObjectInfo, error) {
	imgProxyURL := os.Getenv("IMGPROXY_URL")
	imgProxySalt := os.Getenv("IMGPROXY_SALT")
	imgProxyKey := os.Getenv("IMGPROXY_KEY")

	signature := "_"

	processOpts := opts.String()

	processPath := fmt.Sprintf("%s/%s", processOpts, base64.StdEncoding.EncodeToString([]byte(dataURL)))

	if imgProxySalt != "" && imgProxyKey != "" {
		keyBin, err := hex.DecodeString(imgProxyKey)
		if err != nil {
			return nil, nil, err
		}

		saltBin, err := hex.DecodeString(imgProxySalt)
		if err != nil {
			return nil, nil, err
		}

		mac := hmac.New(sha256.New, keyBin)
		mac.Write(saltBin)
		mac.Write([]byte(processPath))
		signature = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}
	proxyAddress := fmt.Sprintf("%s/%s%s", imgProxyURL, signature, processPath)

	res, err := http.Get(proxyAddress)
	if err != nil {
		return nil, nil, err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return nil, nil, fmt.Errorf("imgproxy returned %s", res.Status)
	}
	lastModified := res.Header.Get("Last-Modified")
	parsedTime, err := time.Parse(time.RFC1123, lastModified)
	if err != nil {
		logger.Error("decoding last-modified", "err", err)
	}
	info := &storage.ObjectInfo{
		Size:     res.ContentLength,
		ETag:     trimEtag(res.Header.Get("etag")),
		Metadata: res.Header,
	}
	if !parsedTime.IsZero() {
		info.LastModified = parsedTime
	}

	return res.Body, info, nil
}

// trimEtag removes quotes from the etag header, which matches the behavior of the minio-go SDK.
func trimEtag(etag string) string {
	etag = strings.TrimPrefix(etag, "\"")
	return strings.TrimSuffix(etag, "\"")
}
