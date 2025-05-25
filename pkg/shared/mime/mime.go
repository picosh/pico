package mime

import "path/filepath"

func GetMimeType(fpath string) string {
	ext := filepath.Ext(fpath)
	switch ext {
	case ".svg":
		return "image/svg+xml"
	case ".css":
		return "text/css"
	case ".js":
		return "text/javascript"
	case ".ico":
		return "image/x-icon"
	case ".pdf":
		return "application/pdf"
	case ".htm":
	case ".html":
		return "text/html"
	case ".jpeg":
	case ".jpg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".otf":
		return "font/otf"
	case ".woff":
		return "font/woff"
	case ".woff2":
		return "font/woff2"
	case ".ttf":
		return "font/ttf"
	case ".md":
		return "text/markdown; charset=UTF-8"
	case ".map":
	case ".json":
		return "application/json"
	case ".rss":
		return "application/rss+xml"
	case ".atom":
		return "application/atom+xml"
	case ".webmanifest":
		return "application/manifest+json"
	case ".xml":
		return "application/xml"
	case ".xsl":
		return "application/xml"
	case ".avif":
		return "image/avif"
	case ".heif":
		return "image/heif"
	case ".heic":
		return "image/heif"
	case ".opus":
		return "audio/opus"
	case ".wav":
		return "audio/wav"
	case ".mp3":
		return "audio/mpeg"
	case ".mp4":
		return "video/mp4"
	case ".mpeg":
		return "video/mpeg"
	case ".wasm":
		return "application/wasm"
	case ".opml":
		return "text/x-opml"
	case ".eot":
		return "application/vnd.ms-fontobject"
	case ".yaml":
	case ".yml":
		return "text/x-yaml"
	case ".zip":
		return "application/zip"
	case ".rar":
		return "application/vnd.rar"
	case ".txt":
		return "text/plain"
	}

	return "text/plain"
}
