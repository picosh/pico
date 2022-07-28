package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"

	"git.sr.ht/~erock/wish/cms/config"
	"git.sr.ht/~erock/wish/cms/db"
	"git.sr.ht/~erock/wish/cms/db/postgres"
	"go.uber.org/zap"
)

func createLogger() *zap.SugaredLogger {
	logger, err := zap.NewProduction()
	if err != nil {
		log.Fatal(err)
	}

	return logger.Sugar()
}

func InsertUser(tx *sql.Tx, user *db.User) error {
	_, err := tx.Exec(
		"INSERT INTO app_users (id, name, created_at) VALUES($1, $2, $3)",
		user.ID,
		user.Name,
		user.CreatedAt,
	)
	return err
}

func InsertPublicKey(tx *sql.Tx, pk *db.PublicKey) error {
	_, err := tx.Exec(
		"INSERT INTO public_keys (id, user_id, public_key, created_at) VALUES ($1, $2, $3, $4)",
		pk.ID,
		pk.UserID,
		pk.Key,
		pk.CreatedAt,
	)
	return err
}

func InsertPost(tx *sql.Tx, post *db.Post) error {
	_, err := tx.Exec(
		`INSERT INTO posts
			(id, user_id, title, text, created_at, publish_at, updated_at, description, filename, hidden, cur_space)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
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
	rollback := func() {
		// Defer a rollback in case anything fails.
		defer tx.Rollback()
	}
	defer rollback()

	proseUsers, err := proseDb.FindUsers()
	if err != nil {
		panic(err)
	}

	listUsers, err := listsDb.FindUsers()
	if err != nil {
		panic(err)
	}

	userMap := map[string]*db.User{}
	for _, proseUser := range proseUsers {
		userMap[proseUser.Name] = proseUser

		err = InsertUser(tx, proseUser)
		if err != nil {
			panic(err)
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

				err = InsertPublicKey(tx, prosePK)
				if err != nil {
					panic(err)
				}
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
		err = InsertUser(tx, data.User)
		if err != nil {
			panic(err)
		}

		for _, pk := range data.Pks {
			err = InsertPublicKey(tx, pk)
			if err != nil {
				panic(err)
			}
		}
	}

	logger.Infof("Adding records with conflicts (%d)", len(conflicts))
	for _, data := range conflicts {
		data.User.Name = fmt.Sprintf("%stmp", data.User.Name)
		err = InsertUser(tx, data.User)
		if err != nil {
			panic(err)
		}

		for _, pk := range data.Pks {
			err = InsertPublicKey(tx, pk)
			if err != nil {
				panic(err)
			}
		}
	}

	prosePosts, err := proseDb.FindPosts()
	if err != nil {
		panic(err)
	}

	logger.Info("Adding posts from prose.sh")
	for _, post := range prosePosts {
		post.Space = "prose"
		err = InsertPost(tx, post)
		if err != nil {
			panic(err)
		}
	}

	listPosts, err := listsDb.FindPosts()
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
				err = InsertPost(tx, post)
				if err != nil {
					fmt.Println(post.Filename)
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
		err = InsertPost(tx, post)
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
