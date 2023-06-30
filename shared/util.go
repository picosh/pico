package shared

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	pathpkg "path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gliderlabs/ssh"
	"golang.org/x/exp/slices"
)

var fnameRe = regexp.MustCompile(`[-_]+`)

func FilenameToTitle(filename string, title string) string {
	if filename != title {
		return title
	}

	return ToUpper(title)
}

func ToUpper(str string) string {
	pre := fnameRe.ReplaceAllString(str, " ")
	r := []rune(pre)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

func SanitizeFileExt(fname string) string {
	return strings.TrimSuffix(fname, filepath.Ext(fname))
}

func KeyText(s ssh.Session) (string, error) {
	if s.PublicKey() == nil {
		return "", fmt.Errorf("session doesn't have public key")
	}
	kb := base64.StdEncoding.EncodeToString(s.PublicKey().Marshal())
	return fmt.Sprintf("%s %s", s.PublicKey().Type(), kb), nil
}

func GetEnv(key string, defaultVal string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}

	return defaultVal
}

// IsText reports whether a significant prefix of s looks like correct UTF-8;
// that is, if it is likely that s is human-readable text.
func IsText(s string) bool {
	const max = 1024 // at least utf8.UTFMax
	if len(s) > max {
		s = s[0:max]
	}
	for i, c := range s {
		if i+utf8.UTFMax > len(s) {
			// last char may be incomplete - ignore
			break
		}
		if c == 0xFFFD || c < ' ' && c != '\n' && c != '\t' && c != '\f' && c != '\r' {
			// decoding error or control character - not a text file
			return false
		}
	}
	return true
}

func IsExtAllowed(filename string, allowedExt []string) bool {
	ext := pathpkg.Ext(filename)
	return slices.Contains(allowedExt, ext)
}

// IsTextFile reports whether the file has a known extension indicating
// a text file, or if a significant chunk of the specified file looks like
// correct UTF-8; that is, if it is likely that the file contains human-
// readable text.
func IsTextFile(text string) bool {
	num := math.Min(float64(len(text)), 1024)
	return IsText(text[0:int(num)])
}

const solarYearSecs = 31556926

func TimeAgo(t *time.Time) string {
	d := time.Since(*t)
	var metric string
	var amount int
	if d.Seconds() < 60 {
		amount = int(d.Seconds())
		metric = "second"
	} else if d.Minutes() < 60 {
		amount = int(d.Minutes())
		metric = "minute"
	} else if d.Hours() < 24 {
		amount = int(d.Hours())
		metric = "hour"
	} else if d.Seconds() < solarYearSecs {
		amount = int(d.Hours()) / 24
		metric = "day"
	} else {
		amount = int(d.Seconds()) / solarYearSecs
		metric = "year"
	}
	if amount == 1 {
		return fmt.Sprintf("%d %s ago", amount, metric)
	} else {
		return fmt.Sprintf("%d %ss ago", amount, metric)
	}
}

func Shasum(data []byte) string {
	h := sha256.New()
	h.Write(data)
	bs := h.Sum(nil)
	return hex.EncodeToString(bs)
}

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
	}

	return "text/plain"
}
