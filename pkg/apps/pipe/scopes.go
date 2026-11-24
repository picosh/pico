package pipe

import "strings"

// Roles a cert limited to some topics may have. Piping needs both.
const (
	rolePub = "pub"
	roleSub = "sub"
)

// scopeRoles are the roles a command needs on every topic it joins.
func scopeRoles(cmd string) []string {
	switch cmd {
	case "pub":
		return []string{rolePub}
	case "sub":
		return []string{roleSub}
	default:
		return []string{rolePub, roleSub}
	}
}

// scopeTopic is how scopes name a resolved topic: without the user's own
// prefix, as the user names it. Public topics and other users' keep theirs.
func (c *CliCmd) scopeTopic(name string) string {
	return strings.TrimPrefix(name, c.userName+"/")
}

// checkTopic refuses a resolved topic that the session's scopes don't cover
// with every one of roles.
func (c *CliCmd) checkTopic(name string, roles ...string) error {
	actions := map[string]string{rolePub: "publishing to", roleSub: "subscribing to"}
	for _, role := range roles {
		if err := c.scopes.Check(actions[role], c.scopeTopic(name), role); err != nil {
			return err
		}
	}
	return nil
}

// sees reports whether the session's scopes cover a topic in any way.
func (c *CliCmd) sees(name string) bool {
	return c.scopes.Allows(c.scopeTopic(name), rolePub, roleSub)
}
