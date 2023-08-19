package pgs

import (
	"errors"
	"fmt"

	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/send/utils"
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

type Cmd struct {
	user    *db.User
	session ssh.Session
	log     *zap.SugaredLogger
	store   storage.ObjectStorage
	dbpool  db.DB
	write   bool
}

func (c *Cmd) output(out string) {
	_, _ = c.session.Write([]byte(out + "\n"))
}

func (c *Cmd) bail(err error) {
	if err == nil {
		return
	}
	c.log.Error(err)
	utils.ErrorHandler(c.session, err)
}

func (c *Cmd) notice() {
	if !c.write {
		c.output("\nNOTICE: changes not commited, use `--write` to save operation")
	}
}

func (c *Cmd) rmProjectAssets(projectName string) error {
	bucketName := shared.GetAssetBucketName(c.user.ID)
	bucket, err := c.store.GetBucket(bucketName)
	if err != nil {
		return err
	}
	c.output(fmt.Sprintf("removing project assets (%s)", projectName))

	fileList, err := c.store.ListFiles(bucket, projectName+"/", true)
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
		if c.write {
			err = c.store.DeleteFile(bucket, file.Name())
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
	c.output(getHelpText(c.user.Name, "project-a"))
}

func (c *Cmd) stats(maxSize int) error {
	bucketName := shared.GetAssetBucketName(c.user.ID)
	bucket, err := c.store.UpsertBucket(bucketName)
	if err != nil {
		return err
	}

	totalFileSize, err := c.store.GetBucketQuota(bucket)
	if err != nil {
		return err
	}

	projects, err := c.dbpool.FindProjectsByUser(c.user.ID)
	if err != nil {
		return err
	}

	str := "stats\n"
	str += "=====\n"
	str += fmt.Sprintf(
		"space:\t\t%.4f/%.4fGB, %.4f%%\n",
		shared.BytesToGB(int(totalFileSize)),
		shared.BytesToGB(maxSize),
		(float32(totalFileSize)/float32(maxSize))*100,
	)
	str += fmt.Sprintf("projects:\t%d", len(projects))
	c.output(str)

	return nil
}

func (c *Cmd) ls() error {
	projects, err := c.dbpool.FindProjectsByUser(c.user.ID)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		c.output("no projects found")
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

func (c *Cmd) unlink(projectName string) error {
	c.log.Infof("user (%s) running `unlink` command with (%s)", c.user.Name, projectName)
	project, err := c.dbpool.FindProjectByName(c.user.ID, projectName)
	if err != nil {
		return errors.Join(err, fmt.Errorf("project (%s) does not exit", projectName))
	}

	err = c.dbpool.LinkToProject(c.user.ID, project.ID, project.Name, c.write)
	if err != nil {
		return err
	}
	c.output(fmt.Sprintf("(%s) unlinked", project.Name))

	return nil
}

func (c *Cmd) link(projectName, linkTo string) error {
	c.log.Infof("user (%s) running `link` command with (%s) (%s)", c.user.Name, projectName, linkTo)

	projectDir := linkTo
	_, err := c.dbpool.FindProjectByName(c.user.ID, linkTo)
	if err != nil {
		e := fmt.Errorf("(%s) project doesn't exist", linkTo)
		return e
	}

	project, err := c.dbpool.FindProjectByName(c.user.ID, projectName)
	projectID := ""
	if err == nil {
		projectID = project.ID
		c.log.Infof("user (%s) already has project (%s), updating", c.user.Name, projectName)
		err = c.dbpool.LinkToProject(c.user.ID, project.ID, projectDir, c.write)
		if err != nil {
			return err
		}
	} else {
		c.log.Infof("user (%s) has no project record (%s), creating", c.user.Name, projectName)
		if !c.write {
			out := fmt.Sprintf("(%s) cannot create a new project without `--write` permission, aborting", projectName)
			c.output(out)
			return nil
		}
		id, err := c.dbpool.InsertProject(c.user.ID, projectName, projectName)
		if err != nil {
			return err
		}
		projectID = id
	}

	c.log.Infof("user (%s) linking (%s) to (%s)", c.user.Name, projectName, projectDir)
	err = c.dbpool.LinkToProject(c.user.ID, projectID, projectDir, c.write)
	if err != nil {
		return err
	}

	out := fmt.Sprintf("(%s) might have orphaned assets, removing", projectName)
	c.output(out)

	err = c.rmProjectAssets(projectName)
	if err != nil {
		return err
	}

	out = fmt.Sprintf("(%s) now points to (%s)", projectName, linkTo)
	c.output(out)
	return nil
}

func (c *Cmd) depends(projectName string) error {
	projects, err := c.dbpool.FindProjectLinks(c.user.ID, projectName)
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
	c.log.Infof("user (%s) running `clean` command for (%s)", c.user.Name, prefix)
	c.output(fmt.Sprintf("searching for projects that match prefix (%s) and are not linked to other projects", prefix))

	if prefix == "" || prefix == "*" {
		e := fmt.Errorf("must provide valid prefix")
		return e
	}

	projects, err := c.dbpool.FindProjectsByPrefix(c.user.ID, prefix)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		c.output(fmt.Sprintf("no projects found matching prefix (%s)", prefix))
		return nil
	}

	rmProjects := []*db.Project{}
	for _, project := range projects {
		links, err := c.dbpool.FindProjectLinks(c.user.ID, project.Name)
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
		err = c.rmProjectAssets(project.Name)
		if err != nil {
			return err
		}

		out = fmt.Sprintf("(%s) removing", project.Name)
		c.output(out)

		if c.write {
			c.log.Infof("(%s) removing", project.Name)
			err = c.dbpool.RemoveProject(project.ID)
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
	c.log.Infof("user (%s) running `rm` command for (%s)", c.user.Name, projectName)
	project, err := c.dbpool.FindProjectByName(c.user.ID, projectName)
	if err == nil {
		c.log.Infof("found project (%s) (%s), checking dependencies", projectName, project.ID)

		links, err := c.dbpool.FindProjectLinks(c.user.ID, projectName)
		if err != nil {
			return err
		}

		if len(links) > 0 {
			e := fmt.Errorf("project (%s) has (%d) projects linking to it, cannot delete project until they have been unlinked or removed, aborting", projectName, len(links))
			return e
		}

		out := fmt.Sprintf("(%s) removing", project.Name)
		c.output(out)
		if c.write {
			c.log.Infof("(%s) removing", project.Name)
			err = c.dbpool.RemoveProject(project.ID)
			if err != nil {
				return err
			}
		}
	} else {
		e := fmt.Errorf("(%s) project not found for user (%s)", projectName, c.user.Name)
		return e
	}

	err = c.rmProjectAssets(project.Name)
	return err
}
