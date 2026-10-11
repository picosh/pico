package router

import (
	"html/template"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
)

type SubdomainProps struct {
	ProjectName string
	Username    string
}

func GetProjectFromSubdomain(subdomain string) (*SubdomainProps, error) {
	props := &SubdomainProps{}
	strs := strings.SplitN(subdomain, "-", 2)
	props.Username = strs[0]
	if len(strs) == 2 {
		props.ProjectName = strs[1]
	} else {
		props.ProjectName = props.Username
	}
	return props, nil
}

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

// CheckHandler reports whether a custom domain points at a user's site on
// domain, through its TXT record for txtPrefix.
func CheckHandler(domain, txtPrefix string, users interface {
	FindUserByName(name string) (*db.User, error)
}) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		hostDomain := r.URL.Query().Get("domain")
		appDomain := strings.Split(domain, ":")[0]

		if !strings.Contains(hostDomain, appDomain) {
			subdomain := GetCustomDomain(hostDomain, txtPrefix)
			if subdomain != "" {
				u, err := users.FindUserByName(subdomain)
				if u != nil && err == nil {
					w.WriteHeader(http.StatusOK)
					return
				}
			}
		}

		w.WriteHeader(http.StatusNotFound)
	}
}

// GetUsernameFromRequest is the user whose site a request is for: the
// subdomain, or the path's {user} on the main domain.
func GetUsernameFromRequest(r *http.Request) string {
	if subdomain := GetSubdomain(r); subdomain != "" {
		return subdomain
	}
	return r.PathValue("user")
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

// RenderTemplate parses templates with the layout and footers in an app's
// dir.
func RenderTemplate(dir string, templates []string) (*template.Template, error) {
	files := make([]string, len(templates))
	copy(files, templates)
	files = append(
		files,
		filepath.Join(dir, "html/footer.partial.tmpl"),
		filepath.Join(dir, "html/marketing-footer.partial.tmpl"),
		filepath.Join(dir, "html/base.layout.tmpl"),
	)

	ts, err := template.New("base").Funcs(FuncMap).ParseFiles(files...)
	if err != nil {
		return nil, err
	}
	return ts, nil
}

// Static serves an app's files from Dir/public and its pages from Dir/html.
type Static struct {
	Dir    string
	Logger *slog.Logger
	Site   shared.SitePageData
}

// File serves the file name in Dir/public.
func (s Static) File(name string, contentType string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		contents, err := os.ReadFile(filepath.Join(s.Dir, "public", name))
		if err != nil {
			s.Logger.Error("could not read file", "fname", name, "err", err.Error())
			http.Error(w, "file not found", http.StatusNotFound)
			return
		}

		w.Header().Add("Content-Type", contentType)

		_, err = w.Write(contents)
		if err != nil {
			s.Logger.Error("could not write http response", "file", name, "err", err.Error())
		}
	}
}

// Page renders the template fname in Dir with the site's data.
func (s Static) Page(fname string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ts, err := RenderTemplate(s.Dir, []string{filepath.Join(s.Dir, fname)})
		if err != nil {
			s.Logger.Error("could not render template", "fname", fname, "err", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		err = ts.Execute(w, shared.PageData{Site: s.Site})
		if err != nil {
			s.Logger.Error("could not execute template", "fname", fname, "err", err.Error())
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	}
}
