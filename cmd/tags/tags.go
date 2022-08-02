package main

import (
	"database/sql"
	"log"
	"os"

	"git.sr.ht/~erock/pico/prose"
	"git.sr.ht/~erock/pico/wish/cms/config"
	"git.sr.ht/~erock/pico/wish/cms/db"
	"git.sr.ht/~erock/pico/wish/cms/db/postgres"
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
		posts.id, user_id, filename, title, text, description,
		posts.created_at, publish_at, posts.updated_at, hidden, COALESCE(views, 0) as views
		FROM posts
		WHERE cur_space = 'prose'
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
			&post.Views,
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

	logger.Info("replacing tags")
	for _, post := range posts {
		parsed, err := prose.ParseText(post.Text)
		if err != nil {
			continue
		}
		if len(parsed.Tags) > 0 {
			picoDb.ReplaceTagsForPost(parsed.Tags, post.ID)
		}
	}
}
