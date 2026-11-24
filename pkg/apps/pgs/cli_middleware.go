package pgs

import (
	"errors"
	"flag"
	"fmt"
	"slices"
	"strings"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/pico/pkg/shared"
)

func getUser(s *pssh.SSHServerConnSession, dbpool pgsdb.PgsDB) (*db.User, error) {
	userID, ok := s.Conn.Permissions.Extensions["user_id"]
	if !ok {
		return nil, fmt.Errorf("`user_id` extension not found")
	}
	return dbpool.FindUser(userID)
}

type arrayFlags []string

func (i *arrayFlags) String() string {
	return "array flags"
}

func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

func flagSet(cmdName string, sesh *pssh.SSHServerConnSession) (*flag.FlagSet, *bool) {
	cmd := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmd.SetOutput(sesh)
	write := cmd.Bool("write", false, "apply changes")
	return cmd, write
}

func flagCheck(cmd *flag.FlagSet, posArg string, cmdArgs []string) bool {
	_ = cmd.Parse(cmdArgs)

	if posArg == "-h" || posArg == "--help" || posArg == "-help" {
		cmd.Usage()
		return false
	}
	return true
}

func Middleware(handler *UploadAssetHandler) pssh.SSHServerMiddleware {
	dbpool := handler.Cfg.DB
	log := handler.Cfg.Logger
	cfg := handler.Cfg
	store := handler.Cfg.Storage

	return func(next pssh.SSHServerHandler) pssh.SSHServerHandler {
		return func(sesh *pssh.SSHServerConnSession) error {
			args := sesh.Command()

			// default width and height when no pty
			width := 100
			height := 24
			pty, _, ok := sesh.Pty()
			if ok {
				width = pty.Window.Width
				height = pty.Window.Height
			}

			opts := Cmd{
				Session: sesh,
				Store:   store,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
				Width:   width,
				Height:  height,
				Cfg:     handler.Cfg,
				Scopes:  shared.SessionScopes(sesh),
			}

			user, err := getUser(sesh, dbpool)
			if err != nil {
				sendutils.ErrorHandler(sesh, err)
				return err
			}

			opts.User = user

			if len(args) == 0 {
				opts.help()
				return nil
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				switch cmd {
				case "help":
					opts.help()
					return nil
				case "stats":
					if err := opts.checkRead(""); err != nil {
						opts.bail(err)
						return err
					}
					err := opts.stats(cfg.MaxSize)
					opts.bail(err)
					return err
				case "ls":
					err := opts.ls()
					opts.bail(err)
					return err
				case "cache-all":
					if err := opts.Scopes.CheckFull("clearing every cache"); err != nil {
						opts.bail(err)
						return err
					}
					opts.Write = true
					err := opts.cacheAll()
					opts.notice()
					opts.bail(err)
					return err
				default:
					return next(sesh)
				}
			}

			projectName := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			logArgs, logCmdArgs := args, cmdArgs
			if cmd == "acl" {
				// may hold an http-pass or http-basic password
				logArgs, logCmdArgs = []string{cmd, projectName}, nil
			}
			log.Info(
				"pgs middleware detected command",
				"args", logArgs,
				"cmd", cmd,
				"projectName", projectName,
				"cmdArgs", logCmdArgs,
			)

			switch cmd {
			case "fzf":
				if err := opts.checkRead(projectName); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.fzf(projectName)
				opts.bail(err)
				return err
			case "link":
				linkCmd, write := flagSet("link", sesh)
				linkTo := linkCmd.String("to", "", "symbolic link to this project")
				if !flagCheck(linkCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if *linkTo == "" {
					err := fmt.Errorf(
						"must provide `--to` flag",
					)
					opts.bail(err)
					return err
				}

				if err := errors.Join(opts.checkDeploy(projectName), opts.checkRead(*linkTo)); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.link(projectName, *linkTo)
				opts.notice()
				if err != nil {
					opts.bail(err)
				}
				return err
			case "unlink":
				unlinkCmd, write := flagSet("unlink", sesh)
				if !flagCheck(unlinkCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if err := opts.checkDeploy(projectName); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.unlink(projectName)
				opts.notice()
				opts.bail(err)
				return err
			case "depends":
				if err := opts.checkRead(projectName); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.depends(projectName)
				opts.bail(err)
				return err
			case "retain":
				retainCmd, write := flagSet("retain", sesh)
				retainNum := retainCmd.Int("n", 3, "latest number of projects to keep")
				if !flagCheck(retainCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.prune(projectName, *retainNum)
				opts.notice()
				opts.bail(err)
				return err
			case "prune":
				pruneCmd, write := flagSet("prune", sesh)
				if !flagCheck(pruneCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.prune(projectName, 0)
				opts.notice()
				opts.bail(err)
				return err
			case "rm":
				rmCmd, write := flagSet("rm", sesh)
				if !flagCheck(rmCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if err := opts.checkDeploy(projectName); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.rm(projectName)
				opts.notice()
				opts.bail(err)
				return err
			case "cache":
				cacheCmd, write := flagSet("cache", sesh)
				if !flagCheck(cacheCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if err := opts.checkDeploy(projectName); err != nil {
					opts.bail(err)
					return err
				}
				err := opts.cache(projectName)
				opts.notice()
				opts.bail(err)
				return err
			case "forms":
				formName := projectName
				formsCmd, write := flagSet("forms", sesh)
				rmForm := formsCmd.Bool("rm", false, "delete form data")
				if !flagCheck(formsCmd, formName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				// Forms belong to the account, so no project covers them.
				check := opts.checkRead
				if *rmForm && formName != "ls" {
					check = opts.checkDeploy
				}
				if err := check(""); err != nil {
					opts.bail(err)
					return err
				}

				var err error
				if formName == "ls" {
					err = opts.formsLs()
				} else {
					if *rmForm {
						err = opts.formRm(formName)
						opts.notice()
					} else {
						err = opts.formData(formName)
					}
				}

				opts.bail(err)
				return err
			case "acl":
				aclCmd, write := flagSet("acl", sesh)
				aclType := aclCmd.String("type", "", "access type: public, pico, pubkeys, http-pass, http-basic")
				var acls arrayFlags
				aclCmd.Var(
					&acls,
					"acl",
					"list of pico usernames or sha256 public keys, delimited by commas (for http-pass: the password, for http-basic: <user>:<password>)",
				)
				if !flagCheck(aclCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if err := opts.Scopes.CheckFull("setting an acl"); err != nil {
					opts.bail(err)
					return err
				}
				if !slices.Contains([]string{"public", "pubkeys", "pico", "http-pass", "http-basic"}, *aclType) {
					err := fmt.Errorf(
						"acl type must be one of the following: [public, pubkeys, pico, http-pass, http-basic], found %s",
						*aclType,
					)
					opts.bail(err)
					return err
				}

				if !db.HasFeature(dbpool, user.ID, "plus") {
					err = fmt.Errorf("setting acl on a project requires pico+")
					opts.bail(err)
					return err
				}

				if pgsdb.IsProjectPrivate(projectName) {
					err = fmt.Errorf("projects prefixed with `private-` can *never* have their access changed; however you can symlink to it")
					opts.bail(err)
					return err
				}

				err := opts.acl(projectName, *aclType, acls)
				opts.notice()
				opts.bail(err)
				return err
			default:
				return next(sesh)
			}
		}
	}
}
