package pastes

import (
	"html/template"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/shared/router"
)

// renderCSP lets a rendered paste show images, styles and fonts, but run no
// scripts and submit no forms. sandbox also gives the page an opaque origin,
// so it can't reach anything of the pastes site. Browsers enforce it
// alongside the CSP Caddy adds.
const renderCSP = "sandbox; default-src 'none'; img-src * data:; style-src * 'unsafe-inline'; font-src * data:; media-src *; form-action 'none'; base-uri 'none'"

// renderKind is how a paste's rendered view shows it: "markdown", "html", or
// "" when it has no rendered view.
func renderKind(filename string) string {
	switch strings.ToLower(filepath.Ext(filename)) {
	case ".md", ".markdown":
		return "markdown"
	case ".html", ".htm":
		return "html"
	}
	return ""
}

type renderPageData struct {
	Title    string
	Contents template.HTML
}

// postHandlerRender serves a markdown or html paste as a page.
func (web *webRouter) postHandlerRender(w http.ResponseWriter, r *http.Request) {
	username := router.GetUsernameFromRequest(r)
	cfg := web.cfg
	slug, _ := url.PathUnescape(r.PathValue("slug"))
	logger := cfg.Logger.With("user", username, "slug", slug)

	user, err := web.db.FindUserByName(username)
	if err != nil {
		logger.Info("user not found")
		http.Error(w, "user not found", http.StatusNotFound)
		return
	}
	router.SetUser(r, user.Name)
	logger = shared.LoggerWithUser(logger, user)

	post, err := web.db.FindPostWithSlug(slug, user.ID, cfg.Space)
	if err != nil || isExpired(post) {
		logger.Info("paste not found")
		http.Error(w, "paste not found", http.StatusNotFound)
		return
	}
	kind := renderKind(post.Filename)
	if kind == "" {
		http.Error(w, "only markdown and html pastes can be rendered", http.StatusNotFound)
		return
	}

	header := w.Header()
	header.Set("Content-Security-Policy", renderCSP)
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Robots-Tag", "noindex")
	header.Set("Content-Type", "text/html; charset=utf-8")

	if kind == "html" {
		if _, err := w.Write([]byte(post.Text)); err != nil {
			logger.Error("write error", "err", err)
		}
		return
	}

	parsed, err := shared.ParseText(post.Text)
	if err != nil {
		logger.Error("could not parse markdown", "err", err)
		http.Error(w, "could not render paste", http.StatusInternalServerError)
		return
	}
	// ParseText takes the title out of the body, as prose shows it apart.
	title, contents := post.Filename, parsed.Html
	if parsed.Title != "" {
		title = parsed.Title
		contents = "<h1>" + template.HTMLEscapeString(parsed.Title) + "</h1>\n" + contents
	}
	ts, err := router.RenderTemplate(staticDir, []string{cfg.StaticPath("html/render.page.tmpl")})
	if err != nil {
		logger.Error("could not load template", "err", err)
		http.Error(w, "could not render paste", http.StatusInternalServerError)
		return
	}
	// parsed.Html has been sanitized, so no script survives even without the
	// CSP.
	if err := ts.Execute(w, renderPageData{Title: title, Contents: template.HTML(contents)}); err != nil {
		logger.Error("could not execute template", "err", err)
	}
}
