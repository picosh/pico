package shared

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"slices"

	"golang.org/x/crypto/ssh"
)

var fnameRe = regexp.MustCompile(`[-_]+`)
var subdomainRe = regexp.MustCompile(`^[a-z0-9-]+$`)

var KB = 1000
var MB = KB * 1000

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

func KeyForKeyText(pk ssh.PublicKey) string {
	kb := base64.StdEncoding.EncodeToString(pk.Marshal())
	return fmt.Sprintf("%s %s", pk.Type(), kb)
}

func KeyForSha256(pk ssh.PublicKey) string {
	return ssh.FingerprintSHA256(pk)
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
	ext := path.Ext(filename)
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

func BytesToMB(size int) float32 {
	return ((float32(size) / 1000) / 1000)
}

func BytesToGB(size int) float32 {
	return BytesToMB(size) / 1000
}

// https://stackoverflow.com/a/46964105
func StartOfMonth() time.Time {
	now := time.Now()
	firstday := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return firstday
}

func StartOfYear() time.Time {
	now := time.Now()
	return now.AddDate(-1, 0, 0)
}

func AnyToStr(mp map[string]any, key string) string {
	if value, ok := mp[key]; ok {
		if value, ok := value.(string); ok {
			return value
		}
	}
	return ""
}

func AnyToFloat(mp map[string]any, key string) float64 {
	if value, ok := mp[key]; ok {
		if value, ok := value.(float64); ok {
			return value
		}
	}
	return 0
}

func AnyToBool(mp map[string]any, key string) bool {
	if value, ok := mp[key]; ok {
		if value, ok := value.(bool); ok {
			return value
		}
	}
	return false
}
