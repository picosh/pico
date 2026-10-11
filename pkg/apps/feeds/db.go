package feeds

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
	"github.com/picosh/pico/pkg/shared/router"
)

// DB is the storage feeds uses.
type DB interface {
	db.PostDB
	FindUser(userID string) (*db.User, error)
	FindPost(postID string) (*db.Post, error)
	FindUsersWithPost(space string) ([]*db.User, error)
	HasFeatureByUser(userID string, feature string) bool
	InsertFeedItems(postID string, items []*db.FeedItem) error
	FindFeedItemsByPostID(postID string) ([]*db.FeedItem, error)
}

// PsqlDB is feeds's postgres storage: the queries apps share, and the ones
// only feeds makes.
type PsqlDB struct {
	*postgres.PsqlDB
}

var _ DB = (*PsqlDB)(nil)

func NewDB(base *postgres.PsqlDB) *PsqlDB {
	return &PsqlDB{PsqlDB: base}
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

// getDB is the storage StartApiServer gave the router, which is a PsqlDB.
func getDB(r *http.Request) DB {
	return router.GetDB(r).(DB)
}
