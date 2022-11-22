package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/wish/cms/config"
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
		LEFT OUTER JOIN post_analytics ON post_analytics.post_id = posts.id
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

func insertUser(tx *sql.Tx, user *db.User) error {
	_, err := tx.Exec(
		"INSERT INTO app_users (id, name, created_at) VALUES($1, $2, $3)",
		user.ID,
		user.Name,
		user.CreatedAt,
	)
	return err
}

func insertPublicKey(tx *sql.Tx, pk *db.PublicKey) error {
	_, err := tx.Exec(
		"INSERT INTO public_keys (id, user_id, public_key, created_at) VALUES ($1, $2, $3, $4)",
		pk.ID,
		pk.UserID,
		pk.Key,
		pk.CreatedAt,
	)
	return err
}

func insertPost(tx *sql.Tx, post *db.Post) error {
	_, err := tx.Exec(
		`INSERT INTO posts
			(id, user_id, title, text, created_at, publish_at, updated_at, description, filename, hidden, cur_space, views)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
		post.ID,
		post.UserID,
		post.Title,
		post.Text,
		post.CreatedAt,
		post.PublishAt,
		post.UpdatedAt,
		post.Description,
		post.Filename,
		post.Hidden,
		post.Space,
		post.Views,
	)
	return err
}

type ConflictData struct {
	User          *db.User
	Pks           []*db.PublicKey
	ReplaceWithID string
}

func main() {
	logger := createLogger()

	listsCfg := config.NewConfigCms()
	listsCfg.Logger = logger
	listsCfg.DbURL = os.Getenv("LISTS_DB_URL")
	listsDb := postgres.NewDB(listsCfg)

	proseCfg := config.NewConfigCms()
	proseCfg.DbURL = os.Getenv("PROSE_DB_URL")
	proseCfg.Logger = logger
	proseDb := postgres.NewDB(proseCfg)

	picoCfg := config.NewConfigCms()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("PICO_DB_URL")
	picoDb := postgres.NewDB(picoCfg)

	ctx := context.Background()
	tx, err := picoDb.Db.BeginTx(ctx, nil)
	if err != nil {
		panic(err)
	}
	defer func() {
		err = tx.Rollback()
	}()

	logger.Info("Finding prose users")
	proseUsers, err := proseDb.FindUsers()
	if err != nil {
		panic(err)
	}

	logger.Info("Finding lists users")
	listUsers, err := listsDb.FindUsers()
	if err != nil {
		panic(err)
	}

	logger.Info("Adding prose users and public keys to PICO db")
	userMap := map[string]*db.User{}
	for _, proseUser := range proseUsers {
		userMap[proseUser.Name] = proseUser

		err = insertUser(tx, proseUser)
		if err != nil {
			panic(err)
		}

		proseKeys, err := proseDb.FindKeysForUser(proseUser)
		if err != nil {
			panic(err)
		}

		for _, prosePK := range proseKeys {
			err = insertPublicKey(tx, prosePK)
			if err != nil {
				panic(err)
			}
		}
	}

	noconflicts := []*ConflictData{}
	conflicts := []*ConflictData{}
	updateIDs := []*ConflictData{}
	logger.Info("Finding conflicts")
	for _, listUser := range listUsers {
		listKeys, err := listsDb.FindKeysForUser(listUser)
		if err != nil {
			panic(err)
		}

		data := &ConflictData{
			User: listUser,
			Pks:  listKeys,
		}

		if userMap[listUser.Name] == nil {
			noconflicts = append(noconflicts, data)
			continue
		} else {
			proseUser := userMap[listUser.Name]
			proseKeys, err := proseDb.FindKeysForUser(proseUser)
			if err != nil {
				panic(err)
			}

			if len(listKeys) != len(proseKeys) {
				conflicts = append(conflicts, data)
				continue
			}

			pkMap := map[string]bool{}
			for _, prosePK := range proseKeys {
				pkMap[prosePK.Key] = true
			}

			conflicted := false
			for _, listPK := range listKeys {
				if !pkMap[listPK.Key] {
					conflicted = true
					conflicts = append(conflicts, data)
					break
				}
			}

			if !conflicted {
				data.ReplaceWithID = proseUser.ID
				updateIDs = append(updateIDs, data)
			}
		}
	}

	logger.Infof("Adding records with no conflicts (%d)", len(noconflicts))
	for _, data := range noconflicts {
		err = insertUser(tx, data.User)
		if err != nil {
			panic(err)
		}

		for _, pk := range data.Pks {
			err = insertPublicKey(tx, pk)
			if err != nil {
				panic(err)
			}
		}
	}

	logger.Infof("Adding records with conflicts (%d)", len(conflicts))
	for _, data := range conflicts {
		data.User.Name = fmt.Sprintf("%stmp", data.User.Name)
		err = insertUser(tx, data.User)
		if err != nil {
			panic(err)
		}

		for _, pk := range data.Pks {
			err = insertPublicKey(tx, pk)
			if err != nil {
				panic(err)
			}
		}
	}

	prosePosts, err := findPosts(proseDb.Db)
	if err != nil {
		panic(err)
	}

	logger.Info("Adding posts from prose.sh")
	for _, post := range prosePosts {
		post.Space = "prose"
		err = insertPost(tx, post)
		if err != nil {
			panic(err)
		}
	}

	listPosts, err := findPosts(listsDb.Db)
	if err != nil {
		panic(err)
	}

	logger.Info("Adding posts from lists.sh")
	for _, post := range listPosts {
		updated := false
		for _, alreadyAdded := range updateIDs {
			if post.UserID == alreadyAdded.User.ID {
				// we need to change the ID for these posts to the prose user id
				// because we were able to determine it was the same user
				post.UserID = alreadyAdded.ReplaceWithID
				post.Space = "lists"
				err = insertPost(tx, post)
				if err != nil {
					panic(err)
				}
				updated = true
				break
			}
		}

		if updated {
			continue
		}

		post.Space = "lists"
		err = insertPost(tx, post)
		if err != nil {
			panic(err)
		}
	}

	logger.Info("Committing transactions to PICO db")
	// Commit the transaction.
	if err = tx.Commit(); err != nil {
		panic(err)
	}
}
