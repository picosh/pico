package ui

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

func ensureUser(w http.ResponseWriter, user *db.User) bool {
	if user == nil {
		shared.JSONError(w, "User not found", http.StatusNotFound)
		return false
	}
	return true
}

func registerUser(httpCtx *shared.HttpCtx, ctx ssh.Context, pubkey ssh.PublicKey, pubkeyStr string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		dbpool := shared.GetDB(r)
		var payload registerPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)

		user, err := dbpool.RegisterUser(payload.Name, pubkeyStr)
		if err != nil {
			errMsg := fmt.Sprintf("error registering user: %s", err.Error())
			logger.Info(errMsg)
			shared.JSONError(w, errMsg, http.StatusUnprocessableEntity)
			return
		}

		picoApi := shared.NewUserApi(user, pubkey)
		err = json.NewEncoder(w).Encode(picoApi)
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type featuresPayload struct {
	Features []*db.FeatureFlag `json:"features"`
}

func getFeatures(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
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

type tokenSecretPayload struct {
	Secret string `json:"secret"`
}

func findOrCreateRssToken(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		var err error
		rssToken, _ := dbpool.FindRssToken(user.ID)
		if rssToken == "" {
			rssToken, err = dbpool.InsertToken(user.ID, "pico-rss")
			if err != nil {
				shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
				return
			}
		}

		err = json.NewEncoder(w).Encode(&tokenSecretPayload{Secret: rssToken})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type pubkeysPayload struct {
	Pubkeys []*db.PublicKey `json:"pubkeys"`
}

func getPublicKeys(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		pubkeys, err := dbpool.FindKeysForUser(user)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		for _, pk := range pubkeys {
			kk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pk.Key))
			if err != nil {
				logger.Error("could not parse public key", "err", err.Error())
				continue
			}
			pk.Key = shared.KeyForSha256(kk)
		}

		err = json.NewEncoder(w).Encode(&pubkeysPayload{Pubkeys: pubkeys})
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

type createPubkeyPayload struct {
	Pubkey string `json:"pubkey"`
	Name   string `json:"name"`
}

func createPubkey(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		var payload createPubkeyPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		pubkey, err := dbpool.InsertPublicKey(user.ID, payload.Pubkey, payload.Name, nil)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		err = json.NewEncoder(w).Encode(pubkey)
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

func deletePubkey(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}
		pubkeyID := shared.GetField(r, 0)

		dbpool := shared.GetDB(r)
		err := dbpool.RemoveKeys([]string{pubkeyID})
		if err != nil {
			logger.Error("could not remove pubkey", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type tokensPayload struct {
	Tokens []*db.Token `json:"tokens"`
}

func getTokens(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
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

type createTokenPayload struct {
	Name string `json:"name"`
}

type createTokenResponsePayload struct {
	Secret string    `json:"secret"`
	Token  *db.Token `json:"token"`
}

func createToken(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		var payload createTokenPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)
		secret, err := dbpool.InsertToken(user.ID, payload.Name)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		// TODO: find token by name
		tokens, err := dbpool.FindTokensForUser(user.ID)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
		}

		var token *db.Token
		for _, tok := range tokens {
			if tok.Name == payload.Name {
				token = tok
				break
			}
		}

		err = json.NewEncoder(w).Encode(&createTokenResponsePayload{Secret: secret, Token: token})
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

func deleteToken(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}
		tokenID := shared.GetField(r, 0)

		dbpool := shared.GetDB(r)
		err := dbpool.RemoveToken(tokenID)
		if err != nil {
			logger.Error("could not remove token", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

type projectsPayload struct {
	Projects []*db.Project `json:"projects"`
}

func getProjects(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		projects, err := dbpool.FindProjectsByUser(user.ID)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if projects == nil {
			projects = []*db.Project{}
		}

		err = json.NewEncoder(w).Encode(&projectsPayload{Projects: projects})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type postsPayload struct {
	Posts []*db.Post `json:"posts"`
}

func getPosts(httpCtx *shared.HttpCtx, ctx ssh.Context, user *db.User, space string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		posts, err := dbpool.FindAllPostsForUser(user.ID, space)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if posts == nil {
			posts = []*db.Post{}
		}

		err = json.NewEncoder(w).Encode(&postsPayload{Posts: posts})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

func CreateRoutes(httpCtx *shared.HttpCtx, ctx ssh.Context) []shared.Route {
	logger := httpCtx.Cfg.Logger
	dbpool := httpCtx.Dbpool
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

	user, _ := dbpool.FindUserForKey("", pubkeyStr)

	return []shared.Route{
		shared.NewCorsRoute("POST", "/api/users", registerUser(httpCtx, ctx, pubkey, pubkeyStr)),
		shared.NewCorsRoute("GET", "/api/features", getFeatures(httpCtx, ctx, user)),
		shared.NewCorsRoute("PUT", "/api/rss-token", findOrCreateRssToken(httpCtx, ctx, user)),
		shared.NewCorsRoute("GET", "/api/pubkeys", getPublicKeys(httpCtx, ctx, user)),
		shared.NewCorsRoute("POST", "/api/pubkeys", createPubkey(httpCtx, ctx, user)),
		shared.NewCorsRoute("DELETE", "/api/pubkeys/(.+)", deletePubkey(httpCtx, ctx, user)),
		shared.NewCorsRoute("GET", "/api/tokens", getTokens(httpCtx, ctx, user)),
		shared.NewCorsRoute("POST", "/api/tokens", createToken(httpCtx, ctx, user)),
		shared.NewCorsRoute("DELETE", "/api/tokens/(.+)", deleteToken(httpCtx, ctx, user)),
		shared.NewCorsRoute("GET", "/api/projects", getProjects(httpCtx, ctx, user)),
		shared.NewCorsRoute("GET", "/api/posts/prose", getPosts(httpCtx, ctx, user, "prose")),
		shared.NewCorsRoute("GET", "/api/posts/pastes", getPosts(httpCtx, ctx, user, "pastes")),
		shared.NewCorsRoute("GET", "/api/posts/feeds", getPosts(httpCtx, ctx, user, "feeds")),
	}
}
