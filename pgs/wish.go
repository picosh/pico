package pgs

import (
	"flag"
	"fmt"
	"slices"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	bm "github.com/charmbracelet/wish/bubbletea"
	"github.com/muesli/termenv"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/tui/common"
	sendutils "github.com/picosh/send/utils"
	"github.com/picosh/utils"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	if s.PublicKey() == nil {
		return nil, fmt.Errorf("key not found")
	}

	key := utils.KeyForKeyText(s.PublicKey())

	user, err := dbpool.FindUserForKey(s.User(), key)
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

func flagSet(cmdName string, sesh ssh.Session) (*flag.FlagSet, *bool) {
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

func WishMiddleware(handler *UploadAssetHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Cfg.Logger
	cfg := handler.Cfg
	store := handler.Storage

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			args := sesh.Command()
			if len(args) == 0 {
				next(sesh)
				return
			}

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
				return
			}

			renderer := bm.MakeRenderer(sesh)
			renderer.SetColorProfile(termenv.TrueColor)
			styles := common.DefaultStyles(renderer)

			opts := Cmd{
				Session: sesh,
				User:    user,
				Store:   store,
				Log:     log,
				Dbpool:  dbpool,
				Write:   false,
				Styles:  styles,
				Width:   width,
				Height:  height,
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
					next(sesh)
					return
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

			if cmd == "stats" {
				err := opts.statsByProject(projectName)
				opts.bail(err)
				return
			} else if cmd == "link" {
				linkCmd, write := flagSet("link", sesh)
				linkTo := linkCmd.String("to", "", "symbolic link to this project")
				if !flagCheck(linkCmd, projectName, cmdArgs) {
					return
				}
				opts.Write = *write

				if *linkTo == "" {
					err := fmt.Errorf(
						"must provide `--to` flag",
					)
					opts.bail(err)
					return
				}

				err := opts.link(projectName, *linkTo)
				opts.notice()
				if err != nil {
					opts.bail(err)
				}
				return
			} else if cmd == "unlink" {
				unlinkCmd, write := flagSet("unlink", sesh)
				if !flagCheck(unlinkCmd, projectName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.unlink(projectName)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "depends" {
				err := opts.depends(projectName)
				opts.bail(err)
				return
			} else if cmd == "retain" {
				retainCmd, write := flagSet("retain", sesh)
				retainNum := retainCmd.Int("n", 3, "latest number of projects to keep")
				if !flagCheck(retainCmd, projectName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.prune(projectName, *retainNum)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "prune" {
				pruneCmd, write := flagSet("prune", sesh)
				if !flagCheck(pruneCmd, projectName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.prune(projectName, 0)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "rm" {
				rmCmd, write := flagSet("rm", sesh)
				if !flagCheck(rmCmd, projectName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.rm(projectName)
				opts.notice()
				opts.bail(err)
				return
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
					return
				}
				opts.Write = *write

				if !slices.Contains([]string{"public", "pubkeys", "pico"}, *aclType) {
					err := fmt.Errorf(
						"acl type must be one of the following: [public, pubkeys, pico], found %s",
						*aclType,
					)
					opts.bail(err)
					return
				}

				err := opts.acl(projectName, *aclType, acls)
				opts.notice()
				opts.bail(err)
			} else {
				next(sesh)
				return
			}
		}
	}
}
