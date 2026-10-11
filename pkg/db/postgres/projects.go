package postgres

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
)

func (me *PsqlDB) InsertProject(userID, name, projectDir string) (string, error) {
	if !shared.IsValidSubdomain(name) {
		return "", fmt.Errorf("'%s' is not a valid project name, must match /^[a-z0-9-]+$/", name)
	}

	var id string
	err := me.Db.QueryRow(`INSERT INTO projects (user_id, name, project_dir) VALUES ($1, $2, $3) RETURNING id;`, userID, name, projectDir).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}

func (me *PsqlDB) UpdateProject(userID, name string) error {
	_, err := me.Db.Exec(`UPDATE projects SET updated_at = $3 WHERE user_id = $1 AND name = $2;`, userID, name, time.Now())
	return err
}

func (me *PsqlDB) FindProjectByName(userID, name string) (*db.Project, error) {
	project := &db.Project{}
	err := me.Db.Get(project, `SELECT * FROM projects WHERE user_id = $1 AND name = $2;`, userID, name)
	if err != nil {
		return nil, err
	}
	return project, nil
}

func (me *PsqlDB) UpsertProject(userID, projectName, projectDir string) (*db.Project, error) {
	project, err := me.FindProjectByName(userID, projectName)
	if err == nil {
		// this just updates the `createdAt` timestamp, useful for book-keeping
		err = me.UpdateProject(userID, projectName)
		if err != nil {
			me.Logger.Error("could not update project", "err", err)
			return nil, err
		}
		return project, nil
	}

	_, err = me.InsertProject(userID, projectName, projectName)
	if err != nil {
		me.Logger.Error("could not create project", "err", err)
		return nil, err
	}
	return me.FindProjectByName(userID, projectName)
}

func (me *PsqlDB) findPagesStats(userID string) (*db.UserServiceStats, error) {
	stats := db.UserServiceStats{
		Service: "pgs",
	}
	// the aggregates are NULL when the user has no projects
	var first, latestCreated, latestUpdated sql.NullTime
	err := me.Db.QueryRow(
		`SELECT count(id), min(created_at), max(created_at), max(updated_at) FROM projects WHERE user_id=$1`,
		userID,
	).Scan(&stats.Num, &first, &latestCreated, &latestUpdated)
	if err != nil {
		return nil, err
	}
	stats.FirstCreatedAt = first.Time
	stats.LastestCreatedAt = latestCreated.Time
	stats.LatestUpdatedAt = latestUpdated.Time

	return &stats, nil
}
