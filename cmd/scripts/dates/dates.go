package main

import (
	"context"
	"database/sql"
	"log"
	"os"
	"time"

	"git.sr.ht/~erock/pico/db"
	"git.sr.ht/~erock/pico/db/postgres"
	"git.sr.ht/~erock/pico/lists"
	"git.sr.ht/~erock/pico/prose"
	"git.sr.ht/~erock/pico/wish/cms/config"
	"go.uber.org/zap"
)

func createLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}

func findPosts(dbpool *sql.DB) ([]*db.Post, error) {
	var posts []*db.Post
	rs, err := dbpool.Query(`SELECT
		id, user_id, filename, title, text, description,
		created_at, publish_at, updated_at, hidden, cur_space
		FROM posts
		WHERE cur_space = 'prose' OR cur_space = 'lists'
	`)
	if err != nil {
		return posts, err
	}
	for rs.Next() {
		post := &db.Post{}
		err := rs.Scan(
			&post.ID,
			&post.UserID,
			&post.Filename,
			&post.Title,
			&post.Text,
			&post.Description,
			&post.CreatedAt,
			&post.PublishAt,
			&post.UpdatedAt,
			&post.Hidden,
			&post.Space,
		)
		if err != nil {
			return posts, err
		}

		posts = append(posts, post)
	}
	if rs.Err() != nil {
		return posts, rs.Err()
	}
	return posts, nil
}

func updateDates(tx *sql.Tx, postID string, date *time.Time) error {
	_, err := tx.Exec("UPDATE posts SET publish_at = $1 WHERE id = $2", date, postID)
	return err
}

func main() {
	logger := createLogger()

	picoCfg := config.NewConfigCms()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoDb := postgres.NewDB(picoCfg)

	logger.Info("fetching all posts")
	posts, err := findPosts(picoDb.Db)
	if err != nil {
		panic(err)
	}
	logger.Infof("found (%d) posts", len(posts))

	ctx := context.Background()
	tx, err := picoDb.Db.BeginTx(ctx, nil)
	if err != nil {
		panic(err)
	}

	defer func() {
		err = tx.Rollback()
		panic(err)
	}()

	datesFixed := []string{}
	logger.Info("updating dates")
	for _, post := range posts {
		if post.Space == "prose" {
			parsed, err := prose.ParseText(post.Text)
			if err != nil {
				logger.Error(err)
				continue
			}

			if parsed.PublishAt != nil && !parsed.PublishAt.IsZero() {
				err = updateDates(tx, post.ID, parsed.MetaData.PublishAt)
				if err != nil {
					logger.Error(err)
					continue
				}

				if !parsed.MetaData.PublishAt.Equal(*post.PublishAt) {
					datesFixed = append(datesFixed, post.ID)
				}
			}
		} else if post.Space == "lists" {
			parsed := lists.ParseText(post.Text)

			if parsed.MetaData.PublishAt != nil && !parsed.MetaData.PublishAt.IsZero() {
				err = updateDates(tx, post.ID, parsed.MetaData.PublishAt)
				if err != nil {
					logger.Error(err)
					continue
				}
				if !parsed.MetaData.PublishAt.Equal(*post.PublishAt) {
					datesFixed = append(datesFixed, post.ID)
				}
			}
		}
	}

	err = tx.Commit()
	if err != nil {
		panic(err)
	}
	logger.Infof("(%d) dates fixed!", len(datesFixed))
}
