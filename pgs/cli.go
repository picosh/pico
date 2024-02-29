package pgs

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/lipgloss/table"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/ui/common"
)

func styleRows(styles common.Styles) func(row, col int) lipgloss.Style {
	return func(row, col int) lipgloss.Style {
		if row == 0 {
			return styles.CliHeader
		}

		even := row%2 == 0
		if even {
			return styles.CliPadding.Copy().Foreground(lipgloss.Color("245"))
		}
		return styles.CliPadding.Copy().Foreground(lipgloss.Color("252"))
	}
}

func projectTable(styles common.Styles, projects []*db.Project) *table.Table {
	headers := []string{
		"Name",
		"Last Updated",
		"Links To",
		"ACL Type",
		"ACL",
	}
	data := [][]string{}
	for _, project := range projects {
		row := []string{
			project.Name,
			project.UpdatedAt.Format("2006-01-02 15:04:05"),
		}
		links := ""
		if project.ProjectDir != project.Name {
			links = project.ProjectDir
		}
		row = append(row, links)
		row = append(row,
			project.Acl.Type,
			strings.Join(project.Acl.Data, " "),
		)
		data = append(data, row)
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(styles.CliBorder).
		Headers(headers...).
		Rows(data...).
		StyleFunc(styleRows(styles))
	return t
}

func getHelpText(styles common.Styles, userName string) string {
	helpStr := "Commands: [help, stats, ls, rm, link, unlink, prune, retain, depends, acl]\n\n"
	helpStr += styles.Note.Render("NOTICE:") + " *must* append with `--write` for the changes to persist.\n\n"

	projectName := "projA"
	headers := []string{"Cmd", "Description"}
	data := [][]string{
		{
			"help",
			"prints this screen",
		},
		{
			"stats",
			"usage statistics",
		},
		{
			"ls",
			"lists projects",
		},
		{
			fmt.Sprintf("rm %s", projectName),
			fmt.Sprintf("delete %s", projectName),
		},
		{
			fmt.Sprintf("link %s --to projB", projectName),
			fmt.Sprintf("symbolic link `%s` to `projB`", projectName),
		},
		{
			fmt.Sprintf("unlink %s", projectName),
			fmt.Sprintf("removes symbolic link for `%s`", projectName),
		},
		{
			fmt.Sprintf("prune %s", projectName),
			fmt.Sprintf("removes projects that match prefix `%s`", projectName),
		},
		{
			fmt.Sprintf("retain %s", projectName),
			"alias to `prune` but keeps last N projects",
		},
		{
			fmt.Sprintf("depends %s", projectName),
			fmt.Sprintf("lists all projects linked to `%s`", projectName),
		},
		{
			fmt.Sprintf("acl %s", projectName),
			fmt.Sprintf("access control for `%s`", projectName),
		},
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(styles.CliBorder).
		Headers(headers...).
		Rows(data...).
		StyleFunc(styleRows(styles))

	helpStr += t.String()
	return helpStr
}

type CmdSessionLogger struct {
	Log *slog.Logger
}

func (c *CmdSessionLogger) Write(out []byte) (int, error) {
	c.Log.Info(string(out))
	return 0, nil
}

func (c *CmdSessionLogger) Exit(code int) error {
	os.Exit(code)
	return fmt.Errorf("panic %d", code)
}

func (c *CmdSessionLogger) Close() error {
	return fmt.Errorf("closing")
}

func (c *CmdSessionLogger) Stderr() io.ReadWriter {
	return nil
}

type CmdSession interface {
	Write([]byte) (int, error)
	Exit(code int) error
	Close() error
	Stderr() io.ReadWriter
}

type Cmd struct {
	User    *db.User
	Session CmdSession
	Log     *slog.Logger
	Store   storage.StorageServe
	Dbpool  db.DB
	Write   bool
	Styles  common.Styles
}

func (c *Cmd) output(out string) {
	_, _ = c.Session.Write([]byte(out + "\r\n"))
}

func (c *Cmd) error(err error) {
	_, _ = fmt.Fprint(c.Session.Stderr(), err, "\r\n")
	_ = c.Session.Exit(1)
	_ = c.Session.Close()
}

func (c *Cmd) bail(err error) {
	if err == nil {
		return
	}
	c.Log.Error(err.Error())
	c.error(err)
}

func (c *Cmd) notice() {
	if !c.Write {
		c.output("\nNOTICE: changes not commited, use `--write` to save operation")
	}
}

func (c *Cmd) RmProjectAssets(projectName string) error {
	bucketName := shared.GetAssetBucketName(c.User.ID)
	bucket, err := c.Store.GetBucket(bucketName)
	if err != nil {
		return err
	}
	c.output(fmt.Sprintf("removing project assets (%s)", projectName))

	fileList, err := c.Store.ListObjects(bucket, projectName+"/", true)
	if err != nil {
		return err
	}

	if len(fileList) == 0 {
		c.output(fmt.Sprintf("no assets found for project (%s)", projectName))
		return nil
	}
	c.output(fmt.Sprintf("found (%d) assets for project (%s), removing", len(fileList), projectName))

	for _, file := range fileList {
		intent := fmt.Sprintf("deleted (%s)", file.Name())
		c.Log.Info(
			"attempting to delete file",
			"user", c.User.Name,
			"bucket", bucket.Name,
			"filename", file.Name(),
		)
		if c.Write {
			err = c.Store.DeleteObject(
				bucket,
				filepath.Join(projectName, file.Name()),
			)
			if err == nil {
				c.output(intent)
			} else {
				return err
			}
		} else {
			c.output(intent)
		}
	}
	return nil
}

func (c *Cmd) help() {
	c.output(getHelpText(c.Styles, c.User.Name))
}

func (c *Cmd) stats(cfgMaxSize uint64) error {
	ff, err := c.Dbpool.FindFeatureForUser(c.User.ID, "pgs")
	if err != nil {
		ff = db.NewFeatureFlag(c.User.ID, "pgs", cfgMaxSize, 0)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(cfgMaxSize)
	storageMax := ff.Data.StorageMax

	bucketName := shared.GetAssetBucketName(c.User.ID)
	bucket, err := c.Store.UpsertBucket(bucketName)
	if err != nil {
		return err
	}

	totalFileSize, err := c.Store.GetBucketQuota(bucket)
	if err != nil {
		return err
	}

	projects, err := c.Dbpool.FindProjectsByUser(c.User.ID)
	if err != nil {
		return err
	}

	headers := []string{"Used (GB)", "Quota (GB)", "Used (%)", "Projects (#)"}
	data := []string{
		fmt.Sprintf("%.4f", shared.BytesToGB(int(totalFileSize))),
		fmt.Sprintf("%.4f", shared.BytesToGB(int(storageMax))),
		fmt.Sprintf("%.4f", (float32(totalFileSize)/float32(storageMax))*100),
		fmt.Sprintf("%d", len(projects)),
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderStyle(c.Styles.CliBorder).
		Headers(headers...).
		Rows(data).
		StyleFunc(styleRows(c.Styles))
	c.output(t.String())

	return nil
}

func (c *Cmd) ls() error {
	projects, err := c.Dbpool.FindProjectsByUser(c.User.ID)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		c.output("no projects found")
	}

	t := projectTable(c.Styles, projects)
	c.output(t.String())

	return nil
}

func (c *Cmd) unlink(projectName string) error {
	c.Log.Info("user running `unlink` command", "user", c.User.Name, "project", projectName)
	project, err := c.Dbpool.FindProjectByName(c.User.ID, projectName)
	if err != nil {
		return errors.Join(err, fmt.Errorf("project (%s) does not exit", projectName))
	}

	err = c.Dbpool.LinkToProject(c.User.ID, project.ID, project.Name, c.Write)
	if err != nil {
		return err
	}
	c.output(fmt.Sprintf("(%s) unlinked", project.Name))

	return nil
}

func (c *Cmd) link(projectName, linkTo string) error {
	c.Log.Info("user running `link` command", "user", c.User.Name, "project", projectName, "link", linkTo)

	projectDir := linkTo
	_, err := c.Dbpool.FindProjectByName(c.User.ID, linkTo)
	if err != nil {
		e := fmt.Errorf("(%s) project doesn't exist", linkTo)
		return e
	}

	project, err := c.Dbpool.FindProjectByName(c.User.ID, projectName)
	projectID := ""
	if err == nil {
		projectID = project.ID
		c.Log.Info("user already has project, updating", "user", c.User.Name, "project", projectName)
		err = c.Dbpool.LinkToProject(c.User.ID, project.ID, projectDir, c.Write)
		if err != nil {
			return err
		}
	} else {
		c.Log.Info("user has no project record, creating", "user", c.User.Name, "project", projectName)
		if !c.Write {
			out := fmt.Sprintf("(%s) cannot create a new project without `--write` permission, aborting", projectName)
			c.output(out)
			return nil
		}
		id, err := c.Dbpool.InsertProject(c.User.ID, projectName, projectName)
		if err != nil {
			return err
		}
		projectID = id
	}

	c.Log.Info("user linking", "user", c.User.Name, "project", projectName, "projectDir", projectDir)
	err = c.Dbpool.LinkToProject(c.User.ID, projectID, projectDir, c.Write)
	if err != nil {
		return err
	}

	out := fmt.Sprintf("(%s) might have orphaned assets, removing", projectName)
	c.output(out)

	err = c.RmProjectAssets(projectName)
	if err != nil {
		return err
	}

	out = fmt.Sprintf("(%s) now points to (%s)", projectName, linkTo)
	c.output(out)
	return nil
}

func (c *Cmd) depends(projectName string) error {
	projects, err := c.Dbpool.FindProjectLinks(c.User.ID, projectName)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		out := fmt.Sprintf("no projects linked to (%s)", projectName)
		c.output(out)
		return nil
	}

	t := projectTable(c.Styles, projects)
	c.output(t.String())

	return nil
}

// delete all the projects and associated assets matching prefix
// but keep the latest N records.
func (c *Cmd) prune(prefix string, keepNumLatest int) error {
	c.Log.Info("user running `clean` command", "user", c.User.Name, "prefix", prefix)
	c.output(fmt.Sprintf("searching for projects that match prefix (%s) and are not linked to other projects", prefix))

	if prefix == "" || prefix == "*" {
		e := fmt.Errorf("must provide valid prefix")
		return e
	}

	projects, err := c.Dbpool.FindProjectsByPrefix(c.User.ID, prefix)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		c.output(fmt.Sprintf("no projects found matching prefix (%s)", prefix))
		return nil
	}

	rmProjects := []*db.Project{}
	for _, project := range projects {
		links, err := c.Dbpool.FindProjectLinks(c.User.ID, project.Name)
		if err != nil {
			return err
		}

		if len(links) == 0 {
			rmProjects = append(rmProjects, project)
		} else {
			out := fmt.Sprintf("project (%s) has (%d) projects linked to it, cannot prune", project.Name, len(links))
			c.output(out)
		}
	}

	goodbye := rmProjects
	if keepNumLatest > 0 {
		max := len(rmProjects) - (keepNumLatest)
		if max <= 0 {
			out := fmt.Sprintf(
				"no projects available to prune (retention policy: %d, total: %d)",
				keepNumLatest,
				len(rmProjects),
			)
			c.output(out)
			return nil
		}
		goodbye = rmProjects[:max]
	}

	for _, project := range goodbye {
		out := fmt.Sprintf("project (%s) is available to be pruned", project.Name)
		c.output(out)
		err = c.RmProjectAssets(project.Name)
		if err != nil {
			return err
		}

		out = fmt.Sprintf("(%s) removing", project.Name)
		c.output(out)

		if c.Write {
			c.Log.Info("removing project", "project", project.Name)
			err = c.Dbpool.RemoveProject(project.ID)
			if err != nil {
				return err
			}
		}
	}

	c.output("\nsummary")
	c.output("=======")
	for _, project := range goodbye {
		c.output(fmt.Sprintf("project (%s) removed", project.Name))
	}

	return nil
}

func (c *Cmd) rm(projectName string) error {
	c.Log.Info("user running `rm` command", "user", c.User.Name, "project", projectName)
	project, err := c.Dbpool.FindProjectByName(c.User.ID, projectName)
	if err == nil {
		c.Log.Info("found project, checking dependencies", "project", projectName, "projectID", project.ID)

		links, err := c.Dbpool.FindProjectLinks(c.User.ID, projectName)
		if err != nil {
			return err
		}

		if len(links) > 0 {
			e := fmt.Errorf("project (%s) has (%d) projects linking to it, cannot delete project until they have been unlinked or removed, aborting", projectName, len(links))
			return e
		}

		out := fmt.Sprintf("(%s) removing", project.Name)
		c.output(out)
		if c.Write {
			c.Log.Info("removing project", "project", project.Name)
			err = c.Dbpool.RemoveProject(project.ID)
			if err != nil {
				return err
			}
		}
	} else {
		e := fmt.Errorf("(%s) project not found for user (%s)", projectName, c.User.Name)
		return e
	}

	err = c.RmProjectAssets(project.Name)
	return err
}

func (c *Cmd) acl(projectName, aclType string, acls []string) error {
	c.Log.Info(
		"user running `acl` command",
		"user", c.User.Name,
		"project", projectName,
		"actType", aclType,
		"acls", acls,
	)
	c.output(fmt.Sprintf("setting acl for %s to %s (%s)", projectName, aclType, strings.Join(acls, ",")))
	acl := db.ProjectAcl{
		Type: aclType,
		Data: acls,
	}
	if c.Write {
		return c.Dbpool.UpdateProjectAcl(c.User.ID, projectName, acl)
	}
	return nil
}
