package pgs

import (
	"net/http"

	"github.com/picosh/pico/pkg/shared/router"
)

func handleAutoForm(w http.ResponseWriter, r *http.Request, cfg *PgsConfig) {
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

func serveAutoFormSubmitted(w http.ResponseWriter, r *http.Request, cfg *PgsConfig) {
	errorMsg := r.URL.Query().Get("error")
	data := loginFormData{
		Error: errorMsg,
	}

	w.WriteHeader(http.StatusUnprocessableEntity)

	ts, err := renderTemplate(cfg, []string{cfg.StaticPath("html/auto_form.page.tmpl")})
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
