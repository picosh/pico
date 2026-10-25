package postgres

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/picosh/pico/pkg/db"
)

var SelectPost = `
	posts.id, user_id, app_users.name, filename, slug, title, text, description,
	posts.created_at, publish_at, posts.updated_at, hidden, file_size, mime_type, shasum, data, expires_at, views`

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

func (me *PsqlDB) RenamePost(cur, next, replaced *db.Post, tags, aliases []string) (*db.Post, error) {
	tx, err := me.Db.Beginx()
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	affected := func(res sql.Result, err error) error {
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return db.ErrPostChanged
		}
		return nil
	}

	// A save of the replaced post since it was read is kept.
	if replaced != nil {
		err := affected(tx.Exec(
			`DELETE FROM posts WHERE id = $1 AND filename = $2 AND text = $3 AND updated_at IS NOT DISTINCT FROM $4`,
			replaced.ID, replaced.Filename, replaced.Text, replaced.UpdatedAt,
		))
		if err != nil {
			return nil, err
		}
	}

	// The update only applies to the post as the caller read it, so a save
	// in between is never overwritten. It sets only what a new name can
	// change, which leaves data that other writers keep, like feed digests.
	query := `
	UPDATE posts
	SET filename = $1, slug = $2, title = $3, description = $4, publish_at = $5, hidden = $6,
		expires_at = $7, mime_type = $8
	WHERE id = $9 AND filename = $10 AND slug = $11 AND title = $12 AND text = $13
		AND description = $14 AND updated_at IS NOT DISTINCT FROM $15
		AND publish_at IS NOT DISTINCT FROM $16 AND hidden = $17
		AND expires_at IS NOT DISTINCT FROM $18`
	err = affected(tx.Exec(
		query,
		next.Filename,
		next.Slug,
		next.Title,
		next.Description,
		next.PublishAt,
		next.Hidden,
		next.ExpiresAt,
		next.MimeType,
		cur.ID,
		cur.Filename,
		cur.Slug,
		cur.Title,
		cur.Text,
		cur.Description,
		cur.UpdatedAt,
		cur.PublishAt,
		cur.Hidden,
		cur.ExpiresAt,
	))
	if err != nil {
		return nil, err
	}

	if err := me.removeTagsByPost(tx, cur.ID); err != nil {
		return nil, err
	}
	if _, err := me.insertTagsByPost(tx, tags, cur.ID); err != nil {
		return nil, err
	}
	if err := me.removeAliasesByPost(tx, cur.ID); err != nil {
		return nil, err
	}
	if _, err := me.insertAliasesByPost(tx, aliases, cur.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	return me.FindPost(cur.ID)
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

	return &db.Paginate[*db.Post]{Data: posts}, nil
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
