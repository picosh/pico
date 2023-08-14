package pgs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
	uploadassets "github.com/picosh/pico/filehandlers/assets"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/wish/cms/util"
	"github.com/picosh/pico/wish/send/utils"
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

func getHelpText(userName, projectName string) string {
	helpStr := "commands: [help, stats, ls, rm, clean, link, links, unlink]\n\n"
	sshCmdStr := fmt.Sprintf("ssh %s@pgs.sh", userName)
	helpStr += fmt.Sprintf("`%s help`: prints this screen\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s stats`: prints stats for user\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s ls`: lists projects\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s %s rm`: deletes `%s`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s %s clean`: removes all projects that match prefix `%s` and is not linked to another project\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s %s links`: lists all projects linked to `%s`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s %s link project-b`: symbolic link from `%s` to `project-b`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf(
		"`%s %s unlink`: alias for `%s link %s`, which removes symbolic link for `%s`\n",
		sshCmdStr, projectName, projectName, projectName, projectName,
	)
	return helpStr
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
			if len(args) == 1 {
				cmd := strings.TrimSpace(args[0])
				if cmd == "help" {
					_, _ = session.Write([]byte(getHelpText(user.Name, "project-a")))
				} else if cmd == "stats" {
					bucketName := shared.GetAssetBucketName(user.ID)
					bucket, err := store.UpsertBucket(bucketName)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}

					totalFileSize, err := store.GetBucketQuota(bucket)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}

					projects, err := dbpool.FindProjectsByUser(user.ID)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}

					str := "stats\n"
					str += "=====\n"
					str += fmt.Sprintf(
						"space:\t\t%.4f/%.4fGB, %.4f%%\n",
						shared.BytesToGB(int(totalFileSize)),
						shared.BytesToGB(cfg.MaxSize),
						(float32(totalFileSize)/float32(cfg.MaxSize))*100,
					)
					str += fmt.Sprintf("projects:\t%d\n", len(projects))
					_, _ = session.Write([]byte(str))
					return
				} else if cmd == "list" || cmd == "ls" {
					projects, err := dbpool.FindProjectsByUser(user.ID)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}

					if len(projects) == 0 {
						out := "no linked projects found\n"
						_, _ = session.Write([]byte(out))
					}

					for _, project := range projects {
						out := fmt.Sprintf("%s (links to: %s)\n", project.Name, project.ProjectDir)
						if project.Name == project.ProjectDir {
							out = fmt.Sprintf("%s\n", project.Name)
						}
						_, _ = session.Write([]byte(out))
					}
				}
				return
			} else if len(args) < 2 {
				utils.ErrorHandler(session, fmt.Errorf("must supply project name and then a command"))
				return
			}

			projectName := strings.TrimSpace(args[0])
			cmd := strings.TrimSpace(args[1])
			log.Infof("pgs middleware detected command: %s", args)

			if cmd == "help" {
				log.Infof("user (%s) running `help` command", user.Name)
				_, _ = session.Write([]byte(getHelpText(user.Name, projectName)))
				return
			} else if cmd == "unlink" {
				log.Infof("user (%s) running `unlink` command with (%s)", user.Name, projectName)
				project, err := dbpool.FindProjectByName(user.ID, projectName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, fmt.Errorf("project (%s) does not exit", projectName))
					return
				}
				err = dbpool.LinkToProject(user.ID, project.ID, project.Name)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				return
			} else if cmd == "link" {
				if len(args) < 3 {
					utils.ErrorHandler(session, fmt.Errorf("must supply link command like: `projectA link projectB`"))
					return
				}
				linkTo := strings.TrimSpace(args[2])
				log.Infof("user (%s) running `link` command with (%s) (%s)", user.Name, projectName, linkTo)

				projectDir := linkTo
				_, err := dbpool.FindProjectByName(user.ID, linkTo)
				if err != nil {
					e := fmt.Errorf("(%s) project doesn't exist", linkTo)
					log.Error(e)
					utils.ErrorHandler(session, e)
					return
				}

				project, err := dbpool.FindProjectByName(user.ID, projectName)
				projectID := ""
				if err == nil {
					projectID = project.ID
					log.Infof("user (%s) already has project (%s), updating ...", user.Name, projectName)
					err = dbpool.LinkToProject(user.ID, project.ID, projectDir)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}
				} else {
					log.Infof("user (%s) has no project record (%s), creating ...", user.Name, projectName)
					id, err := dbpool.InsertProject(user.ID, projectName, projectName)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}
					projectID = id
				}

				log.Infof("user (%s) linking (%s) to (%s) ...", user.Name, projectName, projectDir)
				err = dbpool.LinkToProject(user.ID, projectID, projectDir)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				/* bucketName := shared.GetAssetBucketName(user.ID)
				bucket, err := store.GetBucket(bucketName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				fileList, err := store.ListFiles(bucket, projectName+"/", true)
				if err != nil {
					log.Error(err)
					return
				}

				if len(fileList) > 0 {
					out := fmt.Sprintf("(%s) assets now orphaned, deleting files (%d) ...\n", projectName, len(fileList))
					_, _ = session.Write([]byte(out))
				}

				for _, file := range fileList {
					err = store.DeleteFile(bucket, file.Name())
					if err == nil {
						_, _ = session.Write([]byte(fmt.Sprintf("deleted orphaned (%s)\n", file.Name())))
					} else {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}
				} */

				out := fmt.Sprintf("(%s) now points to (%s)\n", projectName, linkTo)
				_, _ = session.Write([]byte(out))
				return
			} else if cmd == "links" {
				projects, err := dbpool.FindProjectLinks(user.ID, projectName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				if len(projects) == 0 {
					out := fmt.Sprintf("no projects linked to this project (%s) found\n", projectName)
					_, _ = session.Write([]byte(out))
					return
				}

				for _, project := range projects {
					out := fmt.Sprintf("%s (links to: %s)\n", project.Name, project.ProjectDir)
					if project.Name == project.ProjectDir {
						out = fmt.Sprintf("%s\n", project.Name)
					}
					_, _ = session.Write([]byte(out))
				}
			} else if cmd == "clean" {
				log.Infof("user (%s) running `clean` command for (%s)", user.Name, projectName)
				if projectName == "" || projectName == "*" {
					e := fmt.Errorf("must provide valid prefix")
					log.Error(e)
					utils.ErrorHandler(session, e)
					return
				}

				projects, err := dbpool.FindProjectsByPrefix(user.ID, projectName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
				}

				bucketName := shared.GetAssetBucketName(user.ID)
				bucket, err := store.GetBucket(bucketName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				rmProjects := []*db.Project{}
				for _, project := range projects {
					links, err := dbpool.FindProjectLinks(user.ID, project.Name)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}

					if len(links) == 0 {
						out := fmt.Sprintf("project (%s) is available to delete\n", project.Name)
						_, _ = session.Write([]byte(out))
						rmProjects = append(rmProjects, project)
					}
				}

				for _, project := range rmProjects {
					fileList, err := store.ListFiles(bucket, project.Name, true)
					if err != nil {
						log.Error(err)
						return
					}

					for _, file := range fileList {
						err = store.DeleteFile(bucket, file.Name())
						if err == nil {
							_, _ = session.Write([]byte(fmt.Sprintf("deleted (%s)\n", file.Name())))
						} else {
							log.Error(err)
							utils.ErrorHandler(session, err)
						}
					}

					err = dbpool.RemoveProject(project.ID)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}
				}
			} else if cmd == "rm" {
				log.Infof("user (%s) running `rm` command for (%s)", user.Name, projectName)
				project, err := dbpool.FindProjectByName(user.ID, projectName)
				if err == nil {
					log.Infof("found project (%s) (%s), checking dependencies ...", projectName, project.ID)

					links, err := dbpool.FindProjectLinks(user.ID, projectName)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}

					if len(links) > 0 {
						e := fmt.Errorf("project (%s) has (%d) other projects linking to it, can't delete project until they have been unlinked or removed, aborting ...", projectName, len(links))
						log.Error(e)
						return
					}

					err = dbpool.RemoveProject(project.ID)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}
				} else {
					e := fmt.Errorf("(%s) project not found for user (%s)", projectName, user.Name)
					log.Error(e)
					utils.ErrorHandler(session, e)
					return
				}

				bucketName := shared.GetAssetBucketName(user.ID)
				bucket, err := store.GetBucket(bucketName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				fileList, err := store.ListFiles(bucket, projectName, true)
				if err != nil {
					log.Error(err)
					return
				}

				for _, file := range fileList {
					err = store.DeleteFile(bucket, file.Name())
					if err == nil {
						_, _ = session.Write([]byte(fmt.Sprintf("deleted (%s)\n", file.Name())))
					} else {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}
				}
				return
			} else {
				e := fmt.Errorf("%s not a valid command", args)
				log.Error(e)
				utils.ErrorHandler(session, e)
				return
			}
		}
	}
}
