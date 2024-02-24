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

	"slices"

	"github.com/charmbracelet/ssh"
)

var fnameRe = regexp.MustCompile(`[-_]+`)
var subdomainRe = regexp.MustCompile(`^[a-z0-9-]+$`)

var KB = 1024
var MB = KB * 1024
var GB = MB * 1024

func IsValidSubdomain(subd string) bool {
	return subdomainRe.MatchString(subd)
}

func FilenameToTitle(filename string, title string) string {
	if filename != title {
		return title
	}

	return ToUpper(title)
}

func ToUpper(str string) string {
	pre := fnameRe.ReplaceAllString(str, " ")

	r := []rune(pre)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}

	return string(r)
}

func SanitizeFileExt(fname string) string {
	return strings.TrimSuffix(fname, filepath.Ext(fname))
}

func KeyText(s ssh.Session) (string, error) {
	if s.PublicKey() == nil {
		return "", fmt.Errorf("Session doesn't have public key")
	}
	return KeyForKeyText(s.PublicKey())
}

func KeyForKeyText(pk ssh.PublicKey) (string, error) {
	kb := base64.StdEncoding.EncodeToString(pk.Marshal())
	return fmt.Sprintf("%s %s", pk.Type(), kb), nil
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

func BytesToGB(size int) float32 {
	return (((float32(size) / 1024) / 1024) / 1024)
}
