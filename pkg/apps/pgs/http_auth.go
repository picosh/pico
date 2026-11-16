package pgs

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared/router"
	"golang.org/x/crypto/bcrypt"
)

const sessionTTL = 24 * time.Hour

const maxLoginFormSize = 16 * 1024

func hashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", err
	}
	return string(hash), nil
}

func validatePassword(stored, actual string) bool {
	return bcrypt.CompareHashAndPassword([]byte(stored), []byte(actual)) == nil
}

// hashBasicCredentials turns http-basic entries of "user:password" into
// "user:hash".
func hashBasicCredentials(entries []string) ([]string, error) {
	if len(entries) == 0 {
		return nil, fmt.Errorf("http-basic needs at least one user: --acl <user>:<password>")
	}
	seen := map[string]bool{}
	hashed := make([]string, 0, len(entries))
	for _, entry := range entries {
		user, password, ok := strings.Cut(entry, ":")
		if !ok || user == "" || password == "" {
			return nil, fmt.Errorf("http-basic entries look like <user>:<password>")
		}
		if seen[user] {
			return nil, fmt.Errorf("http-basic user %s is listed twice", user)
		}
		seen[user] = true
		hash, err := hashPassword(password)
		if err != nil {
			return nil, fmt.Errorf("http-basic user %s: %w", user, err)
		}
		hashed = append(hashed, user+":"+hash)
	}
	return hashed, nil
}

// dummyHash is checked for an unknown basic auth user, so the time taken
// doesn't reveal which users exist.
var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("pgs"), bcrypt.DefaultCost)

// validBasicAuth checks a request's basic auth credentials against an
// http-basic project.
func validBasicAuth(r *http.Request, project *db.Project) bool {
	user, password, ok := r.BasicAuth()
	if !ok || user == "" {
		return false
	}
	hash, found := string(dummyHash), false
	for _, entry := range project.Acl.Data {
		if u, h, ok := strings.Cut(entry, ":"); ok && u == user {
			hash, found = h, true
			break
		}
	}
	match := validatePassword(hash, password)
	return found && match
}

// basicAuthChallenge asks the browser for http-basic credentials.
func basicAuthChallenge(w http.ResponseWriter, project *db.Project) {
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm="%s", charset="UTF-8"`, project.Name))
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}

// setSessionCookie logs the visitor in to project for sessionTTL.
func setSessionCookie(w http.ResponseWriter, project *db.Project, secure bool) {
	expires := time.Now().Add(sessionTTL)
	http.SetCookie(w, &http.Cookie{
		Name:     getCookieName(project.Name),
		Value:    sessionToken(project, expires),
		Path:     "/",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		Expires:  expires,
		MaxAge:   int(sessionTTL.Seconds()),
	})
}

// sessionToken signs a session for a project until expires. The stored
// credentials are the key, so changing any of them ends every session.
func sessionToken(project *db.Project, expires time.Time) string {
	exp := strconv.FormatInt(expires.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(strings.Join(project.Acl.Data, "\n")))
	mac.Write([]byte(project.ID + "|" + exp))
	return exp + "." + hex.EncodeToString(mac.Sum(nil))
}

// hasValidSession checks every session cookie for the project, since another
// site under the same domain can set one with the same name.
func hasValidSession(r *http.Request, project *db.Project) bool {
	name := getCookieName(project.Name)
	for _, cookie := range r.Cookies() {
		if cookie.Name == name && validSession(project, cookie.Value) {
			return true
		}
	}
	return false
}

func validSession(project *db.Project, token string) bool {
	if len(project.Acl.Data) == 0 {
		return false
	}
	exp, _, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	unix, err := strconv.ParseInt(exp, 10, 64)
	if err != nil || time.Now().Unix() >= unix {
		return false
	}
	return hmac.Equal([]byte(token), []byte(sessionToken(project, time.Unix(unix, 0))))
}

func getCookieName(projectName string) string {
	prefix := "pgs_session_"
	return prefix + projectName
}

// loginFormData holds data for rendering the login form template.
type loginFormData struct {
	ProjectName string
	Error       string
	Redirect    string
}

// serveLoginFormWithConfig renders and serves the login form using templates.
func serveLoginFormWithConfig(w http.ResponseWriter, r *http.Request, project *db.Project, cfg *Config, logger *slog.Logger) {
	// Determine error message from query params
	errorMsg := ""
	if r.URL.Query().Get("error") == "invalid" {
		errorMsg = "Invalid password"
	}

	data := loginFormData{
		ProjectName: project.Name,
		Error:       errorMsg,
		Redirect:    r.URL.RequestURI(),
	}

	renderLoginForm(w, data, cfg, logger)
}

func renderLoginForm(w http.ResponseWriter, data loginFormData, cfg *Config, logger *slog.Logger) {
	w.WriteHeader(http.StatusForbidden)

	ts, err := router.RenderTemplate(staticDir, []string{cfg.StaticPath("html/login.page.tmpl")})
	if err != nil {
		logger.Error("could not render login template", "err", err.Error())
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	err = ts.Execute(w, data)
	if err != nil {
		logger.Error("could not execute login template", "err", err.Error())
		http.Error(w, "Server error", http.StatusInternalServerError)
	}
}

// handleLogin processes the login form submission.
func handleLogin(w http.ResponseWriter, r *http.Request, cfg *Config) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxLoginFormSize)
	err := r.ParseForm()
	if err != nil {
		cfg.Logger.Error("failed to parse login form", "err", err.Error())
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	projectName := strings.TrimSpace(r.FormValue("project"))
	password := r.FormValue("password")
	redirectPath := r.PostForm.Get("redirect")
	if redirectPath == "" {
		redirectPath = r.Header.Get("X-PGS-Referer")
	}
	redirectURL, err := url.Parse(redirectPath)
	if err != nil || !strings.HasPrefix(redirectPath, "/") || strings.HasPrefix(redirectPath, "//") || strings.Contains(redirectURL.Path, `\`) {
		redirectPath = "/"
	}

	invalidPassword := func() {
		renderLoginForm(w, loginFormData{
			ProjectName: projectName,
			Error:       "Invalid password",
			Redirect:    redirectPath,
		}, cfg, cfg.Logger)
	}

	if projectName == "" {
		http.Error(w, "missing project name", http.StatusBadRequest)
		return
	}

	if password == "" {
		invalidPassword()
		return
	}

	subdomain := router.GetSubdomainFromRequest(r, cfg.Domain, cfg.TxtPrefix)
	props, err := router.GetProjectFromSubdomain(subdomain)
	if err != nil {
		cfg.Logger.Error("could not get project from subdomain", "subdomain", subdomain, "err", err)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	user, err := cfg.DB.FindUserByName(props.Username)
	if err != nil {
		cfg.Logger.Error("user not found", "username", props.Username)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)

	project, err := cfg.DB.FindProjectByName(user.ID, projectName)
	if err != nil {
		cfg.Logger.Error("project not found", "username", props.Username, "projectName", projectName)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if project.Acl.Type != "http-pass" {
		cfg.Logger.Error("project is not password protected", "projectName", projectName)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if len(project.Acl.Data) == 0 {
		cfg.Logger.Error("password-protected project has no password hash", "projectName", projectName)
		http.Error(w, "server error", http.StatusInternalServerError)
		return
	}

	storedPass := project.Acl.Data[0]
	if !validatePassword(storedPass, password) {
		cfg.Logger.Info("invalid password attempt", "projectName", projectName)
		invalidPassword()
		return
	}

	setSessionCookie(w, project, cfg.WebProtocol == "https")

	cfg.Logger.Info("successful login", "projectName", projectName, "username", props.Username)
	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}
