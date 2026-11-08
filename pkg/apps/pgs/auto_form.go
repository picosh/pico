package pgs

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/feeds"
	"github.com/picosh/pico/pkg/shared/router"
)

func handleAutoForm(w http.ResponseWriter, r *http.Request, cfg *Config) {
	formName := r.PathValue("fname")
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	err := r.ParseForm()
	if err != nil {
		cfg.Logger.Error("failed to parse auto form", "err", err)
		http.Error(w, "failed to parse auto form", http.StatusBadRequest)
		return
	}

	formValues := make(map[string]interface{})
	for key, values := range r.PostForm {
		if len(values) == 1 {
			formValues[key] = values[0]
		} else if len(values) > 1 {
			formValues[key] = values
		}
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

	ff, err := cfg.DB.FindFeature(user.ID, "plus")
	if err != nil || (ff != nil && !ff.IsValid()) {
		cfg.Logger.Error("pico+ required for auto-forms", "username", props.Username)
		http.Error(w, "pico+ required for auto-forms", http.StatusPaymentRequired)
		return
	}

	err = cfg.DB.InsertFormEntry(user.ID, formName, formValues)
	if err != nil {
		cfg.Logger.Error("failed to save form data", "err", err)
		http.Error(w, "failed to save form data", http.StatusInternalServerError)
		return
	}

	serveAutoFormSubmitted(w, r, cfg)
}

type FormData struct {
	Error string
}

func serveAutoFormSubmitted(w http.ResponseWriter, r *http.Request, cfg *Config) {
	errorMsg := r.URL.Query().Get("error")
	data := loginFormData{
		Error: errorMsg,
	}

	w.WriteHeader(http.StatusUnprocessableEntity)

	ts, err := router.RenderTemplate(staticDir, []string{cfg.StaticPath("html/auto_form.page.tmpl")})
	if err != nil {
		cfg.Logger.Error("could not render auto form template", "err", err.Error())
		http.Error(w, "Server error", http.StatusInternalServerError)
		return
	}

	err = ts.Execute(w, data)
	if err != nil {
		cfg.Logger.Error("could not execute login template", "err", err.Error())
		http.Error(w, "Server error", http.StatusInternalServerError)
	}
}

func handleAutoFormRss(w http.ResponseWriter, r *http.Request, cfg *Config) {
	logger := cfg.Logger
	token := r.PathValue("token")
	user, err := cfg.DB.FindUserByToken(token)
	if err != nil {
		cfg.Logger.Error("invalid token", "err", err.Error())
		http.Error(w, "invalid token", http.StatusUnprocessableEntity)
		return
	}

	formNames, err := cfg.DB.FindFormNamesByUser(user.ID)
	if err != nil {
		cfg.Logger.Error("could not get form names", "err", err.Error())
		http.Error(w, "could not get form names", http.StatusInternalServerError)
		return
	}

	feed := &feeds.Feed{
		Title:       fmt.Sprintf("%s form entries", user.Name),
		Link:        &feeds.Link{Href: "https://pgs.sh"},
		Description: fmt.Sprintf("%s form entries", user.Name),
		Author:      &feeds.Author{Name: user.Name},
		Created:     time.Now(),
	}

	var feedItems []*feeds.Item
	for _, name := range formNames {
		entries, err := cfg.DB.FindFormEntriesByUserAndName(user.ID, name)
		if err != nil {
			continue
		}

		for _, entry := range entries {
			content := "<ul>"
			for key, val := range entry.Data {
				content += fmt.Sprintf("<li>%s: %s</li>\n", key, val)
			}
			content += "</ul>"

			realUrl := fmt.Sprintf("https://pgs.sh/forms/%s/%s", name, entry.ID)
			item := &feeds.Item{
				Id:          entry.ID,
				Title:       entry.Name,
				Link:        &feeds.Link{Href: realUrl},
				Content:     content,
				Created:     *entry.CreatedAt,
				Updated:     *entry.CreatedAt,
				Description: "",
				Author:      &feeds.Author{Name: user.Name},
			}
			feedItems = append(feedItems, item)
		}

	}
	feed.Items = feedItems

	rss, err := feed.ToAtom()
	if err != nil {
		logger.Error("could not convert feed to atom", "err", err.Error())
		http.Error(w, "Could not generate atom rss feed", http.StatusInternalServerError)
	}

	w.Header().Add("Content-Type", "application/atom+xml")
	_, err = w.Write([]byte(rss))
	if err != nil {
		logger.Error("http write failed", "err", err.Error())
	}
}
