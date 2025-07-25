package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"strings"
	"time"

	"slices"

	_ "github.com/lib/pq"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
)

var PAGER_SIZE = 15

var SelectPost = `
	posts.id, user_id, app_users.name, filename, slug, title, text, description,
	posts.created_at, publish_at, posts.updated_at, hidden, file_size, mime_type, shasum, data, expires_at, views`

var (
	sqlSelectPosts = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id`, SelectPost)

	sqlSelectPostsBeforeDate = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE publish_at::date <= $1 AND cur_space = $2`, SelectPost)

	sqlSelectPostWithFilename = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE filename = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)

	sqlSelectPostWithSlug = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE slug = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)

	sqlSelectPost = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE posts.id = $1`, SelectPost)

	sqlSelectUpdatedPostsForUser = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE user_id = $1 AND publish_at::date <= CURRENT_DATE AND cur_space = $2
	ORDER BY posts.updated_at DESC`, SelectPost)

	sqlSelectExpiredPosts = fmt.Sprintf(`
		SELECT %s
		FROM posts
		LEFT JOIN app_users ON app_users.id = posts.user_id
		WHERE
			cur_space = $1 AND
			expires_at <= now();
	`, SelectPost)

	sqlSelectPostsForUser = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE
		hidden = FALSE AND
		user_id = $1 AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $2
	GROUP BY %s
	ORDER BY publish_at DESC, slug DESC
	LIMIT $3 OFFSET $4`, SelectPost, SelectPost)

	sqlSelectAllPostsForUser = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE
		user_id = $1 AND
		cur_space = $2
	ORDER BY publish_at DESC`, SelectPost)

	sqlSelectPostsByTag = `
	SELECT
		posts.id,
		user_id,
		filename,
		slug,
		title,
		text,
		description,
		publish_at,
		app_users.name as username,
		posts.updated_at,
		posts.mime_type
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE
		post_tags.name = $3 AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $4
	ORDER BY publish_at DESC
	LIMIT $1 OFFSET $2`

	sqlSelectUserPostsByTag = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE
		hidden = FALSE AND
		user_id = $1 AND
		(post_tags.name = $2 OR hidden = true) AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $3
	ORDER BY publish_at DESC
	LIMIT $4 OFFSET $5`, SelectPost)
)

const (
	sqlSelectPublicKey         = `SELECT id, user_id, name, public_key, created_at FROM public_keys WHERE public_key = $1`
	sqlSelectPublicKeys        = `SELECT id, user_id, name, public_key, created_at FROM public_keys WHERE user_id = $1 ORDER BY created_at ASC`
	sqlSelectUser              = `SELECT id, name, created_at FROM app_users WHERE id = $1`
	sqlSelectUserForName       = `SELECT id, name, created_at FROM app_users WHERE name = $1`
	sqlSelectUserForNameAndKey = `SELECT app_users.id, app_users.name, app_users.created_at, public_keys.id as pk_id, public_keys.public_key, public_keys.created_at as pk_created_at FROM app_users LEFT JOIN public_keys ON public_keys.user_id = app_users.id WHERE app_users.name = $1 AND public_keys.public_key = $2`
	sqlSelectUsers             = `SELECT id, name, created_at FROM app_users ORDER BY name ASC`

	sqlSelectUserForToken = `
	SELECT app_users.id, app_users.name, app_users.created_at
	FROM app_users
	LEFT JOIN tokens ON tokens.user_id = app_users.id
	WHERE tokens.token = $1 AND tokens.expires_at > NOW()`
	sqlInsertToken              = `INSERT INTO tokens (user_id, name) VALUES($1, $2) RETURNING token;`
	sqlRemoveToken              = `DELETE FROM tokens WHERE id = $1`
	sqlSelectTokensForUser      = `SELECT id, user_id, name, created_at, expires_at FROM tokens WHERE user_id = $1`
	sqlSelectTokenByNameForUser = `SELECT token FROM tokens WHERE user_id = $1 AND name = $2`

	sqlSelectFeatureForUser = `SELECT id, user_id, payment_history_id, name, data, created_at, expires_at FROM feature_flags WHERE user_id = $1 AND name = $2 ORDER BY expires_at DESC LIMIT 1`
	sqlSelectSizeForUser    = `SELECT COALESCE(sum(file_size), 0) FROM posts WHERE user_id = $1`

	sqlSelectPostIdByAliasSlug = `SELECT post_id FROM post_aliases WHERE slug = $1`
	sqlSelectTagPostCount      = `
	SELECT count(posts.id)
	FROM posts
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE hidden = FALSE AND cur_space=$1 and post_tags.name = $2`
	sqlSelectPostCount       = `SELECT count(id) FROM posts WHERE hidden = FALSE AND cur_space=$1`
	sqlSelectAllUpdatedPosts = `
	SELECT
		posts.id,
		user_id,
		filename,
		slug,
		title,
		text,
		description,
		publish_at,
		app_users.name as username,
		posts.updated_at,
		posts.mime_type
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE hidden = FALSE AND publish_at::date <= CURRENT_DATE AND cur_space = $3
	ORDER BY updated_at DESC
	LIMIT $1 OFFSET $2`
	// add some users to deny list since they are robogenerating a bunch of posts
	// per day and are creating a lot of noise.
	sqlSelectPostsByRank = `
	SELECT
		posts.id,
		user_id,
		filename,
		slug,
		title,
		text,
		description,
		publish_at,
		app_users.name as username,
		posts.updated_at,
		posts.mime_type
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE
		hidden = FALSE AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $3 AND
		app_users.name NOT IN ('algiegray', 'mrrccc')
	ORDER BY publish_at DESC
	LIMIT $1 OFFSET $2`

	sqlSelectPopularTags = `
	SELECT name, count(post_id) as "tally"
	FROM post_tags
	LEFT JOIN posts ON posts.id = post_id
	WHERE posts.cur_space = $1
	GROUP BY name
	ORDER BY tally DESC
	LIMIT 5`
	sqlSelectTagsForUser = `
	SELECT name
	FROM post_tags
	LEFT JOIN posts ON posts.id = post_id
	WHERE posts.user_id = $1 AND posts.cur_space = $2
	GROUP BY name`
	sqlSelectTagsForPost     = `SELECT name FROM post_tags WHERE post_id=$1`
	sqlSelectFeedItemsByPost = `SELECT id, post_id, guid, data, created_at FROM feed_items WHERE post_id=$1`

	sqlInsertPublicKey = `INSERT INTO public_keys (user_id, public_key) VALUES ($1, $2)`
	sqlInsertPost      = `
	INSERT INTO posts
		(user_id, filename, slug, title, text, description, publish_at, hidden, cur_space,
		file_size, mime_type, shasum, data, expires_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	RETURNING id`
	sqlInsertUser      = `INSERT INTO app_users (name) VALUES($1) returning id`
	sqlInsertTag       = `INSERT INTO post_tags (post_id, name) VALUES($1, $2) RETURNING id;`
	sqlInsertAliases   = `INSERT INTO post_aliases (post_id, slug) VALUES($1, $2) RETURNING id;`
	sqlInsertFeedItems = `INSERT INTO feed_items (post_id, guid, data) VALUES ($1, $2, $3) RETURNING id;`

	sqlUpdatePost = `
	UPDATE posts
	SET slug = $1, title = $2, text = $3, description = $4, updated_at = $5, publish_at = $6,
		file_size = $7, shasum = $8, data = $9, hidden = $11, expires_at = $12
	WHERE id = $10`
	sqlUpdateUserName = `UPDATE app_users SET name = $1 WHERE id = $2`
	sqlIncrementViews = `UPDATE posts SET views = views + 1 WHERE id = $1 RETURNING views`

	sqlRemoveAliasesByPost = `DELETE FROM post_aliases WHERE post_id = $1`
	sqlRemoveTagsByPost    = `DELETE FROM post_tags WHERE post_id = $1`
	sqlRemovePosts         = `DELETE FROM posts WHERE id = ANY($1::uuid[])`
	sqlRemoveKeys          = `DELETE FROM public_keys WHERE id = ANY($1::uuid[])`
	sqlRemoveUsers         = `DELETE FROM app_users WHERE id = ANY($1::uuid[])`

	sqlInsertProject        = `INSERT INTO projects (user_id, name, project_dir) VALUES ($1, $2, $3) RETURNING id;`
	sqlUpdateProject        = `UPDATE projects SET updated_at = $3 WHERE user_id = $1 AND name = $2;`
	sqlFindProjectByName    = `SELECT id, user_id, name, project_dir, acl, blocked, created_at, updated_at FROM projects WHERE user_id = $1 AND name = $2;`
	sqlSelectProjectCount   = `SELECT count(id) FROM projects`
	sqlFindProjectsByUser   = `SELECT id, user_id, name, project_dir, acl, blocked, created_at, updated_at FROM projects WHERE user_id = $1 ORDER BY name ASC, updated_at DESC;`
	sqlFindProjectsByPrefix = `SELECT id, user_id, name, project_dir, acl, blocked, created_at, updated_at FROM projects WHERE user_id = $1 AND name = project_dir AND name ILIKE $2 ORDER BY updated_at ASC, name ASC;`
	sqlFindProjectLinks     = `SELECT id, user_id, name, project_dir, acl, blocked, created_at, updated_at FROM projects WHERE user_id = $1 AND name != project_dir AND project_dir = $2 ORDER BY name ASC;`
	sqlLinkToProject        = `UPDATE projects SET project_dir = $1, updated_at = $2 WHERE id = $3;`
	sqlRemoveProject        = `DELETE FROM projects WHERE id = $1;`
)

type PsqlDB struct {
	Logger *slog.Logger
	Db     *sql.DB
}

type RowScanner interface {
	Scan(dest ...any) error
}

func CreatePostFromRow(r RowScanner) (*db.Post, error) {
	post := &db.Post{}
	err := r.Scan(
		&post.ID,
		&post.UserID,
		&post.Username,
		&post.Filename,
		&post.Slug,
		&post.Title,
		&post.Text,
		&post.Description,
		&post.CreatedAt,
		&post.PublishAt,
		&post.UpdatedAt,
		&post.Hidden,
		&post.FileSize,
		&post.MimeType,
		&post.Shasum,
		&post.Data,
		&post.ExpiresAt,
		&post.Views,
	)
	if err != nil {
		return nil, err
	}
	return post, nil
}

func CreatePostWithTagsFromRow(r RowScanner) (*db.Post, error) {
	post := &db.Post{}
	tagStr := ""
	err := r.Scan(
		&post.ID,
		&post.UserID,
		&post.Username,
		&post.Filename,
		&post.Slug,
		&post.Title,
		&post.Text,
		&post.Description,
		&post.CreatedAt,
		&post.PublishAt,
		&post.UpdatedAt,
		&post.Hidden,
		&post.FileSize,
		&post.MimeType,
		&post.Shasum,
		&post.Data,
		&post.ExpiresAt,
		&post.Views,
		&tagStr,
	)
	if err != nil {
		return nil, err
	}

	tags := strings.Split(tagStr, ",")
	for _, tag := range tags {
		tg := strings.TrimSpace(tag)
		if tg == "" {
			continue
		}
		post.Tags = append(post.Tags, tg)
	}

	return post, nil
}

func NewDB(databaseUrl string, logger *slog.Logger) *PsqlDB {
	var err error
	d := &PsqlDB{
		Logger: logger,
	}
	d.Logger.Info("Connecting to postgres", "databaseUrl", databaseUrl)

	db, err := sql.Open("postgres", databaseUrl)
	if err != nil {
		d.Logger.Error(err.Error())
	}
	d.Db = db
	return d
}

func (me *PsqlDB) RegisterUser(username, pubkey, comment string) (*db.User, error) {
	lowerName := strings.ToLower(username)
	valid, err := me.ValidateName(lowerName)
	if !valid {
		return nil, err
	}

	ctx := context.Background()
	tx, err := me.Db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() {
		err = tx.Rollback()
	}()

	stmt, err := tx.Prepare(sqlInsertUser)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = stmt.Close()
	}()

	var id string
	err = stmt.QueryRow(lowerName).Scan(&id)
	if err != nil {
		return nil, err
	}

	err = me.InsertPublicKey(id, pubkey, comment, tx)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return me.FindUserForKey(username, pubkey)
}

func (me *PsqlDB) RemoveUsers(userIDs []string) error {
	param := "{" + strings.Join(userIDs, ",") + "}"
	_, err := me.Db.Exec(sqlRemoveUsers, param)
	return err
}

func (me *PsqlDB) InsertPublicKey(userID, key, name string, tx *sql.Tx) error {
	pk, _ := me.FindPublicKeyForKey(key)
	if pk != nil {
		return db.ErrPublicKeyTaken
	}
	query := `INSERT INTO public_keys (user_id, public_key, name) VALUES ($1, $2, $3)`
	var err error
	if tx != nil {
		_, err = tx.Exec(query, userID, key, name)
	} else {
		_, err = me.Db.Exec(query, userID, key, name)
	}
	if err != nil {
		return err
	}

	return nil
}

func (me *PsqlDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	pk, err := me.FindPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}

	query := `UPDATE public_keys SET name=$1 WHERE id=$2;`
	_, err = me.Db.Exec(query, name, pk.ID)
	if err != nil {
		return nil, err
	}

	pk, err = me.FindPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}
	return pk, nil
}

func (me *PsqlDB) FindPublicKeyForKey(key string) (*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Query(sqlSelectPublicKey, key)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		pk := &db.PublicKey{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Name, &pk.Key, &pk.CreatedAt)
		if err != nil {
			return nil, err
		}

		keys = append(keys, pk)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	if len(keys) == 0 {
		return nil, fmt.Errorf("pubkey not found in our database: [%s]", key)
	}

	// When we run PublicKeyForKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(keys) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
	}

	return keys[0], nil
}

func (me *PsqlDB) FindPublicKey(pubkeyID string) (*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Query(`SELECT id, user_id, name, public_key, created_at FROM public_keys WHERE id = $1`, pubkeyID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		pk := &db.PublicKey{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Name, &pk.Key, &pk.CreatedAt)
		if err != nil {
			return nil, err
		}

		keys = append(keys, pk)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	if len(keys) == 0 {
		return nil, errors.New("no public keys found for key provided")
	}

	return keys[0], nil
}

func (me *PsqlDB) FindKeysForUser(user *db.User) ([]*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Query(sqlSelectPublicKeys, user.ID)
	if err != nil {
		return keys, err
	}
	for rs.Next() {
		pk := &db.PublicKey{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Name, &pk.Key, &pk.CreatedAt)
		if err != nil {
			return keys, err
		}

		keys = append(keys, pk)
	}
	if rs.Err() != nil {
		return keys, rs.Err()
	}
	return keys, nil
}

func (me *PsqlDB) RemoveKeys(keyIDs []string) error {
	param := "{" + strings.Join(keyIDs, ",") + "}"
	_, err := me.Db.Exec(sqlRemoveKeys, param)
	return err
}

func (me *PsqlDB) FindPostsBeforeDate(date *time.Time, space string) ([]*db.Post, error) {
	// now := time.Now()
	// expired := now.AddDate(0, 0, -3)
	var posts []*db.Post
	rs, err := me.Db.Query(sqlSelectPostsBeforeDate, date, space)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func (me *PsqlDB) FindUserForKey(username string, key string) (*db.User, error) {
	me.Logger.Info("attempting to find user with only public key", "key", key)
	pk, err := me.FindPublicKeyForKey(key)
	if err == nil {
		me.Logger.Info("found pubkey, looking for user", "key", key, "userId", pk.UserID)
		user, err := me.FindUser(pk.UserID)
		if err != nil {
			return nil, err
		}
		user.PublicKey = pk
		return user, nil
	}

	if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
		me.Logger.Info("detected multiple users with same public key", "user", username)
		user, err := me.FindUserForNameAndKey(username, key)
		if err != nil {
			me.Logger.Info("could not find user by username and public key", "user", username, "key", key)
			// this is a little hacky but if we cannot find a user by name and public key
			// then we return the multiple keys detected error so the user knows to specify their
			// when logging in
			return nil, &db.ErrMultiplePublicKeys{}
		}
		return user, nil
	}

	return nil, err
}

func (me *PsqlDB) FindUserByPubkey(key string) (*db.User, error) {
	me.Logger.Info("attempting to find user with only public key", "key", key)
	pk, err := me.FindPublicKeyForKey(key)
	if err != nil {
		return nil, err
	}

	me.Logger.Info("found pubkey, looking for user", "key", key, "userId", pk.UserID)
	user, err := me.FindUser(pk.UserID)
	if err != nil {
		return nil, err
	}
	user.PublicKey = pk
	return user, nil
}

func (me *PsqlDB) FindUser(userID string) (*db.User, error) {
	user := &db.User{}
	var un sql.NullString
	r := me.Db.QueryRow(sqlSelectUser, userID)
	err := r.Scan(&user.ID, &un, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	if un.Valid {
		user.Name = un.String
	}
	return user, nil
}

func (me *PsqlDB) ValidateName(name string) (bool, error) {
	lower := strings.ToLower(name)
	if slices.Contains(db.DenyList, lower) {
		return false, fmt.Errorf("%s is on deny list: %w", lower, db.ErrNameDenied)
	}
	v := db.NameValidator.MatchString(lower)
	if !v {
		return false, fmt.Errorf("%s is invalid: %w", lower, db.ErrNameInvalid)
	}
	user, _ := me.FindUserByName(lower)
	if user == nil {
		return true, nil
	}
	return false, fmt.Errorf("%s already taken: %w", lower, db.ErrNameTaken)
}

func (me *PsqlDB) FindUserByName(name string) (*db.User, error) {
	user := &db.User{}
	r := me.Db.QueryRow(sqlSelectUserForName, strings.ToLower(name))
	err := r.Scan(&user.ID, &user.Name, &user.CreatedAt)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) FindUserForNameAndKey(name string, key string) (*db.User, error) {
	user := &db.User{}
	pk := &db.PublicKey{}

	r := me.Db.QueryRow(sqlSelectUserForNameAndKey, strings.ToLower(name), key)
	err := r.Scan(&user.ID, &user.Name, &user.CreatedAt, &pk.ID, &pk.Key, &pk.CreatedAt)
	if err != nil {
		return nil, err
	}

	user.PublicKey = pk
	return user, nil
}

func (me *PsqlDB) FindUserForToken(token string) (*db.User, error) {
	user := &db.User{}

	r := me.Db.QueryRow(sqlSelectUserForToken, token)
	err := r.Scan(&user.ID, &user.Name, &user.CreatedAt)
	if err != nil {
		return nil, err
	}

	return user, nil
}

func (me *PsqlDB) SetUserName(userID string, name string) error {
	lowerName := strings.ToLower(name)
	valid, err := me.ValidateName(lowerName)
	if !valid {
		return err
	}

	_, err = me.Db.Exec(sqlUpdateUserName, lowerName, userID)
	return err
}

func (me *PsqlDB) FindPostWithFilename(filename string, persona_id string, space string) (*db.Post, error) {
	r := me.Db.QueryRow(sqlSelectPostWithFilename, filename, persona_id, space)
	post, err := CreatePostWithTagsFromRow(r)
	if err != nil {
		return nil, err
	}

	return post, nil
}

func (me *PsqlDB) FindPostWithSlug(slug string, user_id string, space string) (*db.Post, error) {
	r := me.Db.QueryRow(sqlSelectPostWithSlug, slug, user_id, space)
	post, err := CreatePostWithTagsFromRow(r)
	if err != nil {
		// attempt to find post inside post_aliases
		alias := me.Db.QueryRow(sqlSelectPostIdByAliasSlug, slug)
		postID := ""
		err := alias.Scan(&postID)
		if err != nil {
			return nil, err
		}

		return me.FindPost(postID)
	}

	return post, nil
}

func (me *PsqlDB) FindPost(postID string) (*db.Post, error) {
	r := me.Db.QueryRow(sqlSelectPost, postID)
	post, err := CreatePostFromRow(r)
	if err != nil {
		return nil, err
	}

	return post, nil
}

func (me *PsqlDB) postPager(rs *sql.Rows, pageNum int, space string, tag string) (*db.Paginate[*db.Post], error) {
	var posts []*db.Post
	for rs.Next() {
		post := &db.Post{}
		err := rs.Scan(
			&post.ID,
			&post.UserID,
			&post.Filename,
			&post.Slug,
			&post.Title,
			&post.Text,
			&post.Description,
			&post.PublishAt,
			&post.Username,
			&post.UpdatedAt,
			&post.MimeType,
		)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}

	var count int
	var err error
	if tag == "" {
		err = me.Db.QueryRow(sqlSelectPostCount, space).Scan(&count)
	} else {
		err = me.Db.QueryRow(sqlSelectTagPostCount, space, tag).Scan(&count)
	}
	if err != nil {
		return nil, err
	}

	pager := &db.Paginate[*db.Post]{
		Data:  posts,
		Total: int(math.Ceil(float64(count) / float64(pageNum))),
	}

	return pager, nil
}

func (me *PsqlDB) FindAllPosts(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	rs, err := me.Db.Query(sqlSelectPostsByRank, page.Num, page.Num*page.Page, space)
	if err != nil {
		return nil, err
	}
	return me.postPager(rs, page.Num, space, "")
}

func (me *PsqlDB) FindAllUpdatedPosts(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	rs, err := me.Db.Query(sqlSelectAllUpdatedPosts, page.Num, page.Num*page.Page, space)
	if err != nil {
		return nil, err
	}
	return me.postPager(rs, page.Num, space, "")
}

func (me *PsqlDB) InsertPost(post *db.Post) (*db.Post, error) {
	var id string
	err := me.Db.QueryRow(
		sqlInsertPost,
		post.UserID,
		post.Filename,
		post.Slug,
		post.Title,
		post.Text,
		post.Description,
		post.PublishAt,
		post.Hidden,
		post.Space,
		post.FileSize,
		post.MimeType,
		post.Shasum,
		post.Data,
		post.ExpiresAt,
		post.UpdatedAt,
	).Scan(&id)
	if err != nil {
		return nil, err
	}

	return me.FindPost(id)
}

func (me *PsqlDB) UpdatePost(post *db.Post) (*db.Post, error) {
	_, err := me.Db.Exec(
		sqlUpdatePost,
		post.Slug,
		post.Title,
		post.Text,
		post.Description,
		post.UpdatedAt,
		post.PublishAt,
		post.FileSize,
		post.Shasum,
		post.Data,
		post.ID,
		post.Hidden,
		post.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	return me.FindPost(post.ID)
}

func (me *PsqlDB) RemovePosts(postIDs []string) error {
	param := "{" + strings.Join(postIDs, ",") + "}"
	_, err := me.Db.Exec(sqlRemovePosts, param)
	return err
}

func (me *PsqlDB) FindPostsForUser(page *db.Pager, userID string, space string) (*db.Paginate[*db.Post], error) {
	var posts []*db.Post
	rs, err := me.Db.Query(
		sqlSelectPostsForUser,
		userID,
		space,
		page.Num,
		page.Num*page.Page,
	)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		post, err := CreatePostWithTagsFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	var count int
	err = me.Db.QueryRow(sqlSelectPostCount, space).Scan(&count)
	if err != nil {
		return nil, err
	}

	pager := &db.Paginate[*db.Post]{
		Data:  posts,
		Total: int(math.Ceil(float64(count) / float64(page.Num))),
	}
	return pager, nil
}

func (me *PsqlDB) FindAllPostsForUser(userID string, space string) ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := me.Db.Query(sqlSelectAllPostsForUser, userID, space)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func (me *PsqlDB) FindPosts() ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := me.Db.Query(sqlSelectPosts)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func (me *PsqlDB) FindExpiredPosts(space string) ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := me.Db.Query(sqlSelectExpiredPosts, space)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func (me *PsqlDB) FindUpdatedPostsForUser(userID string, space string) ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := me.Db.Query(sqlSelectUpdatedPostsForUser, userID, space)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func (me *PsqlDB) Close() error {
	me.Logger.Info("Closing db")
	return me.Db.Close()
}

func newNullString(s string) sql.NullString {
	if len(s) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{
		String: s,
		Valid:  true,
	}
}

func (me *PsqlDB) InsertVisit(visit *db.AnalyticsVisits) error {
	_, err := me.Db.Exec(
		`INSERT INTO analytics_visits (user_id, project_id, post_id, namespace, host, path, ip_address, user_agent, referer, status, content_type) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11);`,
		visit.UserID,
		newNullString(visit.ProjectID),
		newNullString(visit.PostID),
		newNullString(visit.Namespace),
		visit.Host,
		visit.Path,
		visit.IpAddress,
		visit.UserAgent,
		visit.Referer,
		visit.Status,
		visit.ContentType,
	)
	return err
}

func visitFilterBy(opts *db.SummaryOpts) (string, string) {
	where := ""
	val := ""
	if opts.Host != "" {
		where = "host"
		val = opts.Host
	} else if opts.Path != "" {
		where = "path"
		val = opts.Path
	}

	return where, val
}

func (me *PsqlDB) visitUnique(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	where, with := visitFilterBy(opts)
	uniqueVisitors := fmt.Sprintf(`SELECT
		date_trunc('%s', created_at) as interval_start,
        count(DISTINCT ip_address) as unique_visitors
	FROM analytics_visits
	WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND status <> 404
	GROUP BY interval_start`, opts.Interval, where)

	intervals := []*db.VisitInterval{}
	rs, err := me.Db.Query(uniqueVisitors, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		interval := &db.VisitInterval{}
		err := rs.Scan(
			&interval.Interval,
			&interval.Visitors,
		)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, interval)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}
	return intervals, nil
}

func (me *PsqlDB) visitReferer(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	where, with := visitFilterBy(opts)
	topUrls := fmt.Sprintf(`SELECT
		referer,
		count(DISTINCT ip_address) as referer_count
	FROM analytics_visits
	WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND referer <> '' AND status <> 404
	GROUP BY referer
	ORDER BY referer_count DESC
	LIMIT 10`, where)

	intervals := []*db.VisitUrl{}
	rs, err := me.Db.Query(topUrls, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		interval := &db.VisitUrl{}
		err := rs.Scan(
			&interval.Url,
			&interval.Count,
		)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, interval)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}
	return intervals, nil
}

func (me *PsqlDB) visitUrl(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	where, with := visitFilterBy(opts)
	topUrls := fmt.Sprintf(`SELECT
		path,
		count(DISTINCT ip_address) as path_count
	FROM analytics_visits
	WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND path <> '' AND status <> 404
	GROUP BY path
	ORDER BY path_count DESC
	LIMIT 10`, where)

	intervals := []*db.VisitUrl{}
	rs, err := me.Db.Query(topUrls, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		interval := &db.VisitUrl{}
		err := rs.Scan(
			&interval.Url,
			&interval.Count,
		)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, interval)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}
	return intervals, nil
}

func (me *PsqlDB) VisitUrlNotFound(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}
	where, with := visitFilterBy(opts)
	topUrls := fmt.Sprintf(`SELECT
		path,
		count(DISTINCT ip_address) as path_count
	FROM analytics_visits
	WHERE created_at >= $1 AND %s = $2 AND user_id = $3 AND path <> '' AND status = 404
	GROUP BY path
	ORDER BY path_count DESC
	LIMIT %d`, where, limit)

	intervals := []*db.VisitUrl{}
	rs, err := me.Db.Query(topUrls, opts.Origin, with, opts.UserID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		interval := &db.VisitUrl{}
		err := rs.Scan(
			&interval.Url,
			&interval.Count,
		)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, interval)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}
	return intervals, nil
}

func (me *PsqlDB) visitHost(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	topUrls := `SELECT
		host,
		count(DISTINCT ip_address) as host_count
	FROM analytics_visits
	WHERE user_id = $1 AND host <> ''
	GROUP BY host
	ORDER BY host_count DESC`

	intervals := []*db.VisitUrl{}
	rs, err := me.Db.Query(topUrls, opts.UserID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		interval := &db.VisitUrl{}
		err := rs.Scan(
			&interval.Url,
			&interval.Count,
		)
		if err != nil {
			return nil, err
		}

		intervals = append(intervals, interval)
	}
	if rs.Err() != nil {
		return nil, rs.Err()
	}
	return intervals, nil
}

func (me *PsqlDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	visitors, err := me.visitUnique(opts)
	if err != nil {
		return nil, err
	}

	urls, err := me.visitUrl(opts)
	if err != nil {
		return nil, err
	}

	notFound, err := me.VisitUrlNotFound(opts)
	if err != nil {
		return nil, err
	}

	refs, err := me.visitReferer(opts)
	if err != nil {
		return nil, err
	}

	return &db.SummaryVisits{
		Intervals:    visitors,
		TopUrls:      urls,
		TopReferers:  refs,
		NotFoundUrls: notFound,
	}, nil
}

func (me *PsqlDB) FindVisitSiteList(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	return me.visitHost(opts)
}

func (me *PsqlDB) FindUsers() ([]*db.User, error) {
	var users []*db.User
	rs, err := me.Db.Query(sqlSelectUsers)
	if err != nil {
		return users, err
	}
	for rs.Next() {
		var name sql.NullString
		user := &db.User{}
		err := rs.Scan(
			&user.ID,
			&name,
			&user.CreatedAt,
		)
		if err != nil {
			return users, err
		}
		user.Name = name.String

		users = append(users, user)
	}
	if rs.Err() != nil {
		return users, rs.Err()
	}
	return users, nil
}

func (me *PsqlDB) removeTagsForPost(tx *sql.Tx, postID string) error {
	_, err := tx.Exec(sqlRemoveTagsByPost, postID)
	return err
}

func (me *PsqlDB) insertTagsForPost(tx *sql.Tx, tags []string, postID string) ([]string, error) {
	ids := make([]string, 0)
	for _, tag := range tags {
		id := ""
		err := tx.QueryRow(sqlInsertTag, postID, tag).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

func (me *PsqlDB) ReplaceTagsForPost(tags []string, postID string) error {
	ctx := context.Background()
	tx, err := me.Db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		err = tx.Rollback()
	}()

	err = me.removeTagsForPost(tx, postID)
	if err != nil {
		return err
	}

	_, err = me.insertTagsForPost(tx, tags, postID)
	if err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) removeAliasesForPost(tx *sql.Tx, postID string) error {
	_, err := tx.Exec(sqlRemoveAliasesByPost, postID)
	return err
}

func (me *PsqlDB) insertAliasesForPost(tx *sql.Tx, aliases []string, postID string) ([]string, error) {
	// hardcoded
	denyList := []string{
		"rss",
		"rss.xml",
		"rss.atom",
		"atom.xml",
		"feed.xml",
		"smol.css",
		"main.css",
		"syntax.css",
		"card.png",
		"favicon-16x16.png",
		"favicon-32x32.png",
		"apple-touch-icon.png",
		"favicon.ico",
		"robots.txt",
		"atom",
		"blog/index.xml",
	}

	ids := make([]string, 0)
	for _, alias := range aliases {
		if slices.Contains(denyList, alias) {
			me.Logger.Info(
				"name is in the deny list for aliases because it conflicts with a static route, skipping",
				"alias", alias,
			)
			continue
		}
		id := ""
		err := tx.QueryRow(sqlInsertAliases, postID, alias).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

func (me *PsqlDB) ReplaceAliasesForPost(aliases []string, postID string) error {
	ctx := context.Background()
	tx, err := me.Db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		err = tx.Rollback()
	}()

	err = me.removeAliasesForPost(tx, postID)
	if err != nil {
		return err
	}

	_, err = me.insertAliasesForPost(tx, aliases, postID)
	if err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) FindUserPostsByTag(page *db.Pager, tag, userID, space string) (*db.Paginate[*db.Post], error) {
	var posts []*db.Post
	rs, err := me.Db.Query(
		sqlSelectUserPostsByTag,
		userID,
		tag,
		space,
		page.Num,
		page.Num*page.Page,
	)
	if err != nil {
		return nil, err
	}
	for rs.Next() {
		post, err := CreatePostFromRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	var count int
	err = me.Db.QueryRow(sqlSelectPostCount, space).Scan(&count)
	if err != nil {
		return nil, err
	}

	pager := &db.Paginate[*db.Post]{
		Data:  posts,
		Total: int(math.Ceil(float64(count) / float64(page.Num))),
	}
	return pager, nil
}

func (me *PsqlDB) FindPostsByTag(pager *db.Pager, tag, space string) (*db.Paginate[*db.Post], error) {
	rs, err := me.Db.Query(
		sqlSelectPostsByTag,
		pager.Num,
		pager.Num*pager.Page,
		tag,
		space,
	)
	if err != nil {
		return nil, err
	}

	return me.postPager(rs, pager.Num, space, tag)
}

func (me *PsqlDB) FindPopularTags(space string) ([]string, error) {
	tags := make([]string, 0)
	rs, err := me.Db.Query(sqlSelectPopularTags, space)
	if err != nil {
		return tags, err
	}
	for rs.Next() {
		name := ""
		tally := 0
		err := rs.Scan(&name, &tally)
		if err != nil {
			return tags, err
		}

		tags = append(tags, name)
	}
	if rs.Err() != nil {
		return tags, rs.Err()
	}
	return tags, nil
}

func (me *PsqlDB) FindTagsForUser(userID string, space string) ([]string, error) {
	tags := []string{}
	rs, err := me.Db.Query(sqlSelectTagsForUser, userID, space)
	if err != nil {
		return tags, err
	}
	for rs.Next() {
		name := ""
		err := rs.Scan(&name)
		if err != nil {
			return tags, err
		}

		tags = append(tags, name)
	}
	if rs.Err() != nil {
		return tags, rs.Err()
	}
	return tags, nil
}

func (me *PsqlDB) FindTagsForPost(postID string) ([]string, error) {
	tags := make([]string, 0)
	rs, err := me.Db.Query(sqlSelectTagsForPost, postID)
	if err != nil {
		return tags, err
	}

	for rs.Next() {
		name := ""
		err := rs.Scan(&name)
		if err != nil {
			return tags, err
		}

		tags = append(tags, name)
	}

	if rs.Err() != nil {
		return tags, rs.Err()
	}

	return tags, nil
}

func (me *PsqlDB) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	ff := &db.FeatureFlag{}
	// payment history is allowed to be null
	// https://devtidbits.com/2020/08/03/go-sql-error-converting-null-to-string-is-unsupported/
	var paymentHistoryID sql.NullString
	err := me.Db.QueryRow(sqlSelectFeatureForUser, userID, feature).Scan(
		&ff.ID,
		&ff.UserID,
		&paymentHistoryID,
		&ff.Name,
		&ff.Data,
		&ff.CreatedAt,
		&ff.ExpiresAt,
	)
	if err != nil {
		return nil, err
	}

	ff.PaymentHistoryID = paymentHistoryID

	return ff, nil
}

func (me *PsqlDB) FindFeaturesForUser(userID string) ([]*db.FeatureFlag, error) {
	var features []*db.FeatureFlag
	// https://stackoverflow.com/a/16920077
	query := `SELECT DISTINCT ON (name)
			id, user_id, payment_history_id, name, data, created_at, expires_at
		FROM feature_flags
		WHERE user_id=$1
		ORDER BY name, expires_at DESC;`
	rs, err := me.Db.Query(query, userID)
	if err != nil {
		return features, err
	}
	for rs.Next() {
		var paymentHistoryID sql.NullString
		ff := &db.FeatureFlag{}
		err := rs.Scan(
			&ff.ID,
			&ff.UserID,
			&paymentHistoryID,
			&ff.Name,
			&ff.Data,
			&ff.CreatedAt,
			&ff.ExpiresAt,
		)
		if err != nil {
			return features, err
		}
		ff.PaymentHistoryID = paymentHistoryID

		features = append(features, ff)
	}
	if rs.Err() != nil {
		return features, rs.Err()
	}
	return features, nil
}

func (me *PsqlDB) HasFeatureForUser(userID string, feature string) bool {
	ff, err := me.FindFeature(userID, feature)
	if err != nil {
		return false
	}
	return ff.IsValid()
}

func (me *PsqlDB) FindTotalSizeForUser(userID string) (int, error) {
	var fileSize int
	err := me.Db.QueryRow(sqlSelectSizeForUser, userID).Scan(&fileSize)
	if err != nil {
		return 0, err
	}
	return fileSize, nil
}

func (me *PsqlDB) InsertFeedItems(postID string, items []*db.FeedItem) error {
	ctx := context.Background()
	tx, err := me.Db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		err = tx.Rollback()
	}()

	for _, item := range items {
		_, err := tx.Exec(
			sqlInsertFeedItems,
			item.PostID,
			item.GUID,
			item.Data,
		)
		if err != nil {
			return err
		}
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) FindFeedItemsByPostID(postID string) ([]*db.FeedItem, error) {
	// sqlSelectFeedItemsByPost
	items := make([]*db.FeedItem, 0)
	rs, err := me.Db.Query(sqlSelectFeedItemsByPost, postID)
	if err != nil {
		return items, err
	}

	for rs.Next() {
		item := &db.FeedItem{}
		err := rs.Scan(
			&item.ID,
			&item.PostID,
			&item.GUID,
			&item.Data,
			&item.CreatedAt,
		)
		if err != nil {
			return items, err
		}

		items = append(items, item)
	}

	if rs.Err() != nil {
		return items, rs.Err()
	}

	return items, nil
}

func (me *PsqlDB) InsertProject(userID, name, projectDir string) (string, error) {
	if !utils.IsValidSubdomain(name) {
		return "", fmt.Errorf("'%s' is not a valid project name, must match /^[a-z0-9-]+$/", name)
	}

	var id string
	err := me.Db.QueryRow(sqlInsertProject, userID, name, projectDir).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (me *PsqlDB) UpdateProject(userID, name string) error {
	_, err := me.Db.Exec(sqlUpdateProject, userID, name, time.Now())
	return err
}

func (me *PsqlDB) FindProjectByName(userID, name string) (*db.Project, error) {
	project := &db.Project{}
	r := me.Db.QueryRow(sqlFindProjectByName, userID, name)
	err := r.Scan(
		&project.ID,
		&project.UserID,
		&project.Name,
		&project.ProjectDir,
		&project.Acl,
		&project.Blocked,
		&project.CreatedAt,
		&project.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}

	return project, nil
}

func (me *PsqlDB) InsertToken(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(sqlInsertToken, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) UpsertToken(userID, name string) (string, error) {
	token, _ := me.FindTokenByName(userID, name)
	if token != "" {
		return token, nil
	}

	token, err := me.InsertToken(userID, name)
	return token, err
}

func (me *PsqlDB) FindTokenByName(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(sqlSelectTokenByNameForUser, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) RemoveToken(tokenID string) error {
	_, err := me.Db.Exec(sqlRemoveToken, tokenID)
	return err
}

func (me *PsqlDB) FindTokensForUser(userID string) ([]*db.Token, error) {
	var keys []*db.Token
	rs, err := me.Db.Query(sqlSelectTokensForUser, userID)
	if err != nil {
		return keys, err
	}
	for rs.Next() {
		pk := &db.Token{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Name, &pk.CreatedAt, &pk.ExpiresAt)
		if err != nil {
			return keys, err
		}

		keys = append(keys, pk)
	}
	if rs.Err() != nil {
		return keys, rs.Err()
	}
	return keys, nil
}

func (me *PsqlDB) InsertFeature(userID, name string, expiresAt time.Time) (*db.FeatureFlag, error) {
	var featureID string
	err := me.Db.QueryRow(
		`INSERT INTO feature_flags (user_id, name, expires_at) VALUES ($1, $2, $3) RETURNING id;`,
		userID,
		name,
		expiresAt,
	).Scan(&featureID)
	if err != nil {
		return nil, err
	}

	feature, err := me.FindFeature(userID, name)
	if err != nil {
		return nil, err
	}

	return feature, nil
}

func (me *PsqlDB) RemoveFeature(userID string, name string) error {
	_, err := me.Db.Exec(`DELETE FROM feature_flags WHERE user_id = $1 AND name = $2`, userID, name)
	return err
}

func (me *PsqlDB) createFeatureExpiresAt(userID, name string) time.Time {
	ff, _ := me.FindFeature(userID, name)
	if ff == nil {
		t := time.Now()
		return t.AddDate(1, 0, 0)
	}
	return ff.ExpiresAt.AddDate(1, 0, 0)
}

func (me *PsqlDB) AddPicoPlusUser(username, email, paymentType, txId string) error {
	user, err := me.FindUserByName(username)
	if err != nil {
		return err
	}

	ctx := context.Background()
	tx, err := me.Db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		err = tx.Rollback()
	}()

	var paymentHistoryId sql.NullString
	if paymentType != "" {
		data := db.PaymentHistoryData{
			Notes: "",
			TxID:  txId,
		}

		err := tx.QueryRow(
			`INSERT INTO payment_history (user_id, payment_type, amount, data) VALUES ($1, $2, 24 * 1000000, $3) RETURNING id;`,
			user.ID,
			paymentType,
			data,
		).Scan(&paymentHistoryId)
		if err != nil {
			return err
		}
	}

	plus := me.createFeatureExpiresAt(user.ID, "plus")
	plusQuery := fmt.Sprintf(`INSERT INTO feature_flags (user_id, name, data, expires_at, payment_history_id)
	VALUES ($1, 'plus', '{"storage_max":10000000000, "file_max":50000000, "email": "%s"}'::jsonb, $2, $3);`, email)
	_, err = tx.Exec(plusQuery, user.ID, plus, paymentHistoryId)
	if err != nil {
		return err
	}

	return tx.Commit()
}

func (me *PsqlDB) UpsertProject(userID, projectName, projectDir string) (*db.Project, error) {
	project, err := me.FindProjectByName(userID, projectName)
	if err == nil {
		// this just updates the `createdAt` timestamp, useful for book-keeping
		err = me.UpdateProject(userID, projectName)
		if err != nil {
			me.Logger.Error("could not update project", "err", err)
			return nil, err
		}
		return project, nil
	}

	_, err = me.InsertProject(userID, projectName, projectName)
	if err != nil {
		me.Logger.Error("could not create project", "err", err)
		return nil, err
	}
	return me.FindProjectByName(userID, projectName)
}

func (me *PsqlDB) findPagesStats(userID string) (*db.UserServiceStats, error) {
	stats := db.UserServiceStats{
		Service: "pgs",
	}
	err := me.Db.QueryRow(
		`SELECT count(id), min(created_at), max(created_at), max(updated_at) FROM projects WHERE user_id=$1`,
		userID,
	).Scan(&stats.Num, &stats.FirstCreatedAt, &stats.LastestCreatedAt, &stats.LatestUpdatedAt)
	if err != nil {
		return nil, err
	}

	return &stats, nil
}

func (me *PsqlDB) InsertTunsEventLog(log *db.TunsEventLog) error {
	_, err := me.Db.Exec(
		`INSERT INTO tuns_event_logs
			(user_id, server_id, remote_addr, event_type, tunnel_type, connection_type, tunnel_id)
		VALUES
			($1, $2, $3, $4, $5, $6, $7)`,
		log.UserId, log.ServerID, log.RemoteAddr, log.EventType, log.TunnelType,
		log.ConnectionType, log.TunnelID,
	)
	return err
}

func (me *PsqlDB) FindTunsEventLogsByAddr(userID, addr string) ([]*db.TunsEventLog, error) {
	logs := []*db.TunsEventLog{}
	rs, err := me.Db.Query(
		`SELECT id, user_id, server_id, remote_addr, event_type, tunnel_type, connection_type, tunnel_id, created_at
		FROM tuns_event_logs WHERE user_id=$1 AND tunnel_id=$2 ORDER BY created_at DESC`, userID, addr)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		log := db.TunsEventLog{}
		err := rs.Scan(
			&log.ID, &log.UserId, &log.ServerID, &log.RemoteAddr,
			&log.EventType, &log.TunnelType, &log.ConnectionType,
			&log.TunnelID, &log.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		logs = append(logs, &log)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	return logs, nil
}

func (me *PsqlDB) FindTunsEventLogs(userID string) ([]*db.TunsEventLog, error) {
	logs := []*db.TunsEventLog{}
	rs, err := me.Db.Query(
		`SELECT id, user_id, server_id, remote_addr, event_type, tunnel_type, connection_type, tunnel_id, created_at
		FROM tuns_event_logs WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		log := db.TunsEventLog{}
		err := rs.Scan(
			&log.ID, &log.UserId, &log.ServerID, &log.RemoteAddr,
			&log.EventType, &log.TunnelType, &log.ConnectionType,
			&log.TunnelID, &log.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		logs = append(logs, &log)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	return logs, nil
}

func (me *PsqlDB) FindUserStats(userID string) (*db.UserStats, error) {
	stats := db.UserStats{}
	rs, err := me.Db.Query(`SELECT cur_space, count(id), min(created_at), max(created_at), max(updated_at) FROM posts WHERE user_id=$1 GROUP BY cur_space`, userID)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		stat := db.UserServiceStats{}
		err := rs.Scan(&stat.Service, &stat.Num, &stat.FirstCreatedAt, &stat.LastestCreatedAt, &stat.LatestUpdatedAt)
		if err != nil {
			return nil, err
		}
		switch stat.Service {
		case "prose":
			stats.Prose = stat
		case "pastes":
			stats.Pastes = stat
		case "feeds":
			stats.Feeds = stat
		}
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	pgs, err := me.findPagesStats(userID)
	if err != nil {
		return nil, err
	}
	stats.Pages = *pgs
	return &stats, err
}
