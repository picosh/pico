package main

import (
	"log/slog"
	"os"
	"strings"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/db/postgres"
	"github.com/picosh/pico/pgs"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/config"
)

func bail(err error) {
	if err != nil {
		panic(err)
	}
}

type RmProject struct {
	user *db.User
	name string
}

// this script will find any objects stored within Store that does not
// have a corresponding project inside our database.
func main() {
	// to actually commit changes, set to true
	writeEnv := shared.GetEnv("WRITE", "0")
	write := false
	if writeEnv == "1" {
		write = true
	}
	logger := slog.Default()

	picoCfg := config.NewConfigCms()
	picoCfg.Logger = logger
	picoCfg.DbURL = os.Getenv("DATABASE_URL")
	picoCfg.MinioURL = os.Getenv("MINIO_URL")
	picoCfg.MinioUser = os.Getenv("MINIO_ROOT_USER")
	picoCfg.MinioPass = os.Getenv("MINIO_ROOT_PASSWORD")
	picoDb := postgres.NewDB(picoCfg.DbURL, picoCfg.Logger)

	var st storage.ObjectStorage
	var err error
	st, err = storage.NewStorageMinio(picoCfg.MinioURL, picoCfg.MinioUser, picoCfg.MinioPass)
	bail(err)

	logger.Info("fetching all users")
	users, err := picoDb.FindUsers()
	bail(err)

	logger.Info("fetching all buckets")
	buckets, err := st.ListBuckets()
	bail(err)

	rmProjects := []RmProject{}

	for _, bucketName := range buckets {
		// only care about pgs
		if !strings.HasPrefix(bucketName, "static-") {
			continue
		}

		bucket, err := st.GetBucket(bucketName)
		bail(err)
		bucketProjects, err := st.ListFiles(bucket, "/", false)
		bail(err)

		userID := strings.Replace(bucketName, "static-", "", 1)
		user := &db.User{
			ID:   userID,
			Name: userID,
		}
		for _, u := range users {
			if u.ID == userID {
				user = u
				break
			}
		}
		projects, err := picoDb.FindProjectsByUser(userID)
		bail(err)
		for _, bucketProject := range bucketProjects {
			found := false
			for _, project := range projects {
				// ignore links
				if project.Name != project.ProjectDir {
					continue
				}
				if project.Name == bucketProject.Name() {
					found = true
				}
			}
			if !found {
				logger.Info("marking for removal", "bucket", bucketName, "project", bucketProject.Name())
				rmProjects = append(rmProjects, RmProject{
					name: bucketProject.Name(),
					user: user,
				})
			}
		}
	}

	session := &pgs.CmdSessionLogger{
		Log: logger,
	}

	for _, project := range rmProjects {
		opts := &pgs.Cmd{
			Session: session,
			User:    project.user,
			Store:   st,
			Log:     logger,
			Dbpool:  picoDb,
			Write:   write,
		}
		err := opts.RmProjectAssets(project.name)
		bail(err)
	}

	logger.Info("store projects marked for deletion", "length", len(rmProjects))
	for _, project := range rmProjects {
		logger.Info("removing project", "user", project.user.Name, "project", project.name)
	}
	if !write {
		logger.Info("WARNING: changes not committed, need env var WRITE=1")
	}
}
