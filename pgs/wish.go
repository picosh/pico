package pgs

import (
	"errors"
	"fmt"
	"strings"

	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
	uploadassets "github.com/picosh/pico/filehandlers/assets"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/pico/wish/send/utils"
	"go.uber.org/zap"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := util.KeyText(s)
	if err != nil {
		return nil, fmt.Errorf("key not found")
	}

	user, err := dbpool.FindUserForKey(s.User(), key)
	if err != nil {
		return nil, err
	}

	if user.Name == "" {
		return nil, fmt.Errorf("must have username set")
	}

	return user, nil
}

type ProjectDetails struct {
	session ssh.Session
	store   storage.ObjectStorage
}

func getHelpText(userName, projectName string) string {
	helpStr := "commands: [help, stats, ls, rm, link, unlink, prune, retain, depends]\n\n"
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

func (c *Cmd) rmProjectAssets(projectName string) error {
	bucketName := shared.GetAssetBucketName(c.user.ID)
	bucket, err := c.store.GetBucket(bucketName)
	if err != nil {
		return err
	}

	fileList, err := c.store.ListFiles(bucket, projectName+"/", true)
	if err != nil {
		return err
	}

	for _, file := range fileList {
		intent := []byte(fmt.Sprintf("deleted (%s)\n", file.Name()))
		if c.write {
			err = c.store.DeleteFile(bucket, file.Name())
			if err == nil {
				_, _ = c.session.Write(intent)
			} else {
				return err
			}
		} else {
			_, _ = c.session.Write(intent)
		}
	}
	return nil
}

func (c *Cmd) help() {
	_, _ = c.session.Write([]byte(getHelpText(c.user.Name, "project-a")))
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
	str += fmt.Sprintf("projects:\t%d\n", len(projects))
	_, _ = c.session.Write([]byte(str))

	return nil
}

func (c *Cmd) ls() error {
	projects, err := c.dbpool.FindProjectsByUser(c.user.ID)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		out := "no linked projects found\n"
		_, _ = c.session.Write([]byte(out))
	}

	for _, project := range projects {
		out := fmt.Sprintf("%s (links to: %s)\n", project.Name, project.ProjectDir)
		if project.Name == project.ProjectDir {
			out = fmt.Sprintf("%s\n", project.Name)
		}
		_, _ = c.session.Write([]byte(out))
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
		c.log.Infof("user (%s) already has project (%s), updating ...", c.user.Name, projectName)
		err = c.dbpool.LinkToProject(c.user.ID, project.ID, projectDir, c.write)
		if err != nil {
			return err
		}
	} else {
		c.log.Infof("user (%s) has no project record (%s), creating ...", c.user.Name, projectName)
		if !c.write {
			out := fmt.Sprintf("(%s) cannot create a new project without `--write` permission, aborting ...\n", projectName)
			_, _ = c.session.Write([]byte(out))
			return nil
		}
		id, err := c.dbpool.InsertProject(c.user.ID, projectName, projectName)
		if err != nil {
			return err
		}
		projectID = id
	}

	c.log.Infof("user (%s) linking (%s) to (%s) ...", c.user.Name, projectName, projectDir)
	err = c.dbpool.LinkToProject(c.user.ID, projectID, projectDir, c.write)
	if err != nil {
		return err
	}

	out := fmt.Sprintf("(%s) might have orphaned assets, removing ...\n", projectName)
	_, _ = c.session.Write([]byte(out))

	err = c.rmProjectAssets(projectName)
	if err != nil {
		return err
	}

	out = fmt.Sprintf("(%s) now points to (%s)\n", projectName, linkTo)
	_, _ = c.session.Write([]byte(out))
	return nil
}

func (c *Cmd) depends(projectName string) error {
	projects, err := c.dbpool.FindProjectLinks(c.user.ID, projectName)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		out := fmt.Sprintf("no projects linked to this project (%s) found\n", projectName)
		_, _ = c.session.Write([]byte(out))
		return nil
	}

	for _, project := range projects {
		out := fmt.Sprintf("%s (links to: %s)\n", project.Name, project.ProjectDir)
		if project.Name == project.ProjectDir {
			out = fmt.Sprintf("%s\n", project.Name)
		}
		_, _ = c.session.Write([]byte(out))
	}

	return nil
}

// delete all the projects and associated assets matching prefix
// but keep the latest N records
func (c *Cmd) prune(prefix string, keepNumLatest int) error {
	c.log.Infof("user (%s) running `clean` command for (%s)", c.user.Name, prefix)
	if prefix == "" || prefix == "*" {
		e := fmt.Errorf("must provide valid prefix")
		return e
	}

	projects, err := c.dbpool.FindProjectsByPrefix(c.user.ID, prefix)
	if err != nil {
		return err
	}

	rmProjects := []*db.Project{}
	for _, project := range projects {
		links, err := c.dbpool.FindProjectLinks(c.user.ID, project.Name)
		if err != nil {
			return err
		}

		if len(links) == 0 {
			out := fmt.Sprintf("project (%s) is available to delete\n", project.Name)
			_, _ = c.session.Write([]byte(out))
			rmProjects = append(rmProjects, project)
		}
	}

	goodbye := rmProjects
	if keepNumLatest > 0 {
		goodbye = rmProjects[:len(rmProjects)-keepNumLatest]
	}

	for _, project := range goodbye {
		err = c.rmProjectAssets(project.Name)
		if err != nil {
			return err
		}

		out := fmt.Sprintf("(%s) removing ...\n", project.Name)
		_, _ = c.session.Write([]byte(out))

		if c.write {
			c.log.Infof("(%s) removing ...", project.Name)
			err = c.dbpool.RemoveProject(project.ID)
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *Cmd) rm(projectName string) error {
	c.log.Infof("user (%s) running `rm` command for (%s)", c.user.Name, projectName)
	project, err := c.dbpool.FindProjectByName(c.user.ID, projectName)
	if err == nil {
		c.log.Infof("found project (%s) (%s), checking dependencies ...", projectName, project.ID)

		links, err := c.dbpool.FindProjectLinks(c.user.ID, projectName)
		if err != nil {
			return err
		}

		if len(links) > 0 {
			e := fmt.Errorf("project (%s) has (%d) other projects linking to it, cannot delete project until they have been unlinked or removed, aborting ...", projectName, len(links))
			return e
		}

		out := fmt.Sprintf("(%s) removing ...\n", project.Name)
		_, _ = c.session.Write([]byte(out))
		if c.write {
			c.log.Infof("(%s) removing ...", project.Name)
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

func (c *Cmd) bail(err error) {
	if err == nil {
		return
	}
	c.log.Error(err)
	utils.ErrorHandler(c.session, err)
}

func (c *Cmd) notice() {
	if !c.write {
		out := fmt.Sprintf("\nNOTICE: changes not commited, use `--write` to save operation\n")
		_, _ = c.session.Write([]byte(out))
	}
}

func WishMiddleware(handler *uploadassets.UploadAssetHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Cfg.Logger
	cfg := handler.Cfg
	store := handler.Storage

	return func(sshHandler ssh.Handler) ssh.Handler {
		return func(session ssh.Session) {
			_, _, activePty := session.Pty()
			if activePty {
				_ = session.Exit(0)
				_ = session.Close()
				return
			}

			user, err := getUser(session, dbpool)
			if err != nil {
				utils.ErrorHandler(session, err)
				return
			}

			args := session.Command()

			opts := Cmd{
				session: session,
				user:    user,
				store:   store,
				log:     log,
				dbpool:  dbpool,
				write:   false,
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return
				} else if cmd == "stats" {
					err := opts.stats(cfg.MaxSize)
					opts.bail(err)
					return
				} else if cmd == "ls" {
					err := opts.ls()
					opts.bail(err)
					return
				} else {
					e := fmt.Errorf("%s not a valid command", args)
					opts.bail(e)
					return
				}
			}

			log.Infof("pgs middleware detected command: %s", args)
			projectName := strings.TrimSpace(args[1])

			if projectName == "--write" {
				utils.ErrorHandler(session, fmt.Errorf("`--write` should be placed at end of command"))
				return
			}

			if cmd == "link" {
				if len(args) < 3 {
					utils.ErrorHandler(session, fmt.Errorf("must supply link command like: `projectA link projectB`"))
					return
				}
				linkTo := strings.TrimSpace(args[2])
				if len(args) >= 4 && strings.TrimSpace(args[3]) == "--write" {
					opts.write = true
				}

				err := opts.link(projectName, linkTo)
				opts.notice()
				if err != nil {
					opts.bail(err)
				}
				return
			}

			if len(args) >= 3 && strings.TrimSpace(args[2]) == "--write" {
				opts.write = true
			}

			if cmd == "unlink" {
				err := opts.unlink(projectName)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "depends" {
				err := opts.depends(projectName)
				opts.bail(err)
				return
			} else if cmd == "retain" {
				err := opts.prune(projectName, 3)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "prune" {
				err := opts.prune(projectName, 0)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "rm" {
				err := opts.rm(projectName)
				opts.notice()
				opts.bail(err)
				return
			} else {
				e := fmt.Errorf("%s not a valid command", args)
				opts.bail(e)
				return
			}
		}
	}
}
