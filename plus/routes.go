package plus

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/charmbracelet/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
)

type registerPayload struct {
	Name string `json:"name"`
}

func registerUser(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbpool := shared.GetDB(r)
		var payload registerPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)

		user, err := dbpool.RegisterUser(payload.Name, pubkey)
		if err != nil {
			errMsg := fmt.Sprintf("error registering user: %s", err.Error())
			logger.Info(errMsg)
			shared.JSONError(w, errMsg, http.StatusUnprocessableEntity)
			return
		}

		pico := &db.PicoApi{
			UserID:    user.ID,
			UserName:  user.Name,
			PublicKey: pubkey,
		}
		err = json.NewEncoder(w).Encode(pico)
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type featuresPayload struct {
	Features []*db.FeatureFlag `json:"features"`
}

func getFeatures(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")

		dbpool := shared.GetDB(r)
		user, err := dbpool.FindUserForKey("", pubkey)
		if err != nil {
			shared.JSONError(w, "User not found", http.StatusNotFound)
			return
		}

		features, err := dbpool.FindFeaturesForUser(user.ID)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if features == nil {
			features = []*db.FeatureFlag{}
		}
		err = json.NewEncoder(w).Encode(&featuresPayload{Features: features})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type rssTokenPayload struct {
	Token string `json:"token"`
}

func findOrCreateRssToken(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbpool := shared.GetDB(r)
		user, err := dbpool.FindUserForKey("", pubkey)
		if err != nil {
			shared.JSONError(w, "User not found", http.StatusUnprocessableEntity)
			return
		}

		rssToken, err := dbpool.FindRssToken(user.ID)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if rssToken == "" {
			rssToken, err = dbpool.InsertToken(user.ID, "pico-rss")
			if err != nil {
				shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
		}

		err = json.NewEncoder(w).Encode(&rssTokenPayload{Token: rssToken})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type pubkeysPayload struct {
	Pubkeys []*db.PublicKey `json:"pubkeys"`
}

func getPublicKeys(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbpool := shared.GetDB(r)
		user, err := dbpool.FindUserForKey("", pubkey)
		if err != nil {
			shared.JSONError(w, "User not found", http.StatusUnprocessableEntity)
			return
		}

		pubkeys, err := dbpool.FindKeysForUser(user)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		err = json.NewEncoder(w).Encode(&pubkeysPayload{Pubkeys: pubkeys})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type tokensPayload struct {
	Tokens []*db.Token `json:"tokens"`
}

func getTokens(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbpool := shared.GetDB(r)
		user, err := dbpool.FindUserForKey("", pubkey)
		if err != nil {
			shared.JSONError(w, "User not found", http.StatusUnprocessableEntity)
			return
		}

		tokens, err := dbpool.FindTokensForUser(user.ID)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if tokens == nil {
			tokens = []*db.Token{}
		}

		err = json.NewEncoder(w).Encode(&tokensPayload{Tokens: tokens})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

func CreateRoutes(httpCtx *shared.HttpCtx, ctx ssh.Context) []shared.Route {
	logger := httpCtx.Cfg.Logger
	pubkey, err := shared.GetPublicKeyCtx(ctx)
	if err != nil {
		logger.Error("could not get pubkey from ctx", "err", err.Error())
		return []shared.Route{}
	}

	pubkeyStr, err := shared.KeyForKeyText(pubkey)
	if err != nil {
		logger.Error("could not convert key to text", "err", err.Error())
		return []shared.Route{}
	}

	return []shared.Route{
		shared.NewCorsRoute("POST", "/api/users", registerUser(httpCtx, ctx, pubkeyStr)),
		shared.NewCorsRoute("GET", "/api/features", getFeatures(httpCtx, ctx, pubkeyStr)),
		shared.NewCorsRoute("PUT", "/api/rss-token", findOrCreateRssToken(httpCtx, ctx, pubkeyStr)),
		shared.NewCorsRoute("GET", "/api/pubkeys", getPublicKeys(httpCtx, ctx, pubkeyStr)),
		shared.NewCorsRoute("GET", "/api/tokens", getTokens(httpCtx, ctx, pubkeyStr)),
	}
}
