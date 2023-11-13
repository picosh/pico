package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
	}

	return "text/plain"
}

func HandleProxy(dataURL string, ratio *Ratio, original bool, useProxy bool) (io.ReadCloser, string, error) {
	imgProxyURL := os.Getenv("IMGPROXY_URL")
	imgProxySalt := os.Getenv("IMGPROXY_SALT")
	imgProxyKey := os.Getenv("IMGPROXY_SALT")

	signature := "_"
	processOpts := "q:80"

	if ratio != nil {
		processOpts += fmt.Sprintf("/s:%d:%d", ratio.Width, ratio.Height)
	}

	fileType := ".webp"
	if original {
		fileType = ""
		processOpts = "raw:1"
	}

	processPath := fmt.Sprintf("/%s/%s%s", processOpts, base64.StdEncoding.EncodeToString([]byte(dataURL)), fileType)

	if imgProxySalt != "" && imgProxyKey != "" {
		keyBin, err := hex.DecodeString(imgProxyKey)
		if err != nil {
			return nil, "", err
		}

		saltBin, err := hex.DecodeString(imgProxySalt)
		if err != nil {
			return nil, "", err
		}

		mac := hmac.New(sha256.New, keyBin)
		mac.Write(saltBin)
		mac.Write([]byte(processPath))
		signature = base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	}

	proxyAddress := fmt.Sprintf("%s/%s%s", imgProxyURL, signature, processPath)

	res, err := http.Get(proxyAddress)
	if err != nil {
		return nil, "", err
	}

	return res.Body, res.Header.Get("Content-Type"), nil
}
