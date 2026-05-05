package postgres

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
)

// mobileUserAgentExpr is a SQL expression to detect mobile user agents.
const mobileUserAgentExpr = `user_agent ILIKE '%mobile%' OR user_agent ILIKE '%android%' OR user_agent ILIKE '%iphone%' OR user_agent ILIKE '%ipad%' OR user_agent ILIKE '%ipod%' OR user_agent ILIKE '%blackberry%' OR user_agent ILIKE '%windows phone%'`

var PAGER_SIZE = 15

var SelectPost = `
	posts.id, user_id, app_users.name, filename, slug, title, text, description,
	posts.created_at, publish_at, posts.updated_at, hidden, file_size, mime_type, shasum, data, expires_at, views`

type PsqlDB struct {
	Logger *slog.Logger
	Db     *sqlx.DB
}

type RowScanner interface {
	Scan(dest ...any) error
}

func CreatePostWithTagsByRow(r RowScanner) (*db.Post, error) {
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

	db, err := sqlx.Connect("postgres", databaseUrl)
	if err != nil {
		d.Logger.Error(err.Error())
	}
	d.Db = db
	return d
}

func (me *PsqlDB) RegisterUser(username, pubkey, comment string) (*db.User, error) {
	lowerName := strings.ToLower(username)
	valid, err := me.validateName(lowerName)
	if !valid {
		return nil, err
	}

	tx, err := me.Db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	var id string
	err = tx.QueryRow(`INSERT INTO app_users (name) VALUES($1) returning id`, lowerName).Scan(&id)
	if err != nil {
		return nil, err
	}

	err = me.insertPublicKeyWithTx(id, pubkey, comment, tx)
	if err != nil {
		return nil, err
	}

	err = tx.Commit()
	if err != nil {
		return nil, err
	}

	return me.FindUserByKey(username, pubkey)
}

func (me *PsqlDB) insertPublicKeyWithTx(userID, key, name string, tx *sqlx.Tx) error {
	pk, _ := me.findPublicKeyByKey(key)
	if pk != nil {
		return db.ErrPublicKeyTaken
	}
	query := `INSERT INTO public_keys (user_id, public_key, name) VALUES ($1, $2, $3)`
	_, err := tx.Exec(query, userID, key, name)
	return err
}

func (me *PsqlDB) InsertPublicKey(userID, key, name string) error {
	pk, _ := me.findPublicKeyByKey(key)
	if pk != nil {
		return db.ErrPublicKeyTaken
	}
	query := `INSERT INTO public_keys (user_id, public_key, name) VALUES ($1, $2, $3)`
	_, err := me.Db.Exec(query, userID, key, name)
	return err
}

func (me *PsqlDB) UpdatePublicKey(pubkeyID, name string) (*db.PublicKey, error) {
	pk, err := me.findPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}

	query := `UPDATE public_keys SET name=$1 WHERE id=$2;`
	_, err = me.Db.Exec(query, name, pk.ID)
	if err != nil {
		return nil, err
	}

	pk, err = me.findPublicKey(pubkeyID)
	if err != nil {
		return nil, err
	}
	return pk, nil
}

func (me *PsqlDB) findPublicKeyByKey(key string) (*db.PublicKey, error) {
	var keys []*db.PublicKey
	rs, err := me.Db.Queryx(`SELECT id, user_id, name, public_key, created_at FROM public_keys WHERE public_key = $1`, key)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

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

	// When we run PublicKeyByKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(keys) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
	}

	return keys[0], nil
}

func (me *PsqlDB) findPublicKey(pubkeyID string) (*db.PublicKey, error) {
	pk := &db.PublicKey{}
	err := me.Db.Get(pk, `SELECT * FROM public_keys WHERE id = $1`, pubkeyID)
	if err != nil {
		return nil, err
	}
	return pk, nil
}

func (me *PsqlDB) FindKeysByUser(user *db.User) ([]*db.PublicKey, error) {
	var keys []*db.PublicKey
	err := me.Db.Select(&keys, `SELECT * FROM public_keys WHERE user_id = $1 ORDER BY created_at ASC`, user.ID)
	if err != nil {
		return nil, err
	}
	return keys, nil
}

func (me *PsqlDB) RemoveKeys(keyIDs []string) error {
	param := "{" + strings.Join(keyIDs, ",") + "}"
	_, err := me.Db.Exec(`DELETE FROM public_keys WHERE id = ANY($1::uuid[])`, param)
	return err
}

func (me *PsqlDB) FindUsersWithPost(space string) ([]*db.User, error) {
	var users []*db.User
	rs, err := me.Db.Queryx(
		`SELECT u.id, u.name, u.created_at
		FROM app_users u
		INNER JOIN posts ON u.id=posts.user_id
		WHERE cur_space='feeds'
		GROUP BY u.id, u.name, u.created_at
		ORDER BY name ASC`,
	)
	if err != nil {
		return users, err
	}
	defer func() { _ = rs.Close() }()
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

func (me *PsqlDB) FindUserByKey(username string, key string) (*db.User, error) {
	me.Logger.Info("attempting to find user with only public key", "key", key)
	pk, err := me.findPublicKeyByKey(key)
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
		user, err := me.findUserForNameAndKey(username, key)
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
	pk, err := me.findPublicKeyByKey(key)
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
	err := me.Db.Get(user, `SELECT id, COALESCE(name, '') as name, created_at FROM app_users WHERE id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) validateName(name string) (bool, error) {
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
	err := me.Db.Get(user, `SELECT * FROM app_users WHERE name = $1`, strings.ToLower(name))
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) findUserForNameAndKey(name string, key string) (*db.User, error) {
	user := &db.User{}
	pk := &db.PublicKey{}

	r := me.Db.QueryRow(`SELECT app_users.id, app_users.name, app_users.created_at, public_keys.id as pk_id, public_keys.public_key, public_keys.created_at as pk_created_at FROM app_users LEFT JOIN public_keys ON public_keys.user_id = app_users.id WHERE app_users.name = $1 AND public_keys.public_key = $2`, strings.ToLower(name), key)
	err := r.Scan(&user.ID, &user.Name, &user.CreatedAt, &pk.ID, &pk.Key, &pk.CreatedAt)
	if err != nil {
		return nil, err
	}

	user.PublicKey = pk
	return user, nil
}

func (me *PsqlDB) FindUserByToken(token string) (*db.User, error) {
	user := &db.User{}
	err := me.Db.Get(user, `
	SELECT app_users.id, app_users.name, app_users.created_at
	FROM app_users
	LEFT JOIN tokens ON tokens.user_id = app_users.id
	WHERE tokens.token = $1 AND tokens.expires_at > NOW()`, token)
	if err != nil {
		return nil, err
	}
	return user, nil
}

func (me *PsqlDB) FindPostWithFilename(filename string, persona_id string, space string) (*db.Post, error) {
	query := fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE filename = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)
	r := me.Db.QueryRow(query, filename, persona_id, space)
	post, err := CreatePostWithTagsByRow(r)
	if err != nil {
		return nil, err
	}

	return post, nil
}

func (me *PsqlDB) FindPostWithSlug(slug string, user_id string, space string) (*db.Post, error) {
	query := fmt.Sprintf(`
	SELECT %s, STRING_AGG(coalesce(post_tags.name, ''), ',') tags
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE slug = $1 AND user_id = $2 AND cur_space = $3
	GROUP BY %s`, SelectPost, SelectPost)
	r := me.Db.QueryRow(query, slug, user_id, space)
	post, err := CreatePostWithTagsByRow(r)
	if err != nil {
		// attempt to find post inside post_aliases
		alias := me.Db.QueryRow(
			`SELECT post_aliases.post_id FROM post_aliases
			INNER JOIN posts ON posts.id = post_aliases.post_id
			WHERE post_aliases.slug = $1 AND posts.user_id = $2`,
			slug, user_id,
		)
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
	post := &db.Post{}
	query := fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE posts.id = $1`, SelectPost)
	err := me.Db.Get(post, query, postID)
	if err != nil {
		return nil, err
	}
	return post, nil
}

func (me *PsqlDB) postPager(rs *sqlx.Rows, pageNum int, space string, tag string) (*db.Paginate[*db.Post], error) {
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
		err = me.Db.QueryRow(`SELECT count(id) FROM posts WHERE hidden = FALSE AND cur_space=$1`, space).Scan(&count)
	} else {
		err = me.Db.QueryRow(`
	SELECT count(posts.id)
	FROM posts
	LEFT JOIN post_tags ON post_tags.post_id = posts.id
	WHERE hidden = FALSE AND cur_space=$1 and post_tags.name = $2`, space, tag).Scan(&count)
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

func (me *PsqlDB) FindPostsByFeed(page *db.Pager, space string) (*db.Paginate[*db.Post], error) {
	query := `
	SELECT *
	FROM (
	    SELECT DISTINCT ON (posts.user_id)
	        posts.id,
	        posts.user_id,
	        posts.filename,
	        posts.slug,
	        posts.title,
	        posts.text,
	        posts.description,
	        posts.publish_at,
	        app_users.name AS username,
	        posts.updated_at,
	        posts.mime_type
	    FROM posts
	    LEFT JOIN app_users ON app_users.id = posts.user_id
	    WHERE
	        hidden = FALSE
	        AND publish_at::date <= CURRENT_DATE
	        AND cur_space = $3
	    ORDER BY posts.user_id, publish_at DESC
	) AS latest_posts
	ORDER BY publish_at DESC
	LIMIT $1 OFFSET $2`
	rs, err := me.Db.Queryx(query, page.Num, page.Num*page.Page, space)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	return me.postPager(rs, page.Num, space, "")
}

func (me *PsqlDB) InsertPost(post *db.Post) (*db.Post, error) {
	var id string
	query := `
	INSERT INTO posts
		(user_id, filename, slug, title, text, description, publish_at, hidden, cur_space,
		file_size, mime_type, shasum, data, expires_at, updated_at)
	VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
	RETURNING id`
	err := me.Db.QueryRow(
		query,
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
	query := `
	UPDATE posts
	SET slug = $1, title = $2, text = $3, description = $4, updated_at = $5, publish_at = $6,
		file_size = $7, shasum = $8, data = $9, hidden = $11, expires_at = $12
	WHERE id = $10`
	_, err := me.Db.Exec(
		query,
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
	_, err := me.Db.Exec(`DELETE FROM posts WHERE id = ANY($1::uuid[])`, param)
	return err
}

func (me *PsqlDB) FindPostsByUser(page *db.Pager, userID string, space string) (*db.Paginate[*db.Post], error) {
	var posts []*db.Post
	query := fmt.Sprintf(`
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
	rs, err := me.Db.Queryx(
		query,
		userID,
		space,
		page.Num,
		page.Num*page.Page,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()
	for rs.Next() {
		post, err := CreatePostWithTagsByRow(rs)
		if err != nil {
			return nil, err
		}

		posts = append(posts, post)
	}

	if rs.Err() != nil {
		return nil, rs.Err()
	}

	var count int
	err = me.Db.QueryRow(`SELECT count(id) FROM posts WHERE hidden = FALSE AND cur_space=$1`, space).Scan(&count)
	if err != nil {
		return nil, err
	}

	pager := &db.Paginate[*db.Post]{
		Data:  posts,
		Total: int(math.Ceil(float64(count) / float64(page.Num))),
	}
	return pager, nil
}

func (me *PsqlDB) FindAllPostsByUser(userID string, space string) ([]*db.Post, error) {
	var posts []*db.Post
	query := fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id
	WHERE
		user_id = $1 AND
		cur_space = $2
	ORDER BY publish_at DESC`, SelectPost)
	err := me.Db.Select(&posts, query, userID, space)
	if err != nil {
		return nil, err
	}
	return posts, nil
}

func (me *PsqlDB) FindPosts() ([]*db.Post, error) {
	var posts []*db.Post
	query := fmt.Sprintf(`
	SELECT %s
	FROM posts
	LEFT JOIN app_users ON app_users.id = posts.user_id`, SelectPost)
	err := me.Db.Select(&posts, query)
	if err != nil {
		return nil, err
	}
	return posts, nil
}

func (me *PsqlDB) FindExpiredPosts(space string) ([]*db.Post, error) {
	var posts []*db.Post
	query := fmt.Sprintf(`
		SELECT %s
		FROM posts
		LEFT JOIN app_users ON app_users.id = posts.user_id
		WHERE
			cur_space = $1 AND
			expires_at <= now();
	`, SelectPost)
	err := me.Db.Select(&posts, query, space)
	if err != nil {
		return nil, err
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
	var intervals, currentIntervals []*db.VisitInterval
	var sumErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		intervals, sumErr = me.visitUniqueFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		currentIntervals, rawErr = me.visitUniqueFromRaw(opts)
	}()

	wg.Wait()

	if sumErr != nil {
		return nil, fmt.Errorf("query summary visits: %w", sumErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw visits: %w", rawErr)
	}

	// Merge: current month data may overlap with summary data, combine counts
	return mergeVisitIntervals(intervals, currentIntervals), nil
}

// visitUniqueFromSummary reads unique visitor counts from analytics_monthly_visits for historical data.
func (me *PsqlDB) visitUniqueFromSummary(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	where := ""
	args := []interface{}{opts.UserID, opts.Origin, currentMonthStart}
	argIdx := 4
	if opts.Host != "" {
		where = "AND host = $" + fmt.Sprintf("%d", argIdx)
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT
			date_trunc('%s', visit_date)::timestamptz as interval_start,
			sum(unique_visits) as unique_visitors,
			sum(mobile_visits) as mobile_visits,
			sum(desktop_visits) as desktop_visits
		FROM analytics_monthly_visits
		WHERE user_id = $1 AND visit_date >= $2 AND visit_date < $3 %s
		GROUP BY interval_start
		ORDER BY interval_start`, opts.Interval, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var intervals []*db.VisitInterval
	for rows.Next() {
		interval := &db.VisitInterval{}
		if err := rows.Scan(&interval.Interval, &interval.Visitors, &interval.MobileVisitors, &interval.DesktopVisitors); err != nil {
			return nil, err
		}
		intervals = append(intervals, interval)
	}
	return intervals, rows.Err()
}

// visitUniqueFromRaw reads unique visitor counts from analytics_visits for the previous and current months.
// This covers the gap between the last aggregated month and the current month.
func (me *PsqlDB) visitUniqueFromRaw(opts *db.SummaryOpts) ([]*db.VisitInterval, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	uniqueVisitors := fmt.Sprintf(`
		SELECT
			date_trunc('%s', created_at)::timestamptz as interval_start,
			count(DISTINCT CASE WHEN %s THEN ip_address END) as mobile_visitors,
			count(DISTINCT CASE WHEN NOT %s THEN ip_address END) as desktop_visitors,
			count(DISTINCT ip_address) as unique_visitors
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND status <> 404
		GROUP BY interval_start
		ORDER BY interval_start`,
		opts.Interval, mobileUserAgentExpr, mobileUserAgentExpr, where)

	rows, err := me.Db.Queryx(uniqueVisitors, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var intervals []*db.VisitInterval
	for rows.Next() {
		interval := &db.VisitInterval{}
		if err := rows.Scan(&interval.Interval, &interval.MobileVisitors, &interval.DesktopVisitors, &interval.Visitors); err != nil {
			return nil, err
		}
		intervals = append(intervals, interval)
	}
	return intervals, rows.Err()
}

// mergeVisitIntervals combines historical (summary table) and current (raw) intervals.
// Summary data is preferred when both sources have the same interval to avoid double-counting.
func mergeVisitIntervals(historical, current []*db.VisitInterval) []*db.VisitInterval {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by interval timestamp for merging.
	// Summary data takes precedence over raw data for the same interval
	// (e.g. when a month is aggregated but raw data still exists in a local dump).
	intervalMap := make(map[int64]*db.VisitInterval)
	for _, ci := range current {
		ts := ci.Interval.Unix()
		intervalMap[ts] = ci
	}
	for _, hi := range historical {
		ts := hi.Interval.Unix()
		intervalMap[ts] = hi // summary overwrites raw if both exist
	}

	// Sort by interval
	result := make([]*db.VisitInterval, 0, len(intervalMap))
	for _, iv := range intervalMap {
		result = append(result, iv)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Interval.Before(*result[j].Interval)
	})
	return result
}

// mergeTopUrls combines historical and current top URLs, summing counts for overlapping URLs,
// then returns the top 10 by total count.
func mergeTopUrls(historical, current []*db.VisitUrl) []*db.VisitUrl {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by URL for merging
	urlMap := make(map[string]*db.VisitUrl)
	for _, hu := range historical {
		urlMap[hu.Url] = hu
	}

	for _, cu := range current {
		if existing, ok := urlMap[cu.Url]; ok {
			existing.Count += cu.Count
		} else {
			urlMap[cu.Url] = cu
		}
	}

	// Sort by count descending and take top 10
	result := make([]*db.VisitUrl, 0, len(urlMap))
	for _, u := range urlMap {
		result = append(result, u)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	if len(result) > 10 {
		result = result[:10]
	}
	return result
}

// mergeTopReferers combines historical and current top referers, summing counts for overlapping referers,
// then returns the top 10 by total count.
func mergeTopReferers(historical, current []*db.VisitUrl) []*db.VisitUrl {
	return mergeTopUrls(historical, current) // Same logic as mergeTopUrls
}

// mergeHosts combines historical and current hosts, summing counts for overlapping hosts,
// then returns sorted by total count descending.
func mergeHosts(historical, current []*db.VisitUrl) []*db.VisitUrl {
	if len(historical) == 0 {
		return current
	}
	if len(current) == 0 {
		return historical
	}

	// Build a map by host for merging
	hostMap := make(map[string]*db.VisitUrl)
	for _, hu := range historical {
		hostMap[hu.Url] = hu
	}

	for _, cu := range current {
		if existing, ok := hostMap[cu.Url]; ok {
			existing.Count += cu.Count
		} else {
			hostMap[cu.Url] = cu
		}
	}

	// Sort by count descending
	result := make([]*db.VisitUrl, 0, len(hostMap))
	for _, h := range hostMap {
		result = append(result, h)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})
	return result
}

func (me *PsqlDB) visitReferer(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitRefererFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitRefererFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary referers: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw referers: %w", rawErr)
	}

	return mergeTopReferers(historical, current), nil
}

// visitRefererFromSummary reads top referers from analytics_monthly_top_referers for historical data.
func (me *PsqlDB) visitRefererFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	if opts.Host != "" {
		where = "AND host = $4"
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT referer, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_referers
		WHERE user_id = $1 AND month >= $2 AND month < $3 %s
		GROUP BY referer
		ORDER BY total_visits DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitRefererFromRaw reads top referers from analytics_visits for the previous and current months.
func (me *PsqlDB) visitRefererFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			referer,
			count(DISTINCT ip_address) as referer_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND referer <> '' AND status <> 404
		GROUP BY referer
		ORDER BY referer_count DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) visitUrl(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitUrlFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitUrlFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary urls: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw urls: %w", rawErr)
	}

	return mergeTopUrls(historical, current), nil
}

// visitUrlFromSummary reads top URLs from analytics_monthly_top_urls for historical data.
func (me *PsqlDB) visitUrlFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	if opts.Host != "" {
		where = "AND host = $4"
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT path, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_urls
		WHERE user_id = $1 AND month >= $2 AND month < $3 AND status_code <> 404 %s
		GROUP BY path
		ORDER BY total_visits DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitUrlFromRaw reads top URLs from analytics_visits for the previous and current months.
func (me *PsqlDB) visitUrlFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			path,
			count(DISTINCT ip_address) as path_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND path <> '' AND status <> 404
		GROUP BY path
		ORDER BY path_count DESC
		LIMIT 10`, where)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) VisitUrlNotFound(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	limit := opts.Limit
	if limit == 0 {
		limit = 10
	}

	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitUrlNotFoundFromSummary(opts, limit)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitUrlNotFoundFromRaw(opts, limit)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary 404 urls: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw 404 urls: %w", rawErr)
	}

	return mergeTopUrls(historical, current), nil
}

// visitUrlNotFoundFromSummary reads top 404 URLs from analytics_monthly_top_urls for historical data.
func (me *PsqlDB) visitUrlNotFoundFromSummary(opts *db.SummaryOpts, limit int) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	// If origin is in the previous month or later, raw data covers it — no summary to fetch.
	if !opts.Origin.Before(previousMonthStart) {
		return nil, nil
	}

	// Clamp origin to month boundary for summary table lookup
	originMonthStart := time.Date(opts.Origin.Year(), opts.Origin.Month(), 1, 0, 0, 0, 0, time.UTC)

	where := ""
	args := []interface{}{opts.UserID, originMonthStart, currentMonthStart}
	argIdx := 4
	if opts.Host != "" {
		where = "AND host = $" + fmt.Sprintf("%d", argIdx)
		args = append(args, opts.Host)
	}

	query := fmt.Sprintf(`
		SELECT path, sum(unique_visits) as total_visits
		FROM analytics_monthly_top_urls
		WHERE user_id = $1 AND month >= $2 AND month < $3 AND status_code = 404 %s
		GROUP BY path
		ORDER BY total_visits DESC
		LIMIT %d`, where, limit)

	rows, err := me.Db.Queryx(query, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitUrlNotFoundFromRaw reads top 404 URLs from analytics_visits for the previous and current months.
func (me *PsqlDB) visitUrlNotFoundFromRaw(opts *db.SummaryOpts, limit int) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	previousMonthStart := currentMonthStart.AddDate(0, -1, 0)

	where, with := visitFilterBy(opts)

	// Determine the effective start: max(origin, previousMonthStart)
	effectiveStart := previousMonthStart
	if opts.Origin.After(previousMonthStart) {
		effectiveStart = opts.Origin
	}

	topUrls := fmt.Sprintf(`
		SELECT
			path,
			count(DISTINCT ip_address) as path_count
		FROM analytics_visits
		WHERE created_at >= $1 AND created_at < $2 AND %s = $3 AND user_id = $4 AND path <> '' AND status = 404
		GROUP BY path
		ORDER BY path_count DESC
		LIMIT %d`, where, limit)

	rows, err := me.Db.Queryx(topUrls, effectiveStart, currentMonthStart.AddDate(0, 1, 0), with, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) visitHost(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	var historical, current []*db.VisitUrl
	var histErr, rawErr error

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		historical, histErr = me.visitHostFromSummary(opts)
	}()
	go func() {
		defer wg.Done()
		current, rawErr = me.visitHostFromRaw(opts)
	}()

	wg.Wait()

	if histErr != nil {
		return nil, fmt.Errorf("query summary hosts: %w", histErr)
	}
	if rawErr != nil {
		return nil, fmt.Errorf("query raw hosts: %w", rawErr)
	}

	return mergeHosts(historical, current), nil
}

// visitHostFromSummary reads host data from analytics_user_sites for historical data.
func (me *PsqlDB) visitHostFromSummary(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	rows, err := me.Db.Queryx(`
		SELECT host, total_visits
		FROM analytics_user_sites
		WHERE user_id = $1 AND host <> ''
		ORDER BY total_visits DESC`, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

// visitHostFromRaw reads hosts from analytics_visits for the current month that aren't in summary.
func (me *PsqlDB) visitHostFromRaw(opts *db.SummaryOpts) ([]*db.VisitUrl, error) {
	now := time.Now()
	currentMonthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)

	rows, err := me.Db.Queryx(`
		SELECT host, count(DISTINCT ip_address) as host_count
		FROM analytics_visits
		WHERE created_at >= $1 AND user_id = $2 AND host <> ''
		GROUP BY host
		ORDER BY host_count DESC`, currentMonthStart, opts.UserID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var results []*db.VisitUrl
	for rows.Next() {
		result := &db.VisitUrl{}
		if err := rows.Scan(&result.Url, &result.Count); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, rows.Err()
}

func (me *PsqlDB) VisitSummary(opts *db.SummaryOpts) (*db.SummaryVisits, error) {
	var (
		visitors    []*db.VisitInterval
		urls        []*db.VisitUrl
		refs        []*db.VisitUrl
		notFound    []*db.VisitUrl
		visitorsErr error
		urlsErr     error
		refsErr     error
		nfErr       error
	)

	var wg sync.WaitGroup
	wg.Add(4)

	go func() {
		defer wg.Done()
		visitors, visitorsErr = me.visitUnique(opts)
	}()
	go func() {
		defer wg.Done()
		urls, urlsErr = me.visitUrl(opts)
	}()
	go func() {
		defer wg.Done()
		refs, refsErr = me.visitReferer(opts)
	}()
	go func() {
		defer wg.Done()
		notFound, nfErr = me.VisitUrlNotFound(opts)
	}()

	wg.Wait()

	// Return the first error encountered
	for _, err := range []error{visitorsErr, urlsErr, refsErr, nfErr} {
		if err != nil {
			return nil, err
		}
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
	err := me.Db.Select(&users, `SELECT id, COALESCE(name, '') as name, created_at FROM app_users ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	return users, nil
}

func (me *PsqlDB) removeTagsByPost(tx *sqlx.Tx, postID string) error {
	_, err := tx.Exec(`DELETE FROM post_tags WHERE post_id = $1`, postID)
	return err
}

func (me *PsqlDB) insertTagsByPost(tx *sqlx.Tx, tags []string, postID string) ([]string, error) {
	ids := make([]string, 0)
	for _, tag := range tags {
		id := ""
		err := tx.QueryRow(`INSERT INTO post_tags (post_id, name) VALUES($1, $2) RETURNING id;`, postID, tag).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

func (me *PsqlDB) ReplaceTagsByPost(tags []string, postID string) error {
	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	err = me.removeTagsByPost(tx, postID)
	if err != nil {
		return err
	}

	_, err = me.insertTagsByPost(tx, tags, postID)
	if err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) removeAliasesByPost(tx *sqlx.Tx, postID string) error {
	_, err := tx.Exec(`DELETE FROM post_aliases WHERE post_id = $1`, postID)
	return err
}

func (me *PsqlDB) insertAliasesByPost(tx *sqlx.Tx, aliases []string, postID string) ([]string, error) {
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
		err := tx.QueryRow(`INSERT INTO post_aliases (post_id, slug) VALUES($1, $2) RETURNING id;`, postID, alias).Scan(&id)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}

	return ids, nil
}

func (me *PsqlDB) ReplaceAliasesByPost(aliases []string, postID string) error {
	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	err = me.removeAliasesByPost(tx, postID)
	if err != nil {
		return err
	}

	_, err = me.insertAliasesByPost(tx, aliases, postID)
	if err != nil {
		return err
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) FindUserPostsByTag(page *db.Pager, tag, userID, space string) (*db.Paginate[*db.Post], error) {
	var posts []*db.Post
	query := fmt.Sprintf(`
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
	err := me.Db.Select(
		&posts,
		query,
		userID,
		tag,
		space,
		page.Num,
		page.Num*page.Page,
	)
	if err != nil {
		return nil, err
	}

	var count int
	err = me.Db.QueryRow(`SELECT count(id) FROM posts WHERE hidden = FALSE AND cur_space=$1`, space).Scan(&count)
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
	query := `
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
	rs, err := me.Db.Queryx(
		query,
		pager.Num,
		pager.Num*pager.Page,
		tag,
		space,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

	return me.postPager(rs, pager.Num, space, tag)
}

func (me *PsqlDB) FindPopularTags(space string) ([]string, error) {
	tags := make([]string, 0)
	query := `
	SELECT name, count(post_id) as "tally"
	FROM post_tags
	LEFT JOIN posts ON posts.id = post_id
	WHERE posts.cur_space = $1
	GROUP BY name
	ORDER BY tally DESC
	LIMIT 5`
	rs, err := me.Db.Queryx(query, space)
	if err != nil {
		return tags, err
	}
	defer func() { _ = rs.Close() }()
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

func (me *PsqlDB) FindFeature(userID string, feature string) (*db.FeatureFlag, error) {
	ff := &db.FeatureFlag{}
	err := me.Db.Get(ff, `SELECT * FROM feature_flags WHERE user_id = $1 AND name = $2 ORDER BY expires_at DESC LIMIT 1`, userID, feature)
	if err != nil {
		return nil, err
	}
	return ff, nil
}

func (me *PsqlDB) FindFeaturesByUser(userID string) ([]*db.FeatureFlag, error) {
	var features []*db.FeatureFlag
	// https://stackoverflow.com/a/16920077
	query := `SELECT DISTINCT ON (name) *
		FROM feature_flags
		WHERE user_id=$1
		ORDER BY name, expires_at DESC;`
	err := me.Db.Select(&features, query, userID)
	if err != nil {
		return nil, err
	}
	return features, nil
}

func (me *PsqlDB) HasFeatureByUser(userID string, feature string) bool {
	ff, err := me.FindFeature(userID, feature)
	if err != nil {
		return false
	}
	return ff.IsValid()
}

func (me *PsqlDB) InsertFeedItems(postID string, items []*db.FeedItem) error {
	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	for _, item := range items {
		_, err := tx.Exec(
			`INSERT INTO feed_items (post_id, guid, data) VALUES ($1, $2, $3) RETURNING id;`,
			item.PostID,
			item.GUID,
			item.Data,
		)
		if err != nil {
			return fmt.Errorf(
				"post id:%s, link:%s, guid:%s, err:%w",
				item.PostID, item.Data.Link, item.GUID, err,
			)
		}
	}

	err = tx.Commit()
	return err
}

func (me *PsqlDB) FindFeedItemsByPostID(postID string) ([]*db.FeedItem, error) {
	var items []*db.FeedItem
	err := me.Db.Select(&items, `SELECT * FROM feed_items WHERE post_id=$1`, postID)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (me *PsqlDB) InsertProject(userID, name, projectDir string) (string, error) {
	if !shared.IsValidSubdomain(name) {
		return "", fmt.Errorf("'%s' is not a valid project name, must match /^[a-z0-9-]+$/", name)
	}

	var id string
	err := me.Db.QueryRow(`INSERT INTO projects (user_id, name, project_dir) VALUES ($1, $2, $3) RETURNING id;`, userID, name, projectDir).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (me *PsqlDB) UpdateProject(userID, name string) error {
	_, err := me.Db.Exec(`UPDATE projects SET updated_at = $3 WHERE user_id = $1 AND name = $2;`, userID, name, time.Now())
	return err
}

func (me *PsqlDB) FindProjectByName(userID, name string) (*db.Project, error) {
	project := &db.Project{}
	err := me.Db.Get(project, `SELECT * FROM projects WHERE user_id = $1 AND name = $2;`, userID, name)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func (me *PsqlDB) InsertToken(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(`INSERT INTO tokens (user_id, name) VALUES($1, $2) RETURNING token;`, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) UpsertToken(userID, name string) (string, error) {
	token, _ := me.findTokenByName(userID, name)
	if token != "" {
		return token, nil
	}

	token, err := me.InsertToken(userID, name)
	return token, err
}

func (me *PsqlDB) findTokenByName(userID, name string) (string, error) {
	var token string
	err := me.Db.QueryRow(`SELECT token FROM tokens WHERE user_id = $1 AND name = $2`, userID, name).Scan(&token)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (me *PsqlDB) RemoveToken(tokenID string) error {
	_, err := me.Db.Exec(`DELETE FROM tokens WHERE id = $1`, tokenID)
	return err
}

func (me *PsqlDB) FindTokensByUser(userID string) ([]*db.Token, error) {
	var tokens []*db.Token
	err := me.Db.Select(&tokens, `SELECT * FROM tokens WHERE user_id = $1`, userID)
	if err != nil {
		return nil, err
	}
	return tokens, nil
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
	// if the feature flag has already expired we don't want to add a year to it since that will
	// not grant the user a full year
	if ff == nil || !ff.IsValid() {
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

	tx, err := me.Db.Beginx()
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
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
	VALUES ($1, 'plus', '{"storage_max":10000000000, "file_max":100000000, "email": "%s"}'::jsonb, $2, $3);`, email)
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
	var logs []*db.TunsEventLog
	err := me.Db.Select(&logs,
		`SELECT * FROM tuns_event_logs WHERE user_id=$1 AND tunnel_id=$2 ORDER BY created_at DESC`, userID, addr)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindTunsEventLogs(userID string) ([]*db.TunsEventLog, error) {
	var logs []*db.TunsEventLog
	err := me.Db.Select(&logs,
		`SELECT * FROM tuns_event_logs WHERE user_id=$1 ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindUserStats(userID string) (*db.UserStats, error) {
	stats := db.UserStats{}
	rs, err := me.Db.Queryx(`SELECT cur_space, count(id), min(created_at), max(created_at), max(updated_at) FROM posts WHERE user_id=$1 GROUP BY cur_space`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

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

func (me *PsqlDB) FindAccessLogs(userID string, fromDate *time.Time) ([]*db.AccessLog, error) {
	var logs []*db.AccessLog
	err := me.Db.Select(&logs, `SELECT * FROM access_logs WHERE user_id=$1 AND created_at >= $2 ORDER BY created_at ASC`, userID, fromDate)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindAccessLogsByPubkey(pubkey string, fromDate *time.Time) ([]*db.AccessLog, error) {
	var logs []*db.AccessLog
	err := me.Db.Select(&logs, `SELECT * FROM access_logs WHERE pubkey=$1 AND created_at >= $2 ORDER BY created_at ASC`, pubkey, fromDate)
	if err != nil {
		return nil, err
	}
	return logs, nil
}

func (me *PsqlDB) FindPubkeysInAccessLogs(userID string) ([]string, error) {
	var pubkeys []string
	err := me.Db.Select(&pubkeys, `SELECT DISTINCT(pubkey) FROM access_logs WHERE user_id=$1`, userID)
	if err != nil {
		return nil, err
	}
	return pubkeys, nil
}

func (me *PsqlDB) InsertAccessLog(log *db.AccessLog) error {
	_, err := me.Db.Exec(
		`INSERT INTO access_logs (user_id, service, pubkey, identity) VALUES ($1, $2, $3, $4);`,
		log.UserID,
		log.Service,
		log.Pubkey,
		log.Identity,
	)
	return err
}

func (me *PsqlDB) UpsertPipeMonitor(userID, topic string, dur time.Duration, winEnd *time.Time) error {
	durStr := fmt.Sprintf("%d seconds", int64(dur.Seconds()))
	_, err := me.Db.Exec(
		`INSERT INTO pipe_monitors (user_id, topic, window_dur, window_end)
		VALUES ($1, $2, $3::interval, $4)
		ON CONFLICT (user_id, topic) DO UPDATE SET window_dur = $3::interval, window_end = $4, updated_at = NOW();`,
		userID,
		topic,
		durStr,
		winEnd,
	)
	return err
}

func (me *PsqlDB) UpdatePipeMonitorLastPing(userID, topic string, lastPing *time.Time) error {
	_, err := me.Db.Exec(
		`UPDATE pipe_monitors SET last_ping = $3, updated_at = NOW() WHERE user_id = $1 AND topic = $2;`,
		userID,
		topic,
		lastPing,
	)
	return err
}

func (me *PsqlDB) RemovePipeMonitor(userID, topic string) error {
	_, err := me.Db.Exec(
		`DELETE FROM pipe_monitors WHERE user_id = $1 AND topic = $2;`,
		userID,
		topic,
	)
	return err
}

func (me *PsqlDB) FindPipeMonitorByTopic(userID, topic string) (*db.PipeMonitor, error) {
	monitor := &db.PipeMonitor{}
	err := me.Db.Get(monitor, `SELECT id, user_id, topic, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors WHERE user_id = $1 AND topic = $2;`, userID, topic)
	if err != nil {
		return nil, err
	}
	return monitor, nil
}

func (me *PsqlDB) FindPipeMonitorsByUser(userID string) ([]*db.PipeMonitor, error) {
	var monitors []*db.PipeMonitor
	err := me.Db.Select(&monitors, `SELECT id, user_id, topic, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors WHERE user_id = $1 ORDER BY topic;`, userID)
	if err != nil {
		return nil, err
	}
	return monitors, nil
}

func (me *PsqlDB) InsertPipeMonitorHistory(monitorID string, windowDur time.Duration, windowEnd, lastPing *time.Time) error {
	durStr := fmt.Sprintf("%d seconds", int64(windowDur.Seconds()))
	_, err := me.Db.Exec(
		`INSERT INTO pipe_monitors_history (monitor_id, window_dur, window_end, last_ping) VALUES ($1, $2::interval, $3, $4)`,
		monitorID, durStr, windowEnd, lastPing,
	)
	return err
}

func (me *PsqlDB) FindPipeMonitorHistory(monitorID string, from, to time.Time) ([]*db.PipeMonitorHistory, error) {
	var history []*db.PipeMonitorHistory
	err := me.Db.Select(
		&history,
		`SELECT id, monitor_id, (EXTRACT(EPOCH FROM window_dur) * 1000000000)::bigint as window_dur, window_end, last_ping, created_at, updated_at FROM pipe_monitors_history WHERE monitor_id = $1 AND last_ping <= $2 AND window_end >= $3 ORDER BY last_ping ASC`,
		monitorID, to, from,
	)
	if err != nil {
		return nil, err
	}
	return history, nil
}
