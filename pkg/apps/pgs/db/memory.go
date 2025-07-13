package pgsdb

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/utils"
)

type MemoryDB struct {
	Logger   *slog.Logger
	Users    []*db.User
	Projects []*db.Project
	Pubkeys  []*db.PublicKey
	Feature  *db.FeatureFlag
}

var _ PgsDB = (*MemoryDB)(nil)

func NewDBMemory(logger *slog.Logger) *MemoryDB {
	d := &MemoryDB{
		Logger: logger,
	}
	d.Logger.Info("connecting to our in-memory database. All data created during runtime will be lost on exit.")
	return d
}

func (me *MemoryDB) SetupTestData() {
	user := &db.User{
		ID:   uuid.NewString(),
		Name: "testusr",
	}
	me.Users = append(me.Users, user)
	feature := db.NewFeatureFlag(
		user.ID,
		"plus",
		uint64(25*utils.MB),
		int64(10*utils.MB),
		int64(5*utils.KB),
	)
	expiresAt := time.Now().Add(time.Hour * 24)
	feature.ExpiresAt = &expiresAt
	me.Feature = feature
}

var errNotImpl = fmt.Errorf("not implemented")

func (me *MemoryDB) FindUsers() ([]*db.User, error) {
	users := []*db.User{}
	return users, errNotImpl
}

func (me *MemoryDB) FindUserByPubkey(key string) (*db.User, error) {
	for _, pk := range me.Pubkeys {
		if pk.Key == key {
			return me.FindUser(pk.UserID)
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (me *MemoryDB) FindUser(userID string) (*db.User, error) {
	for _, user := range me.Users {
		if user.ID == userID {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (me *MemoryDB) FindUserByName(name string) (*db.User, error) {
	for _, user := range me.Users {
		if user.Name == name {
			return user, nil
		}
	}
	return nil, fmt.Errorf("user not found")
}

func (me *MemoryDB) FindFeature(userID, name string) (*db.FeatureFlag, error) {
	return me.Feature, nil
}

func (me *MemoryDB) Close() error {
	return nil
}

func (me *MemoryDB) FindTotalSizeForUser(userID string) (int, error) {
	return 0, errNotImpl
}

func (me *MemoryDB) InsertProject(userID, name, projectDir string) (string, error) {
	id := uuid.NewString()
	now := time.Now()
	me.Projects = append(me.Projects, &db.Project{
		ID:         id,
		UserID:     userID,
		Name:       name,
		ProjectDir: projectDir,
		CreatedAt:  &now,
		UpdatedAt:  &now,
	})
	return id, nil
}

func (me *MemoryDB) UpdateProject(userID, name string) error {
	project, err := me.FindProjectByName(userID, name)
	if err != nil {
		return err
	}

	now := time.Now()
	project.UpdatedAt = &now

	return nil
}

func (me *MemoryDB) UpsertProject(userID, projectName, projectDir string) (*db.Project, error) {
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

func (me *MemoryDB) LinkToProject(userID, projectID, projectDir string, commit bool) error {
	return errNotImpl
}

func (me *MemoryDB) RemoveProject(projectID string) error {
	return errNotImpl
}

func (me *MemoryDB) FindProjectByName(userID, name string) (*db.Project, error) {
	for _, project := range me.Projects {
		if project.UserID != userID {
			continue
		}

		if project.Name != name {
			continue
		}

		return project, nil
	}
	return nil, fmt.Errorf("project not found by name %s", name)
}

func (me *MemoryDB) FindProjectLinks(userID, name string) ([]*db.Project, error) {
	return []*db.Project{}, errNotImpl
}

func (me *MemoryDB) FindProjectsByPrefix(userID, prefix string) ([]*db.Project, error) {
	return []*db.Project{}, errNotImpl
}

func (me *MemoryDB) FindProjectsByUser(userID string) ([]*db.Project, error) {
	pjs := []*db.Project{}
	for _, project := range me.Projects {
		if project.UserID != userID {
			continue
		}
		pjs = append(pjs, project)
	}
	return pjs, nil
}

func (me *MemoryDB) FindProjects(userID string) ([]*db.Project, error) {
	return []*db.Project{}, errNotImpl
}

func (me *MemoryDB) UpdateProjectAcl(userID, name string, acl db.ProjectAcl) error {
	return errNotImpl
}

func (me *MemoryDB) RegisterAdmin(username, pubkey, pubkeyName string) error {
	return errNotImpl
}
