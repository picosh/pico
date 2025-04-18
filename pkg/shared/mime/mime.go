package mime

import "path/filepath"

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
