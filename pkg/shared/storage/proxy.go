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
	"strconv"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/pobj/storage"
)

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
				switch idx {
				case 1:
					r.Width, err = strconv.Atoi(sg)
					if err != nil {
						return opts, err
					}
				case 2:
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

func HandleProxy(r *http.Request, logger *slog.Logger, dataURL string, opts *ImgProcessOpts) (io.ReadCloser, *storage.ObjectInfo, error) {
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

	req, err := http.NewRequest(http.MethodGet, proxyAddress, nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("accept", r.Header.Get("accept"))
	req.Header.Set("accept-encoding", r.Header.Get("accept-encoding"))
	req.Header.Set("accept-language", r.Header.Get("accept-language"))
	req.Header.Set("content-type", r.Header.Get("content-type"))
	fmt.Println("HEADERS", req.Header)
	res, err := http.DefaultClient.Do(req)
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
