package pgs

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"go.uber.org/zap"
)

func getHelpText(userName, projectName string) string {
	helpStr := "commands: [help, stats, ls, rm, link, unlink, prune, retain, depends]\n\n"
	helpStr += "NOTICE: any cmd that results in a mutation *must* be appended with `--write` for the changes to persist, otherwise it will simply output a dry-run.\n\n"

	sshCmdStr := fmt.Sprintf("ssh %s@pgs.sh", userName)
	helpStr += fmt.Sprintf("`%s help`: prints this screen\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s stats`: prints stats for user\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s ls`: lists projects\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s rm %s`: deletes `%s`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s link %s project-b`: symbolic link from `%s` to `project-b`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf(
		"`%s unlink %s`: alias for `link %s %s`, which removes symbolic link for `%s`\n",
		sshCmdStr, projectName, projectName, projectName, projectName,
	)
	helpStr += fmt.Sprintf("`%s prune %s`: removes all projects that match prefix `%s` and is not linked to another project\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s retain %s`: alias for `prune` but retains the (3) most recently updated projects\n", sshCmdStr, projectName)
	helpStr += fmt.Sprintf("`%s depends %s`: lists all projects linked to `%s`\n", sshCmdStr, projectName, projectName)
	return helpStr
}

type CmdSessionLogger struct {
	Log *zap.SugaredLogger
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
	Log     *zap.SugaredLogger
	Store   storage.ObjectStorage
	Dbpool  db.DB
	Write   bool
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
	c.Log.Error(err)
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

	fileList, err := c.Store.ListFiles(bucket, projectName+"/", true)
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
		c.Log.Infof(
			"(%s) attempting to delete (bucket: %s) (%s)",
			c.User.Name,
			bucket.Name,
			file.Name(),
		)
		if c.Write {
			err = c.Store.DeleteFile(
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
	c.output(getHelpText(c.User.Name, "project-a"))
}

func (c *Cmd) stats(cfgMaxSize uint64) error {
	ff, err := c.Dbpool.FindFeatureForUser(c.User.ID, "pgs")
	if err != nil {
		ff = db.NewFeatureFlag(c.User.ID, "pgs", cfgMaxSize, 0)
	}
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

	str := "stats\n"
	str += "=====\n"
	str += fmt.Sprintf(
		"space:\t\t%.4f/%.4fGB, %.4f%%\n",
		shared.BytesToGB(int(totalFileSize)),
		shared.BytesToGB(int(storageMax)),
		(float32(totalFileSize)/float32(storageMax))*100,
	)
	str += fmt.Sprintf("projects:\t%d", len(projects))
	c.output(str)

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

	for _, project := range projects {
		out := fmt.Sprintf("%s (links to: %s)", project.Name, project.ProjectDir)
		if project.Name == project.ProjectDir {
			out = fmt.Sprintf("%s\t(last updated: %s)", project.Name, project.UpdatedAt)
		}
		c.output(out)
	}

	return nil
}

func (c *Cmd) unlink(projectName string) error {
	c.Log.Infof("user (%s) running `unlink` command with (%s)", c.User.Name, projectName)
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
	c.Log.Infof("user (%s) running `link` command with (%s) (%s)", c.User.Name, projectName, linkTo)

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
		c.Log.Infof("user (%s) already has project (%s), updating", c.User.Name, projectName)
		err = c.Dbpool.LinkToProject(c.User.ID, project.ID, projectDir, c.Write)
		if err != nil {
			return err
		}
	} else {
		c.Log.Infof("user (%s) has no project record (%s), creating", c.User.Name, projectName)
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

	c.Log.Infof("user (%s) linking (%s) to (%s)", c.User.Name, projectName, projectDir)
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
		out := fmt.Sprintf("no projects linked to this project (%s) found", projectName)
		c.output(out)
		return nil
	}

	for _, project := range projects {
		out := fmt.Sprintf("%s (links to: %s)", project.Name, project.ProjectDir)
		if project.Name == project.ProjectDir {
			out = project.Name
		}
		c.output(out)
	}

	return nil
}

// delete all the projects and associated assets matching prefix
// but keep the latest N records.
func (c *Cmd) prune(prefix string, keepNumLatest int) error {
	c.Log.Infof("user (%s) running `clean` command for (%s)", c.User.Name, prefix)
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
			c.Log.Infof("(%s) removing", project.Name)
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
	c.Log.Infof("user (%s) running `rm` command for (%s)", c.User.Name, projectName)
	project, err := c.Dbpool.FindProjectByName(c.User.ID, projectName)
	if err == nil {
		c.Log.Infof("found project (%s) (%s), checking dependencies", projectName, project.ID)

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
			c.Log.Infof("(%s) removing", project.Name)
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
