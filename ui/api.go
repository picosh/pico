package ui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

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

func registerUser(apiConfig *shared.ApiConfig, ctx ssh.Context, pubkey ssh.PublicKey, pubkeyStr string) http.HandlerFunc {
	logger := apiConfig.Cfg.Logger
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
		shared.SetUserCtx(ctx, user)
		err = json.NewEncoder(w).Encode(picoApi)
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

type featuresPayload struct {
	Features []*db.FeatureFlag `json:"features"`
}

func getFeatures(apiConfig *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := apiConfig.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

func findOrCreateRssToken(apiConfig *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := apiConfig.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

func toFingerprint(pubkey string) (string, error) {
	kk, _, _, _, err := ssh.ParseAuthorizedKey([]byte(pubkey))
	if err != nil {
		return "", err
	}
	return shared.KeyForSha256(kk), nil
}

func getPublicKeys(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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
			fingerprint, err := toFingerprint(pk.Key)
			if err != nil {
				logger.Error("could not parse public key", "err", err.Error())
				continue
			}
			pk.Key = fingerprint
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

func createPubkey(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

		fingerprint, err := toFingerprint(pubkey.Key)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		pubkey.Key = fingerprint
		err = json.NewEncoder(w).Encode(pubkey)
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

func deletePubkey(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}
		dbpool := shared.GetDB(r)
		pubkeyID := shared.GetField(r, 0)

		ownedKeys, err := dbpool.FindKeysForUser(user)
		if err != nil {
			logger.Error("could not query for pubkeys", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		found := false
		for _, key := range ownedKeys {
			if key.ID == pubkeyID {
				found = true
				break
			}
		}

		if !found {
			logger.Error("user trying to delete key they do not own")
			shared.JSONError(w, "user trying to delete key they do not own", http.StatusUnauthorized)
			return
		}

		err = dbpool.RemoveKeys([]string{pubkeyID})
		if err != nil {
			logger.Error("could not remove pubkey", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func patchPubkey(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		pubkeyID := shared.GetField(r, 0)

		var payload createPubkeyPayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)

		auth, err := dbpool.FindPublicKey(pubkeyID)
		if err != nil {
			logger.Error("could not find user with pubkey provided", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		if auth.UserID != user.ID {
			logger.Error("user trying to update pubkey they do not own")
			shared.JSONError(w, "user trying to update pubkey they do not own", http.StatusUnauthorized)
			return
		}

		pubkey, err := dbpool.UpdatePublicKey(pubkeyID, payload.Name)
		if err != nil {
			logger.Error("could not update pubkey", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		fingerprint, err := toFingerprint(pubkey.Key)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		pubkey.Key = fingerprint

		err = json.NewEncoder(w).Encode(pubkey)
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

type tokensPayload struct {
	Tokens []*db.Token `json:"tokens"`
}

func getTokens(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

func createToken(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

func deleteToken(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}
		dbpool := shared.GetDB(r)
		tokenID := shared.GetField(r, 0)

		toks, err := dbpool.FindTokensForUser(user.ID)
		if err != nil {
			logger.Error("could not query for user tokens", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		found := false
		for _, tok := range toks {
			if tok.ID == tokenID {
				found = true
				break
			}
		}

		if !found {
			logger.Error("user trying to delete token they do not own")
			shared.JSONError(w, "user trying to delete token they do not own", http.StatusUnauthorized)
			return
		}

		err = dbpool.RemoveToken(tokenID)
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

func getProjects(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

func getPosts(httpCtx *shared.ApiConfig, ctx ssh.Context, space string) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
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

type objectsPayload struct {
	Objects []*ProjectObject `json:"objects"`
}

type ProjectObject struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type createFeaturePayload struct {
	Name string `json:"name"`
}

var featureAllowList = []string{
	"analytics",
}

func createFeature(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}

		dbpool := shared.GetDB(r)
		var payload createFeaturePayload
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &payload)

		// only allow the user to add certain features to their account
		if !slices.Contains(featureAllowList, payload.Name) {
			err := fmt.Errorf(
				"(%s) is not in feature allowlist (%s)",
				payload.Name,
				strings.Join(featureAllowList, ", "),
			)
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		now := time.Now()
		expiresAt := now.AddDate(100, 0, 0)
		feature, err := dbpool.InsertFeature(user.ID, payload.Name, expiresAt)
		if err != nil {
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		err = json.NewEncoder(w).Encode(feature)
		if err != nil {
			logger.Error("json encode", "err", err.Error())
		}
	}
}

func deleteFeature(httpCtx *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := httpCtx.Cfg.Logger
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}
		dbpool := shared.GetDB(r)
		featureName := shared.GetField(r, 0)

		if !slices.Contains(featureAllowList, featureName) {
			err := fmt.Errorf(
				"(%s) is not in feature allowlist (%s)",
				featureName,
				strings.Join(featureAllowList, ", "),
			)
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		err := dbpool.RemoveFeature(user.ID, featureName)
		if err != nil {
			logger.Error("could not remove features", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

func getProjectObjects(apiConfig *shared.ApiConfig, ctx ssh.Context) http.HandlerFunc {
	logger := apiConfig.Cfg.Logger
	storage := apiConfig.Storage
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}

		projectName := shared.GetField(r, 0) + "/"
		bucketName := shared.GetAssetBucketName(user.ID)
		bucket, err := storage.GetBucket(bucketName)
		if err != nil {
			logger.Info("bucket not found", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusNotFound)
			return
		}
		objects, err := storage.ListObjects(bucket, projectName, true)
		if err != nil {
			logger.Info("cannot fetch objects", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		pobjs := []*ProjectObject{}
		for _, obj := range objects {
			pobjs = append(pobjs, &ProjectObject{
				ID:      fmt.Sprintf("%s%s", projectName, obj.Name()),
				Name:    obj.Name(),
				Size:    obj.Size(),
				ModTime: obj.ModTime(),
			})
		}

		err = json.NewEncoder(w).Encode(&objectsPayload{Objects: pobjs})
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

func getAnalytics(apiConfig *shared.ApiConfig, ctx ssh.Context, sumtype, bytype, where string) http.HandlerFunc {
	logger := apiConfig.Cfg.Logger
	dbpool := apiConfig.Dbpool
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		user, _ := shared.GetUserCtx(ctx)
		if !ensureUser(w, user) {
			return
		}

		fkID := user.ID
		by := "user_id"
		if bytype == "project" {
			fkID = shared.GetField(r, 0)
			by = "project_id"
		} else if bytype == "post" {
			fkID = shared.GetField(r, 0)
			by = "post_id"
		}

		year := &db.SummaryOpts{FkID: fkID, By: by, Interval: "month", Origin: shared.StartOfYear(), Where: where}
		month := &db.SummaryOpts{FkID: fkID, By: by, Interval: "day", Origin: shared.StartOfMonth(), Where: where}

		opts := year
		if sumtype == "month" {
			opts = month
		}

		summary, err := dbpool.VisitSummary(opts)
		if err != nil {
			logger.Info("cannot fetch analytics", "err", err.Error())
			shared.JSONError(w, err.Error(), http.StatusUnprocessableEntity)
			return
		}

		err = json.NewEncoder(w).Encode(&summary)
		if err != nil {
			logger.Error(err.Error())
		}
	}
}

func CreateRoutes(apiConfig *shared.ApiConfig, ctx ssh.Context) []shared.Route {
	logger := apiConfig.Cfg.Logger
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
		shared.NewCorsRoute("POST", "/api/users", registerUser(apiConfig, ctx, pubkey, pubkeyStr)),
		shared.NewCorsRoute("GET", "/api/features", getFeatures(apiConfig, ctx)),
		shared.NewCorsRoute("PUT", "/api/rss-token", findOrCreateRssToken(apiConfig, ctx)),
		shared.NewCorsRoute("GET", "/api/pubkeys", getPublicKeys(apiConfig, ctx)),
		shared.NewCorsRoute("POST", "/api/pubkeys", createPubkey(apiConfig, ctx)),
		shared.NewCorsRoute("DELETE", "/api/pubkeys/(.+)", deletePubkey(apiConfig, ctx)),
		shared.NewCorsRoute("POST", "/api/features", createFeature(apiConfig, ctx)),
		shared.NewCorsRoute("DELETE", "/api/features/(.+)", deleteFeature(apiConfig, ctx)),
		shared.NewCorsRoute("PATCH", "/api/pubkeys/(.+)", patchPubkey(apiConfig, ctx)),
		shared.NewCorsRoute("GET", "/api/tokens", getTokens(apiConfig, ctx)),
		shared.NewCorsRoute("POST", "/api/tokens", createToken(apiConfig, ctx)),
		shared.NewCorsRoute("DELETE", "/api/tokens/(.+)", deleteToken(apiConfig, ctx)),
		shared.NewCorsRoute("GET", "/api/projects/(.+)/analytics", getAnalytics(apiConfig, ctx, "month", "project", "")),
		shared.NewCorsRoute("GET", "/api/projects/(.+)/analytics/year", getAnalytics(apiConfig, ctx, "year", "project", "")),
		shared.NewCorsRoute("GET", "/api/projects/(.+)", getProjectObjects(apiConfig, ctx)),
		shared.NewCorsRoute("GET", "/api/projects", getProjects(apiConfig, ctx)),
		shared.NewCorsRoute("GET", "/api/posts/analytics/year", getAnalytics(apiConfig, ctx, "year", "user", "AND (post_id IS NOT NULL OR (post_id IS NULL AND project_id IS NULL))")),
		shared.NewCorsRoute("GET", "/api/posts/analytics", getAnalytics(apiConfig, ctx, "month", "user", "AND (post_id IS NOT NULL OR (post_id IS NULL AND project_id IS NULL))")),
		shared.NewCorsRoute("GET", "/api/posts/(.+)/analytics", getAnalytics(apiConfig, ctx, "month", "post", "")),
		shared.NewCorsRoute("GET", "/api/posts/(.+)/analytics/year", getAnalytics(apiConfig, ctx, "year", "post", "")),
		shared.NewCorsRoute("GET", "/api/posts/prose", getPosts(apiConfig, ctx, "prose")),
		shared.NewCorsRoute("GET", "/api/posts/pastes", getPosts(apiConfig, ctx, "pastes")),
		shared.NewCorsRoute("GET", "/api/posts/feeds", getPosts(apiConfig, ctx, "feeds")),
		shared.NewCorsRoute("GET", "/api/analytics/year", getAnalytics(apiConfig, ctx, "year", "user", "")),
		shared.NewCorsRoute("GET", "/api/analytics", getAnalytics(apiConfig, ctx, "month", "user", "")),
	}
}
