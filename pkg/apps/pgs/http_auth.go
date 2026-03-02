package pgs

import (
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared/router"
)

func validatePassword(expectedPass, actualPass string) bool {
	return expectedPass == actualPass
}

func getCookieName(projectName string) string {
	prefix := "pgs_session_"
	return prefix + projectName
}

// loginFormData holds data for rendering the login form template.
type loginFormData struct {
	ProjectName string
	Error       string
}

// serveLoginFormWithConfig renders and serves the login form using templates.
func serveLoginFormWithConfig(w http.ResponseWriter, r *http.Request, project *db.Project, cfg *PgsConfig, logger *slog.Logger) {
	// Determine error message from query params
	errorMsg := ""
	if r.URL.Query().Get("error") == "invalid" {
		errorMsg = "Invalid password"
	}

	data := loginFormData{
		ProjectName: project.Name,
		Error:       errorMsg,
	}

	w.WriteHeader(http.StatusForbidden)

	ts, err := renderTemplate(cfg, []string{cfg.StaticPath("html/login.page.tmpl")})
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
func handleLogin(w http.ResponseWriter, r *http.Request, cfg *PgsConfig) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		cfg.Logger.Error("failed to parse login form", "err", err.Error())
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}

	projectName := strings.TrimSpace(r.FormValue("project"))
	password := r.FormValue("password")

	if projectName == "" {
		http.Error(w, "missing project name", http.StatusBadRequest)
		return
	}

	if password == "" {
		// Redirect back with error
		http.Redirect(w, r, "/?error=invalid", http.StatusSeeOther)
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
		http.Redirect(w, r, "/?error=invalid", http.StatusSeeOther)
		return
	}

	expiresAt := time.Hour * 24
	cookieName := getCookieName(projectName)
	cookie := &http.Cookie{
		Name:     cookieName,
		Value:    project.ID,
		Path:     "/",
		HttpOnly: true,
		Secure:   cfg.WebProtocol == "https",
		SameSite: http.SameSiteStrictMode,
		Expires:  time.Now().Add(expiresAt),
		MaxAge:   int(expiresAt.Seconds()),
	}
	http.SetCookie(w, cookie)

	redirectPath := r.Header.Get("X-PGS-Referer")
	if redirectPath == "" || !strings.HasPrefix(redirectPath, "/") {
		redirectPath = "/"
	}

	cfg.Logger.Info("successful login", "projectName", projectName, "username", props.Username)
	http.Redirect(w, r, redirectPath, http.StatusSeeOther)
}
