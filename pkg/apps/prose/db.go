package prose

import (
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"

	"github.com/hashicorp/golang-lru/v2/expirable"
	"github.com/jmoiron/sqlx"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared/router"
)

// DB is the storage prose uses.
type DB interface {
	db.PostDB
	ReplaceTagsByPost(tags []string, postID string) error
	ReplaceAliasesByPost(aliases []string, postID string) error
	FindUserPostsByTag(pager *db.Pager, tag, userID, space string) (*db.Paginate[*db.Post], error)
	FindPopularPosts() ([]*db.Post, error)
	FindUserByName(name string) (*db.User, error)
	FindFeature(userID string, feature string) (*db.FeatureFlag, error)
}

// PsqlDB is prose's postgres storage: the queries apps share, and the ones
// only prose makes.
type PsqlDB struct {
	*postgres.PsqlDB

	popularPostsCache *expirable.LRU[string, []*db.Post]
	popularPostsMu    sync.Mutex
}

var _ DB = (*PsqlDB)(nil)

func NewDB(base *postgres.PsqlDB) *PsqlDB {
	return &PsqlDB{
		PsqlDB:            base,
		popularPostsCache: expirable.NewLRU[string, []*db.Post](16, nil, 15*time.Minute),
	}
}

func (me *PsqlDB) ClearPopularPostsCache() {
	if me.popularPostsCache != nil {
		me.popularPostsCache.Purge()
	}
}

func (me *PsqlDB) FindPopularPosts() ([]*db.Post, error) {
	if me.popularPostsCache != nil {
		if posts, ok := me.popularPostsCache.Get("popular_posts"); ok {
			return posts, nil
		}
	}

	me.popularPostsMu.Lock()
	defer me.popularPostsMu.Unlock()

	if me.popularPostsCache != nil {
		if posts, ok := me.popularPostsCache.Get("popular_posts"); ok {
			return posts, nil
		}
	}

	query := `
	WITH candidate_posts AS (
	    SELECT
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
	        AND publish_at >= NOW() - INTERVAL '30 days'
	        AND cur_space = 'prose'
	        AND EXISTS (
	            SELECT 1 FROM feature_flags
	            WHERE feature_flags.user_id = posts.user_id
	              AND (name = 'plus' OR name = 'prose')
	              AND expires_at > NOW()
	        )
	),
	post_visitors AS (
	    SELECT post_id, COUNT(DISTINCT ip_address) AS unique_visitors
	    FROM analytics_visits
	    WHERE post_id IN (SELECT id FROM candidate_posts)
	      AND status = 200
	      AND created_at >= NOW() - INTERVAL '30 days'
	    GROUP BY post_id
	),
	scored_posts AS (
	    SELECT
	        cp.*,
	        COALESCE(pv.unique_visitors, 0) AS score,
	        (
	            LOG(GREATEST(COALESCE(pv.unique_visitors, 0), 1)) +
	            (EXTRACT(EPOCH FROM cp.publish_at) - 1700000000) / 345600.0
	        ) AS rank_score,
	        ROW_NUMBER() OVER (
	            PARTITION BY cp.user_id
	            ORDER BY (
	                LOG(GREATEST(COALESCE(pv.unique_visitors, 0), 1)) +
	                (EXTRACT(EPOCH FROM cp.publish_at) - 1700000000) / 345600.0
	            ) DESC, cp.publish_at DESC
	        ) AS user_rank
	    FROM candidate_posts cp
	    LEFT JOIN post_visitors pv ON pv.post_id = cp.id
	)
	SELECT
	    id,
	    user_id,
	    filename,
	    slug,
	    title,
	    text,
	    description,
	    publish_at,
	    username,
	    updated_at,
	    mime_type,
	    score
	FROM scored_posts
	WHERE user_rank <= 3
	ORDER BY rank_score DESC, publish_at DESC
	LIMIT 30`
	rs, err := me.Db.Queryx(query)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rs.Close() }()

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

	if me.popularPostsCache != nil {
		me.popularPostsCache.Add("popular_posts", posts)
	}

	return posts, nil
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
	LIMIT $4 OFFSET $5`, postgres.SelectPost)
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

// getDB is the storage StartApiServer gave the router, which is a PsqlDB.
func getDB(r *http.Request) DB {
	return router.GetDB(r).(DB)
}
