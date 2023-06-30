package pgs

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/wish"
	"github.com/gliderlabs/ssh"
	"github.com/picosh/pico/db"
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

func getHelpText(userName, projectName string) string {
	helpStr := "commands: [rm, list, link, unlink]\n\n"
	sshCmdStr := fmt.Sprintf("ssh %s@pgs.sh", userName)
	helpStr += fmt.Sprintf("`%s help`: prints this screen\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s list`: lists projects\n", sshCmdStr)
	helpStr += fmt.Sprintf("`%s %s rm`: deletes `%s`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s %s link projectB`: symbolic link from `%s` to `projectB`\n", sshCmdStr, projectName, projectName)
	helpStr += fmt.Sprintf("`%s %s unlink`: removes symbolic link for `%s`\n", sshCmdStr, projectName, projectName)
	return helpStr
}

func WishMiddleware(dbpool db.DB, store storage.ObjectStorage, log *zap.SugaredLogger) wish.Middleware {
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
					_, _ = session.Write([]byte(getHelpText(user.Name, "projectA")))
				} else if cmd == "list" {
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
				err = dbpool.RemoveProject(project.ID)
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
				project, err := dbpool.FindProjectByName(user.ID, projectName)
				if err == nil {
					log.Infof("user (%s) already has project (%s), updating ...", user.Name, projectName)
					err = dbpool.UpdateProject(project.ID, projectDir)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}
				} else {
					log.Infof("user (%s) has no project record (%s), creating ...", user.Name, projectName)
					_, err := dbpool.InsertProject(user.ID, projectName, projectDir)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
						return
					}
				}
				out := fmt.Sprintf("(%s) now points to (%s)\n", projectName, linkTo)
				_, _ = session.Write([]byte(out))
				return
			} else if cmd == "rm" {
				log.Infof("user (%s) running `rm` command for (%s)", user.Name, projectName)
				project, err := dbpool.FindProjectByName(user.ID, projectName)
				if err == nil {
					log.Infof("found project (%s) (%s), removing ...", projectName, project.ID)
					err = dbpool.RemoveProject(project.ID)
					if err != nil {
						log.Error(err)
						utils.ErrorHandler(session, err)
					}
				}

				bucketName := shared.GetAssetBucketName(user.ID)
				bucket, err := store.GetBucket(bucketName)
				if err != nil {
					log.Error(err)
					utils.ErrorHandler(session, err)
					return
				}

				fileList, err := store.ListFiles(bucket, projectName)
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
			}

			sshHandler(session)
		}
	}
}
