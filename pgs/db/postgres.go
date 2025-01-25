package pgsdb

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/picosh/pico/db"
	"github.com/picosh/utils"
)

type PgsPsqlDB struct {
	Logger *slog.Logger
	Db     *sqlx.DB
}

var _ PgsDB = (*PgsPsqlDB)(nil)

func NewDB(databaseUrl string, logger *slog.Logger) (*PgsPsqlDB, error) {
	var err error
	d := &PgsPsqlDB{
		Logger: logger,
	}
	d.Logger.Info("connecting to postgres", "databaseUrl", databaseUrl)

	db, err := sqlx.Connect("postgres", databaseUrl)
	if err != nil {
		return nil, err
	}

	d.Db = db
	return d, nil
}

func (me *PgsPsqlDB) Close() error {
	return me.Db.Close()
}

func (me *PgsPsqlDB) FindUsers() ([]*db.User, error) {
	users := []*db.User{}
	err := me.Db.Select(&users, "SELECT * FROM app_users")
	return users, err
}

func (me *PgsPsqlDB) FindUserByPubkey(key string) (*db.User, error) {
	pk := []db.PublicKey{}
	err := me.Db.Select(&pk, "SELECT * FROM public_keys WHERE public_key=$1", key)
	if err != nil {
		return nil, err
	}
	if len(pk) == 0 {
		return nil, fmt.Errorf("pubkey not found in our database: [%s]", key)
	}
	// When we run PublicKeyForKey and there are multiple public keys returned from the database
	// that should mean that we don't have the correct username for this public key.
	// When that happens we need to reject the authentication and ask the user to provide the correct
	// username when using ssh.  So instead of `ssh <domain>` it should be `ssh user@<domain>`
	if len(pk) > 1 {
		return nil, &db.ErrMultiplePublicKeys{}
	}

	return me.FindUser(pk[0].UserID)
}

func (me *PgsPsqlDB) FindUser(userID string) (*db.User, error) {
	user := db.User{}
	err := me.Db.Get(&user, "SELECT * FROM app_users WHERE id=$1", userID)
	return &user, err
}

func (me *PgsPsqlDB) FindUserByName(name string) (*db.User, error) {
	user := db.User{}
	err := me.Db.Get(&user, "SELECT * FROM app_users WHERE name=$1", name)
	return &user, err
}

func (me *PgsPsqlDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	ff := db.FeatureFlag{}
	err := me.Db.Get(&ff, "SELECT * FROM feature_flags WHERE user_id=$1 AND name=$2 ORDER BY expires_at DESC LIMIT 1", userID, name)
	return &ff, err
}

func (me *PgsPsqlDB) InsertProject(userID, name, projectDir string) (string, error) {
	if !utils.IsValidSubdomain(name) {
		return "", fmt.Errorf("'%s' is not a valid project name, must match /^[a-z0-9-]+$/", name)
	}

	var projectID string
	row := me.Db.QueryRow(
		"INSERT INTO projects (user_id, name, project_dir) VALUES ($1, $2, $3) RETURNING id",
		userID,
		name,
		projectDir,
	)
	err := row.Scan(&projectID)
	return projectID, err
}

func (me *PgsPsqlDB) UpdateProject(userID, name string) error {
	_, err := me.Db.Exec("UPDATE projects SET updated_at=$1 WHERE user_id=$2 AND name=$3", time.Now(), userID, name)
	return err
}

func (me *PgsPsqlDB) UpsertProject(userID, projectName, projectDir string) (*db.Project, error) {
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

func (me *PgsPsqlDB) LinkToProject(userID, projectID, projectDir string, commit bool) error {
	linkToProject, err := me.FindProjectByName(userID, projectDir)
	if err != nil {
		return err
	}
	isAlreadyLinked := linkToProject.Name != linkToProject.ProjectDir
	sameProject := linkToProject.ID == projectID

	/*
		A project linked to another project which is also linked to a
		project is forbidden.  CI/CD Example:
			- ProjectProd links to ProjectStaging
			- ProjectStaging links to ProjectMain
			- We merge `main` and trigger a deploy which uploads to ProjectMain
			- All three get updated immediately
		This scenario was not the intent of our CI/CD.  What we actually
		wanted was to create a snapshot of ProjectMain and have ProjectStaging
		link to the snapshot, but that's not the intended design of pgs.

		So we want to close that gap here.

		We ensure that `project.Name` and `project.ProjectDir` are identical
		when there is no aliasing.
	*/
	if !sameProject && isAlreadyLinked {
		return fmt.Errorf(
			"cannot link (%s) to (%s) because it is also a link to (%s)",
			projectID,
			projectDir,
			linkToProject.ProjectDir,
		)
	}

	if commit {
		_, err = me.Db.Exec(
			"UPDATE projects SET project_dir=$1, updated_at=$2 WHERE id=$3",
			projectDir,
			time.Now(),
			projectID,
		)
	}
	return err
}

func (me *PgsPsqlDB) RemoveProject(projectID string) error {
	_, err := me.Db.Exec("DELETE FROM projects WHERE id=$1", projectID)
	return err
}

func (me *PgsPsqlDB) FindProjectByName(userID, name string) (*db.Project, error) {
	project := db.Project{}
	err := me.Db.Get(&project, "SELECT * FROM projects WHERE user_id=$1 AND name=$2", userID, name)
	return &project, err
}

func (me *PgsPsqlDB) FindProjectLinks(userID, name string) ([]*db.Project, error) {
	projects := []*db.Project{}
	err := me.Db.Select(
		&projects,
		"SELECT * FROM projects WHERE user_id=$1 AND name != project_dir AND project_dir=$2 ORDER BY name ASC",
		userID,
		name,
	)
	return projects, err
}

func (me *PgsPsqlDB) FindProjectsByPrefix(userID, prefix string) ([]*db.Project, error) {
	projects := []*db.Project{}
	err := me.Db.Select(
		&projects,
		"SELECT * FROM projects WHERE user_id=$1 AND name=project_dir AND name ILIKE $2 ORDER BY updated_at ASC, name ASC",
		userID,
		prefix+"%",
	)
	return projects, err
}

func (me *PgsPsqlDB) FindProjectsByUser(userID string) ([]*db.Project, error) {
	projects := []*db.Project{}
	err := me.Db.Select(
		&projects,
		"SELECT * FROM projects WHERE user_id=$1 ORDER BY name ASC",
		userID,
	)
	return projects, err
}

func (me *PgsPsqlDB) FindProjects(by string) ([]*db.Project, error) {
	projects := []*db.Project{}
	err := me.Db.Select(
		&projects,
		`SELECT p.id, p.user_id, u.name as username, p.name, p.project_dir, p.acl, p.blocked, p.created_at, p.updated_at
		FROM projects AS p
		LEFT JOIN app_users AS u ON u.id = p.user_id
		ORDER BY $1 DESC`,
		by,
	)
	return projects, err
}

func (me *PgsPsqlDB) UpdateProjectAcl(userID, name string, acl db.ProjectAcl) error {
	_, err := me.Db.Exec(
		"UPDATE projects SET acl=$3, updated_at=$4 WHERE user_id=$1 AND name=$2",
		userID, name, acl, time.Now(),
	)
	return err
}
