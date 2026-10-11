package postgres

import (
	"slices"

	"github.com/jmoiron/sqlx"
)

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
