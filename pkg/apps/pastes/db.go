package pastes

import (
	"fmt"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/db/postgres"
)

// DB is the storage pastes uses.
type DB interface {
	db.PostDB
	FindUserByName(name string) (*db.User, error)
	FindExpiredPosts(space string) ([]*db.Post, error)
	FindFeature(userID string, feature string) (*db.FeatureFlag, error)
}

// PsqlDB is pastes's postgres storage: the queries apps share, and the ones
// only pastes makes.
type PsqlDB struct {
	*postgres.PsqlDB
}

var _ DB = (*PsqlDB)(nil)

func NewDB(base *postgres.PsqlDB) *PsqlDB {
	return &PsqlDB{PsqlDB: base}
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
	`, postgres.SelectPost)
	err := me.Db.Select(&posts, query, space)
	if err != nil {
		return nil, err
	}
	return posts, nil
}
