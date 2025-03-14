package pgs

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	pgsdb "github.com/picosh/pico/pkg/apps/pgs/db"
	"github.com/picosh/pico/pkg/db"
	"github.com/picosh/pico/pkg/pssh"
	sendutils "github.com/picosh/pico/pkg/send/utils"
	"github.com/picosh/utils"
)

func getUser(s *pssh.SSHServerConnSession, dbpool pgsdb.PgsDB) (*db.User, error) {
	if s.PublicKey() == nil {
		return nil, fmt.Errorf("key not found")
	}

	key := utils.KeyForKeyText(s.PublicKey())

	user, err := dbpool.FindUserByPubkey(key)
	if err != nil {
		return nil, err
	}

	if user.Name == "" {
		return nil, fmt.Errorf("must have username set")
	}

	return user, nil
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

			user, err := getUser(sesh, dbpool)
			if err != nil {
				sendutils.ErrorHandler(sesh, err)
				return err
			}

			// renderer := bm.MakeRenderer(sesh)
			// renderer.SetColorProfile(termenv.TrueColor)
			// styles := common.DefaultStyles(renderer)

			opts := Cmd{
				Session: sesh,
				User:    user,
				Store:   store,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
				// Styles:  styles,
				Width:  width,
				Height: height,
				Cfg:    handler.Cfg,
			}

			if len(args) == 0 {
				opts.help()
				return nil
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
					return nil
				} else if cmd == "stats" {
					err := opts.stats(cfg.MaxSize)
					opts.bail(err)
					return err
				} else if cmd == "ls" {
					err := opts.ls()
					opts.bail(err)
					return err
				} else if cmd == "cache-all" {
					opts.Write = true
					err := opts.cacheAll()
					opts.notice()
					opts.bail(err)
					return err
				} else {
					return next(sesh)
				}
			}

			projectName := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			log.Info(
				"pgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"projectName", projectName,
				"cmdArgs", cmdArgs,
			)

			if cmd == "fzf" {
				err := opts.fzf(projectName)
				opts.bail(err)
				return err
			} else if cmd == "link" {
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

				err := opts.link(projectName, *linkTo)
				opts.notice()
				if err != nil {
					opts.bail(err)
				}
				return err
			} else if cmd == "unlink" {
				unlinkCmd, write := flagSet("unlink", sesh)
				if !flagCheck(unlinkCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.unlink(projectName)
				opts.notice()
				opts.bail(err)
				return err
			} else if cmd == "depends" {
				err := opts.depends(projectName)
				opts.bail(err)
				return err
			} else if cmd == "retain" {
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
			} else if cmd == "prune" {
				pruneCmd, write := flagSet("prune", sesh)
				if !flagCheck(pruneCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.prune(projectName, 0)
				opts.notice()
				opts.bail(err)
				return err
			} else if cmd == "rm" {
				rmCmd, write := flagSet("rm", sesh)
				if !flagCheck(rmCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.rm(projectName)
				opts.notice()
				opts.bail(err)
				return err
			} else if cmd == "cache" {
				cacheCmd, write := flagSet("cache", sesh)
				if !flagCheck(cacheCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				err := opts.cache(projectName)
				opts.notice()
				opts.bail(err)
				return err
			} else if cmd == "acl" {
				aclCmd, write := flagSet("acl", sesh)
				aclType := aclCmd.String("type", "", "access type: public, pico, pubkeys")
				var acls arrayFlags
				aclCmd.Var(
					&acls,
					"acl",
					"list of pico usernames or sha256 public keys, delimited by commas",
				)
				if !flagCheck(aclCmd, projectName, cmdArgs) {
					return nil
				}
				opts.Write = *write

				if !slices.Contains([]string{"public", "pubkeys", "pico"}, *aclType) {
					err := fmt.Errorf(
						"acl type must be one of the following: [public, pubkeys, pico], found %s",
						*aclType,
					)
					opts.bail(err)
					return err
				}

				err := opts.acl(projectName, *aclType, acls)
				opts.notice()
				opts.bail(err)
				return err
			} else {
				return next(sesh)
			}
		}
	}
}
