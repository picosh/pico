package shared

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"os"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
)

func CorsHeaders(headers http.Header) {
	headers.Add("Access-Control-Allow-Origin", "*")
	headers.Add("Vary", "Origin")
	headers.Add("Vary", "Access-Control-Request-Method")
	headers.Add("Vary", "Access-Control-Request-Headers")
	headers.Add("Access-Control-Allow-Headers", "Content-Type, Accept")
	headers.Add("Access-Control-Allow-Methods", "GET, POST, OPTIONS, PUT, PATCH, DELETE")
}

func UnauthorizedHandler(w http.ResponseWriter, r *http.Request) {
	http.Error(w, "You do not have access to this site", http.StatusUnauthorized)
}

type errPayload struct {
	Message string `json:"message"`
}

func JSONError(w http.ResponseWriter, msg string, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(errPayload{Message: msg})
}

type UserApi struct {
	*db.User
	Fingerprint string `json:"fingerprint"`
}

func NewUserApi(user *db.User, pubkey ssh.PublicKey) *UserApi {
	return &UserApi{
		User:        user,
		Fingerprint: utils.KeyForSha256(pubkey),
	}
}

func CheckHandler(w http.ResponseWriter, r *http.Request) {
	dbpool := GetDB(r)
	cfg := GetCfg(r)

	if cfg.IsCustomdomains() {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(cfg.Domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := GetCustomDomain(hostDomain, cfg.Space)
			if subdomain != "" {
				u, err := dbpool.FindUserForName(subdomain)
				if u != nil && err == nil {
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}
	}

	w.WriteHeader(http.StatusNotFound)
}

func GetUsernameFromRequest(r *http.Request) string {
	subdomain := GetSubdomain(r)
	cfg := GetCfg(r)

	if !cfg.IsSubdomains() || subdomain == "" {
		return GetField(r, 0)
	}
	return subdomain
}

func ServeFile(file string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)

		contents, err := os.ReadFile(cfg.StaticPath(fmt.Sprintf("public/%s", file)))
		if err != nil {
			logger.Error(err.Error())
			http.Error(w, "file not found", 404)
		}

		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

func minus(a, b int) int {
	return a - b
}

func intRange(start, end int) []int {
	n := end - start + 1
	result := make([]int, n)
	for i := 0; i < n; i++ {
		result[i] = start + i
	}
	return result
}

var FuncMap = template.FuncMap{
	"minus":    minus,
	"intRange": intRange,
}

func RenderTemplate(cfg *ConfigSite, templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		cfg.StaticPath("html/footer.partial.tmpl"),
		cfg.StaticPath("html/marketing-footer.partial.tmpl"),
		cfg.StaticPath("html/base.layout.tmpl"),
	)

	ts, err := template.New("base").Funcs(FuncMap).ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

func CreatePageHandler(fname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		logger := GetLogger(r)
		cfg := GetCfg(r)
		ts, err := RenderTemplate(cfg, []string{cfg.StaticPath(fname)})

		if err != nil {
			logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data := PageData{
			Site: *cfg.GetSiteData(),
		}
		err = ts.Execute(w, data)
		if err != nil {
			logger.Error(err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
