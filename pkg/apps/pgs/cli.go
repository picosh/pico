package pgs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/shared"
	"github.com/picosh/pico/pkg/storage"
)

func NewTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 2, ' ', tabwriter.TabIndent)
}

func projectTable(sesh io.Writer, projects []*db.Project) {
	writer := NewTabWriter(sesh)
	_, _ = fmt.Fprintln(writer, "Name\tLast Updated\tLinks To\tACL Type\tACL\tBlocked")

	for _, project := range projects {
		links := ""
		if project.ProjectDir != project.Name {
			links = project.ProjectDir
		}
		_, _ = fmt.Fprintf(
			writer,
			"%s\t%s\t%s\t%s\t%s\t%s\r\n",
			project.Name,
			project.UpdatedAt.Format("2006-01-02 15:04:05"),
			links,
			project.Acl.Type,
			strings.Join(project.Acl.Data, " "),
			project.Blocked,
		)
	}
	_ = writer.Flush()
}

type Cmd struct {
	User    *db.User
	Session shared.CmdSession
	Log     *slog.Logger
	Store   storage.StorageServe
	Dbpool  pgsdb.PgsDB
	Write   bool
	Width   int
	Height  int
	Cfg     *PgsConfig
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
		if file.IsDir() {
			continue
		}
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
	helpStr := `pgs.sh
======

Deploy static sites with a single command. No passwords. No config files. No CI setup. Just your SSH key and rsync.

	rsync --delete -rv ./public/ pgs.sh:/mysite
	# => https://erock-mysite.pgs.sh

That's the entire workflow. Your SSH key is your identity and every deploy is instant.

You can fetch project assets with the same command in reverse:

	rsync -rv pgs.sh:/mysite/ ./public/

You can also use unix pipes to directly upload files by providing the project name as part of the path:

	echo "<body>hello world!</body>" | ssh pgs.sh /mysite/index.html
	# => https://erock-mysite.pgs.sh/index.html

The leading "/" is important.

You can also create private projects when you prefix the project name with 'private':

	rsync -rv ./public/ pgs.sh:/private-site/

This means only you can access the site through a web tunnel or by downloading the files.
`
	helpStr += "\r\nCommands: [help, stats, ls, fzf, rm, link, unlink, prune, retain, depends, acl, cache]\r\n"
	helpStr += "For most of these commands you can provide a `-h` to learn about its usage.\r\n"
	helpStr += "\r\n> NOTICE:" + " *must* append with `--write` for the changes to persist.\r\n"
	c.output(helpStr)
	projectName := "{project}"

	data := [][]string{
		{
			"help",
			"Prints this screen",
		},
		{
			"stats",
			"Usage statistics (quota, % quota used, number of projects)",
		},
		{
			"ls",
			"Lists all projects and meta data",
		},
		{
			fmt.Sprintf("fzf %s", projectName),
			"Lists urls of all assets in project",
		},
		{
			fmt.Sprintf("rm %s", projectName),
			"Removes all files in project and then deletes the project",
		},
		{
			fmt.Sprintf("link %s --to projB", projectName),
			fmt.Sprintf("Instant promotion and rollback mechanism that symbolic links %s to `projB`", projectName),
		},
		{
			fmt.Sprintf("unlink %s", projectName),
			"Removes symbolic link",
		},
		{
			fmt.Sprintf("prune %s", projectName),
			"Delete all projects matching a prefix (except projects with linked projects)",
		},
		{
			fmt.Sprintf("retain %s", projectName),
			"Delete all projects matching a prefix except the last N recently updated projects.",
		},
		{
			fmt.Sprintf("depends %s", projectName),
			"Lists all projects linked to project",
		},
		{
			fmt.Sprintf("acl %s", projectName),
			"Access control for project",
		},
		{
			fmt.Sprintf("cache %s", projectName),
			"Clear http cache",
		},
	}

	writer := NewTabWriter(c.Session)
	_, _ = fmt.Fprintln(writer, "Cmd\tDescription")
	_, _ = fmt.Fprintf(writer, "===\t===========\r\n")
	for _, dat := range data {
		_, _ = fmt.Fprintf(writer, "%s\t%s\r\n", dat[0], dat[1])
	}
	_ = writer.Flush()
}

func (c *Cmd) stats(cfgMaxSize uint64) error {
	ff, err := c.Dbpool.FindFeature(c.User.ID, "plus")
	if err != nil {
		ff = db.NewFeatureFlag(c.User.ID, "plus", cfgMaxSize, 0, 0)
	}
	// this is jank
	ff.Data.StorageMax = ff.FindStorageMax(cfgMaxSize)
	storageMax := ff.Data.StorageMax

	bucketName := shared.GetAssetBucketName(c.User.ID)
	bucket, err := c.Store.GetBucket(bucketName)
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

	writer := NewTabWriter(c.Session)
	_, _ = fmt.Fprintln(writer, "Used (GB)\tQuota (GB)\tUsed (%)\tProjects (#)")
	_, _ = fmt.Fprintf(
		writer,
		"%.4f\t%.4f\t%.4f\t%d\r\n",
		shared.BytesToGB(int(totalFileSize)),
		shared.BytesToGB(int(storageMax)),
		(float32(totalFileSize)/float32(storageMax))*100,
		len(projects),
	)
	return writer.Flush()
}

func (c *Cmd) ls() error {
	projects, err := c.Dbpool.FindProjectsByUser(c.User.ID)
	if err != nil {
		return err
	}

	if len(projects) == 0 {
		c.output("no projects found")
	}

	projectTable(c.Session, projects)

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

func (c *Cmd) fzf(projectName string) error {
	project, err := c.Dbpool.FindProjectByName(c.User.ID, projectName)
	if err != nil {
		return err
	}

	bucket, err := c.Store.GetBucket(shared.GetAssetBucketName(c.User.ID))
	if err != nil {
		return err
	}

	objs, err := c.Store.ListObjects(bucket, project.ProjectDir+"/", true)
	if err != nil {
		return err
	}

	for _, obj := range objs {
		if strings.Contains(obj.Name(), "._pico_keep_dir") {
			continue
		}
		url := c.Cfg.AssetURL(
			c.User.Name,
			project.Name,
			strings.TrimPrefix(obj.Name(), "/"),
		)
		c.output(url)
	}

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

	projectTable(c.Session, projects)
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
		pmax := len(rmProjects) - (keepNumLatest)
		if pmax <= 0 {
			out := fmt.Sprintf(
				"no projects available to prune (retention policy: %d, total: %d)",
				keepNumLatest,
				len(rmProjects),
			)
			c.output(out)
			return nil
		}
		goodbye = rmProjects[:pmax]
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

	c.output("\r\nsummary")
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
		msg := fmt.Sprintf("(%s) project record not found for user (%s)", projectName, c.User.Name)
		c.output(msg)
	}

	err = c.RmProjectAssets(projectName)
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

func (c *Cmd) cache(projectName string) error {
	c.Log.Info(
		"user running `cache` command",
		"user", c.User.Name,
		"project", projectName,
	)

	c.output(fmt.Sprintf("clearing http cache for %s", projectName))

	if c.Write {
		surrogate := getSurrogateKey(c.User.Name, projectName)
		return purgeCache(c.Cfg, c.Cfg.Pubsub, surrogate)
	}
	return nil
}

func (c *Cmd) cacheAll() error {
	isAdmin := false
	ff, _ := c.Dbpool.FindFeature(c.User.ID, "admin")
	if ff != nil {
		if ff.ExpiresAt.Before(time.Now()) {
			isAdmin = true
		}
	}

	if !isAdmin {
		return fmt.Errorf("must be admin to use this command")
	}

	c.Log.Info(
		"admin running `cache-all` command",
		"user", c.User.Name,
	)
	c.output("clearing http cache for all sites")
	if c.Write {
		return purgeAllCache(c.Cfg, c.Cfg.Pubsub)
	}
	return nil
}

func (c *Cmd) formsLs() error {
	forms, err := c.Dbpool.FindFormNamesByUser(c.User.ID)
	if err != nil {
		return err
	}
	if len(forms) == 0 {
		c.output("no forms found")
		return nil
	}
	for _, name := range forms {
		c.output(name)
	}
	return nil
}

func (c *Cmd) formData(formName string) error {
	formData, err := c.Dbpool.FindFormEntriesByUserAndName(c.User.ID, formName)
	if err != nil {
		return err
	}
	data, err := json.Marshal(formData)
	if err != nil {
		return err
	}
	c.output(string(data))
	return nil
}

func (c *Cmd) formRm(formName string) error {
	c.output(fmt.Sprintf("removing all data associated with form: %s", formName))
	if c.Write {
		return c.Dbpool.RemoveFormEntriesByUserAndName(c.User.ID, formName)
	}
	return nil
}
