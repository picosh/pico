package pgs

import "github.com/picosh/pico/pkg/send/scoped"

// Roles a cert limited to some projects may have. Projects match exactly, or
// by prefix with a trailing *, which deploys that link a new project need.
const (
	roleRead   = "read"
	roleDeploy = "deploy"
)

var uploadRules = scoped.Rules{
	Read:     []string{roleRead, roleDeploy},
	Write:    []string{roleDeploy},
	Resource: scoped.Project,
}

// checkRead refuses a session whose scopes don't cover reading a project, or
// the whole account if projectName is empty.
func (c *Cmd) checkRead(projectName string) error {
	return c.Scopes.Check("reading", projectName, roleRead, roleDeploy)
}

func (c *Cmd) checkDeploy(projectName string) error {
	return c.Scopes.Check("changing", projectName, roleDeploy)
}
