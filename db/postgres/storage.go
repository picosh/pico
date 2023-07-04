package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	_ "github.com/lib/pq"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/wish/cms/config"
	"go.uber.org/zap"
	"golang.org/x/exp/slices"
)

var PAGER_SIZE = 15

var SelectPost = `
	posts.id, user_id, app_users.name, path, filename, slug, title, text, description,
	posts.created_at, publish_at, posts.updated_at, hidden, file_size, mime_type, shasum, data, expires_at, views`

var (
	sqlSelectPosts = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id`, SelectPost)

	sqlSelectPostsBeforeDate = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	WHERE publish_at::date <= $1 AND cur_space = $2`, SelectPost)

	sqlSelectPostWithFilename = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
	WHERE filename = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)

	sqlSelectPostWithSlug = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
	WHERE slug = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)

	sqlSelectPostWithPath = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
	WHERE path = $1 AND filename = $2 AND user_id = $3 AND cur_space = $4
	GROUP BY %s`, SelectPost, SelectPost)

	sqlSelectPost = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	WHERE posts.id = $1`, SelectPost)

	sqlSelectUpdatedPostsForUser = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	WHERE user_id = $1 AND publish_at::date <= CURRENT_DATE AND cur_space = $2
	ORDER BY posts.updated_at DESC`, SelectPost)

	sqlSelectExpiredPosts = fmt.Sprintf(`
		SELECT %s
		FROM posts
		LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
		WHERE
			cur_space = $1 AND
			expires_at <= now();
	`, SelectPost)

	sqlSelectPostsForUser = fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
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
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
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
		posts.mime_type,
		0 AS "score"
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
	WHERE
		post_tags.name = $3 AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $4
	ORDER BY publish_at DESC
	LIMIT $1 OFFSET $2`

	sqlSelectUserPostsByTag = fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
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
	sqlSelectPublicKey         = `SELECT id, user_id, public_key, created_at FROM public_keys WHERE public_key = $1`
	sqlSelectPublicKeys        = `SELECT id, user_id, public_key, created_at FROM public_keys WHERE user_id = $1`
	sqlSelectUser              = `SELECT id, name, created_at FROM app_users WHERE id = $1`
	sqlSelectUserForName       = `SELECT id, name, created_at FROM app_users WHERE name = $1`
	sqlSelectUserForNameAndKey = `SELECT app_users.id, app_users.name, app_users.created_at, public_keys.id as pk_id, public_keys.public_key, public_keys.created_at as pk_created_at FROM app_users LEFT OUTER JOIN public_keys ON public_keys.user_id = app_users.id WHERE app_users.name = $1 AND public_keys.public_key = $2`
	sqlSelectUsers             = `SELECT id, name, created_at FROM app_users ORDER BY name ASC`

	sqlSelectTotalUsers          = `SELECT count(id) FROM app_users`
	sqlSelectUsersAfterDate      = `SELECT count(id) FROM app_users WHERE created_at >= $1`
	sqlSelectTotalPosts          = `SELECT count(id) FROM posts WHERE cur_space = $1`
	sqlSelectTotalPostsAfterDate = `SELECT count(id) FROM posts WHERE created_at >= $1 AND cur_space = $2`
	sqlSelectUsersWithPost       = `SELECT count(app_users.id) FROM app_users WHERE EXISTS (SELECT 1 FROM posts WHERE user_id = app_users.id AND cur_space = $1);`

	sqlSelectFeatureForUser = `SELECT id FROM feature_flags WHERE user_id = $1 AND name = $2`
	sqlSelectSizeForUser    = `SELECT sum(file_size) FROM posts WHERE user_id = $1`

	sqlSelectPostIdByAliasSlug = `SELECT post_id FROM post_aliases WHERE slug = $1`
	sqlSelectTagPostCount      = `
	SELECT count(posts.id)
	FROM posts
	LEFT OUTER JOIN post_tags ON post_tags.post_id = posts.id
	WHERE hidden = FALSE AND cur_space=$1 and post_tags.name = $2`
	sqlSelectPostCount       = `SELECT count(id) FROM posts WHERE hidden = FALSE AND cur_space=$1`
	sqlSelectAllUpdatedPosts = `
	SELECT
		posts.id,
		user_id,
		path,
		filename,
		slug,
		title,
		text,
		description,
		publish_at,
		app_users.name as username,
		posts.updated_at,
		posts.mime_type,
		0 AS "score"
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	WHERE hidden = FALSE AND publish_at::date <= CURRENT_DATE AND cur_space = $3
	ORDER BY updated_at DESC
	LIMIT $1 OFFSET $2`
	sqlSelectPostsByRank = `
	SELECT
		posts.id,
		user_id,
		path,
		filename,
		slug,
		title,
		text,
		description,
		publish_at,
		app_users.name as username,
		posts.updated_at,
		posts.mime_type,
		(
			LOG(2.0, COALESCE(NULLIF(posts.views, 0), 1)) / (
				EXTRACT(
					EPOCH FROM (STATEMENT_TIMESTAMP() - posts.publish_at)
				) / (14 * 8600)
			)
		) AS "score"
	FROM posts
	LEFT OUTER JOIN app_users ON app_users.id = posts.user_id
	WHERE
		hidden = FALSE AND
		publish_at::date <= CURRENT_DATE AND
		cur_space = $3
	ORDER BY score DESC
	LIMIT $1 OFFSET $2`

	sqlSelectPopularTags = `
	SELECT name, count(post_id) as "tally"
	FROM post_tags
	LEFT OUTER JOIN posts ON posts.id = post_id
	WHERE posts.cur_space = $1
	GROUP BY name
	ORDER BY tally DESC
	LIMIT 5`
	sqlSelectTagsForPost     = `SELECT name FROM post_tags WHERE post_id=$1`
	sqlSelectFeedItemsByPost = `SELECT id, post_id, guid, data, created_at FROM feed_items WHERE post_id=$1`

	sqlInsertPublicKey = `INSERT INTO public_keys (user_id, public_key) VALUES ($1, $2)`
	sqlInsertPost      = `
	INSERT INTO posts
		(user_id, path, filename, slug, title, text, description, publish_at, hidden, cur_space,
		file_size, mime_type, shasum, data, expires_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	RETURNING id`
	sqlInsertUser      = `INSERT INTO app_users DEFAULT VALUES returning id`
	sqlInsertTag       = `INSERT INTO post_tags (post_id, name) VALUES($1, $2) RETURNING id;`
	sqlInsertAliases   = `INSERT INTO post_aliases (post_id, slug) VALUES($1, $2) RETURNING id;`
	sqlInsertFeedItems = `INSERT INTO feed_items (post_id, guid, data) VALUES ($1, $2, $3) RETURNING id;`

	sqlUpdatePost = `
	UPDATE posts
	SET slug = $1, title = $2, text = $3, description = $4, updated_at = $5, publish_at = $6,
		file_size = $7, shasum = $8, data = $9
	WHERE id = $10`
	sqlUpdateUserName = `UPDATE app_users SET name = $1 WHERE id = $2`
	sqlIncrementViews = `UPDATE posts SET views = views + 1 WHERE id = $1 RETURNING views`

	sqlRemoveAliasesByPost = `DELETE FROM post_aliases WHERE post_id = $1`
	sqlRemoveTagsByPost    = `DELETE FROM post_tags WHERE post_id = $1`
	sqlRemovePosts         = `DELETE FROM posts WHERE id = ANY($1::uuid[])`
	sqlRemoveKeys          = `DELETE FROM public_keys WHERE id = ANY($1::uuid[])`
	sqlRemoveUsers         = `DELETE FROM app_users WHERE id = ANY($1::uuid[])`
)

type PsqlDB struct {
	Logger *zap.SugaredLogger
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
		&post.Path,
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
		&post.Path,
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

func NewDB(cfg *config.ConfigCms) *PsqlDB {
	databaseUrl := cfg.DbURL
	var err error
	d := &PsqlDB{
		Logger: cfg.Logger,
	}
	d.Logger.Infof("Connecting to postgres: %s", databaseUrl)

	db, err := sql.Open("postgres", databaseUrl)
	if err != nil {
		d.Logger.Fatal(err)
	}
	d.Db = db
	return d
}

func (me *PsqlDB) AddUser() (string, error) {
	var id string
	err := me.Db.QueryRow(sqlInsertUser).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (me *PsqlDB) RemoveUsers(userIDs []string) error {
	param := "{" + strings.Join(userIDs, ",") + "}"
	_, err := me.Db.Exec(sqlRemoveUsers, param)
	return err
}

func (me *PsqlDB) LinkUserKey(userID string, key string) error {
	_, err := me.Db.Exec(sqlInsertPublicKey, userID, key)
	return err
}

func (me *PsqlDB) FindPublicKeyForKey(key string) (*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Query(sqlSelectPublicKey, key)
	if err != nil {
		return nil, err
	}

	for rs.Next() {
		pk := &db.PublicKey{}
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Key, &pk.CreatedAt)
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

	// When we run PublicKeyForKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(keys) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
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
		err := rs.Scan(&pk.ID, &pk.UserID, &pk.Key, &pk.CreatedAt)
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

func (me *PsqlDB) FindSiteAnalytics(space string) (*db.Analytics, error) {
	analytics := &db.Analytics{}
	r := me.Db.QueryRow(sqlSelectTotalUsers)
	err := r.Scan(&analytics.TotalUsers)
	if err != nil {
		return nil, err
	}

	r = me.Db.QueryRow(sqlSelectTotalPosts, space)
	err = r.Scan(&analytics.TotalPosts)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	year, month, _ := now.Date()
	begMonth := time.Date(year, month, 1, 0, 0, 0, 0, now.Location())

	r = me.Db.QueryRow(sqlSelectTotalPostsAfterDate, begMonth, space)
	err = r.Scan(&analytics.PostsLastMonth)
	if err != nil {
		return nil, err
	}

	r = me.Db.QueryRow(sqlSelectUsersAfterDate, begMonth)
	err = r.Scan(&analytics.UsersLastMonth)
	if err != nil {
		return nil, err
	}

	r = me.Db.QueryRow(sqlSelectUsersWithPost, space)
	err = r.Scan(&analytics.UsersWithPost)
	if err != nil {
		return nil, err
	}

	return analytics, nil
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
	me.Logger.Infof("Attempting to find user with only public key (%s)", key)
	pk, err := me.FindPublicKeyForKey(key)
	if err == nil {
		user, err := me.FindUser(pk.UserID)
		if err != nil {
			return nil, err
		}
		user.PublicKey = pk
		return user, nil
	}

	if errors.Is(err, &db.ErrMultiplePublicKeys{}) {
		me.Logger.Infof("Detected multiple users with same public key, using ssh username (%s) to find correct one", username)
		user, err := me.FindUserForNameAndKey(username, key)
		if err != nil {
			me.Logger.Infof("Could not find user by username (%s) and public key (%s)", username, key)
			// this is a little hacky but if we cannot find a user by name and public key
			// then we return the multiple keys detected error so the user knows to specify their
			// when logging in
			return nil, &db.ErrMultiplePublicKeys{}
		}
		return user, nil
	}

	return nil, err
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
		return false, fmt.Errorf("%s is invalid: %w", lower, db.ErrNameDenied)
	}
	v := db.NameValidator.MatchString(lower)
	if !v {
		return false, fmt.Errorf("%s is invalid: %w", lower, db.ErrNameInvalid)
	}
	user, _ := me.FindUserForName(lower)
	if user == nil {
		return true, nil
	}
	return false, fmt.Errorf("%s is invalid: %w", lower, db.ErrNameTaken)
}

func (me *PsqlDB) FindUserForName(name string) (*db.User, error) {
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

func (me *PsqlDB) FindPostWithPath(fpath string, fname string, user_id string, space string) (*db.Post, error) {
	r := me.Db.QueryRow(sqlSelectPostWithPath, fpath, fname, user_id, space)
	post, err := CreatePostWithTagsFromRow(r)
	if err != nil {
		return nil, err
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
			&post.Path,
			&post.Filename,
			&post.Slug,
			&post.Title,
			&post.Text,
			&post.Description,
			&post.PublishAt,
			&post.Username,
			&post.UpdatedAt,
			&post.MimeType,
			&post.Score,
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
		post.Path,
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
		time.Now(),
		post.PublishAt,
		post.FileSize,
		post.Shasum,
		post.Data,
		post.ID,
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

func (me *PsqlDB) AddViewCount(postID string) (int, error) {
	views := 0
	err := me.Db.QueryRow(sqlIncrementViews, postID).Scan(&views)
	if err != nil {
		return views, err
	}
	return views, nil
}

func (me *PsqlDB) FindUsers() ([]*db.User, error) {
	var users []*db.User
	rs, err := me.Db.Query(sqlSelectUsers)
	if err != nil {
		return users, err
	}
	for rs.Next() {
		user := &db.User{}
		err := rs.Scan(
			&user.ID,
			&user.Name,
			&user.CreatedAt,
		)
		if err != nil {
			return users, err
		}

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
		"atom.xml",
		"feed.xml",
		"styles.css",
		"main.css",
		"prose.css",
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
			me.Logger.Infof(
				"(%s) is in the deny list for aliases because it conflicts with a static route, skipping",
				alias,
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

func (me *PsqlDB) HasFeatureForUser(userID string, feature string) bool {
	var id string
	err := me.Db.QueryRow(sqlSelectFeatureForUser, userID, feature).Scan(&id)
	if err != nil {
		return false
	}
	return id != ""
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
