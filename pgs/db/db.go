package pgsdb

import "github.com/picosh/pico/db"

type PgsDB interface {
	FindUser(userID string) (*db.User, error)
	FindUserByName(name string) (*db.User, error)
	FindUserByPubkey(pubkey string) (*db.User, error)
	FindUsers() ([]*db.User, error)

	FindFeature(userID string, name string) (*db.FeatureFlag, error)

	InsertProject(userID, name, projectDir string) (string, error)
	UpdateProject(userID, name string) error
	UpdateProjectAcl(userID, name string, acl db.ProjectAcl) error
	UpsertProject(userID, projectName, projectDir string) (*db.Project, error)
	RemoveProject(projectID string) error
	LinkToProject(userID, projectID, projectDir string, commit bool) error
	FindProjectByName(userID, name string) (*db.Project, error)
	FindProjectLinks(userID, name string) ([]*db.Project, error)
	FindProjectsByUser(userID string) ([]*db.Project, error)
	FindProjectsByPrefix(userID, name string) ([]*db.Project, error)
	FindProjects(by string) ([]*db.Project, error)

	Close() error
}
