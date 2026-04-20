package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/picosh/pico/pkg/shared/mime"
)

type ImgProxy struct {
	url      string
	salt     string
	key      string
	filepath string
	opts     *ImgProcessOpts
}

func NewImgProxy(fp string, opts *ImgProcessOpts) *ImgProxy {
	return &ImgProxy{
		url:      os.Getenv("IMGPROXY_URL"),
		salt:     os.Getenv("IMGPROXY_SALT"),
		key:      os.Getenv("IMGPROXY_KEY"),
		filepath: fp,
		opts:     opts,
	}
}

func (img *ImgProxy) CanServe() error {
	if img.url == "" {
		return fmt.Errorf("no imgproxy url provided")
	}
	if img.opts == nil {
		return fmt.Errorf("no image options provided")
	}
	mimeType := mime.GetMimeType(img.filepath)
	if !strings.HasPrefix(mimeType, "image/") {
		return fmt.Errorf("file mimetype not an image")
	}
	return nil
}

func (img *ImgProxy) GetSig(ppath []byte) string {
	signature := "_"
	imgProxySalt := img.salt
	imgProxyKey := img.key
	if imgProxySalt == "" || imgProxyKey == "" {
		return signature
	}

	keyBin, err := hex.DecodeString(imgProxyKey)
	if err != nil {
		return signature
	}

	saltBin, err := hex.DecodeString(imgProxySalt)
	if err != nil {
		return signature
	}

	mac := hmac.New(sha256.New, keyBin)
	mac.Write(saltBin)
	mac.Write(ppath)
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (img *ImgProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	dataURL := fmt.Sprintf("local:///%s", img.filepath)
	imgProxyURL := img.url
	processOpts := img.opts.String()
	processPath := fmt.Sprintf(
		"%s/%s",
		processOpts,
		base64.StdEncoding.EncodeToString([]byte(dataURL)),
	)
	sig := img.GetSig([]byte(processPath))

	rurl := fmt.Sprintf("%s/%s%s", imgProxyURL, sig, processPath)
	destUrl, err := url.Parse(rurl)
	if err != nil {
		msg := fmt.Sprintf("could not parse url: %s", rurl)
		http.Error(w, msg, http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(destUrl)
	proxy.ServeHTTP(w, r)
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
