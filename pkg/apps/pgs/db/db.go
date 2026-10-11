package pgsdb

import (
	"strings"

	"github.com/picosh/pico/pkg/db"
)

type PgsDB interface {
	db.AuthDB
	FindUsers() ([]*db.User, error)

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

	InsertFormEntry(userID, name string, data map[string]interface{}) error
	FindFormEntriesByUserAndName(userID, name string) ([]*db.FormEntry, error)
	FindFormNamesByUser(userID string) ([]string, error)
	RemoveFormEntriesByUserAndName(userID, name string) error

	RegisterAdmin(username, pubkey, pubkeyName string) error

	Close() error
}

func IsProjectPrivate(projectName string) bool {
	return strings.HasPrefix(projectName, "private-")
}
