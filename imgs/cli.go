package imgs

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/google/uuid"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	"github.com/picosh/pico/tui/common"
	sst "github.com/picosh/pobj/storage"
	psub "github.com/picosh/pubsub"
	"github.com/picosh/send/send/utils"
)

func getUser(s ssh.Session, dbpool db.DB) (*db.User, error) {
	var err error
	key, err := shared.KeyText(s)
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

type Cmd struct {
	User        *db.User
	Session     shared.CmdSession
	Log         *slog.Logger
	Dbpool      db.DB
	Write       bool
	Styles      common.Styles
	Storage     sst.ObjectStorage
	RegistryUrl string
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

func (c *Cmd) help() {
	helpStr := "Commands: [help, ls, rm]\n"
	helpStr += "NOTICE: *must* append with `--write` for the changes to persist.\n"
	c.output(helpStr)
}

func (c *Cmd) rm(repo string) error {
	bucket, err := c.Storage.GetBucket("imgs")
	if err != nil {
		return err
	}

	fp := filepath.Join("docker/registry/v2/repositories", c.User.Name, repo)

	fileList, err := c.Storage.ListObjects(bucket, fp, true)
	if err != nil {
		return err
	}

	if len(fileList) == 0 {
		c.output(fmt.Sprintf("repo not found (%s)", repo))
		return nil
	}
	c.output(fmt.Sprintf("found (%d) objects for repo (%s), removing", len(fileList), repo))

	for _, obj := range fileList {
		fname := filepath.Join(fp, obj.Name())
		intent := fmt.Sprintf("deleted (%s)", obj.Name())
		c.Log.Info(
			"attempting to delete file",
			"user", c.User.Name,
			"bucket", bucket.Name,
			"repo", repo,
			"filename", fname,
		)
		if c.Write {
			err := c.Storage.DeleteObject(bucket, fname)
			if err != nil {
				return err
			}
		}
		c.output(intent)
	}

	return nil
}

type RegistryCatalog struct {
	Repos []string `json:"repositories"`
}

func (c *Cmd) ls() error {
	res, err := http.Get(
		fmt.Sprintf("http://%s/v2/_catalog", c.RegistryUrl),
	)
	if err != nil {
		return err
	}

	body, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	var data RegistryCatalog
	err = json.Unmarshal(body, &data)

	if err != nil {
		return err
	}

	if len(data.Repos) == 0 {
		c.output("You don't have any repos on imgs.sh")
		return nil
	}

	user := c.User.Name
	out := "repos\n"
	out += "-----\n"
	for _, repo := range data.Repos {
		if !strings.HasPrefix(repo, user+"/") {
			continue
		}
		rr := strings.TrimPrefix(repo, user+"/")
		out += fmt.Sprintf("%s\n", rr)
	}
	c.output(out)
	return nil
}

type CliHandler struct {
	DBPool      db.DB
	Logger      *slog.Logger
	Storage     storage.StorageServe
	RegistryUrl string
	PubSub      *psub.Cfg
}

func WishMiddleware(handler *CliHandler) wish.Middleware {
	dbpool := handler.DBPool
	log := handler.Logger
	st := handler.Storage
	pubsub := handler.PubSub

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			args := sesh.Command()

			opts := Cmd{
				Session:     sesh,
				User:        user,
				Log:         log,
				Dbpool:      dbpool,
				Write:       false,
				Storage:     st,
				RegistryUrl: handler.RegistryUrl,
			}

			if len(args) == 0 {
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					opts.help()
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

			repoName := strings.TrimSpace(args[1])
			cmdArgs := args[2:]
			log.Info(
				"imgs middleware detected command",
				"args", args,
				"cmd", cmd,
				"repoName", repoName,
				"cmdArgs", cmdArgs,
			)

			if cmd == "rm" {
				rmCmd, write := flagSet("rm", sesh)
				if !flagCheck(rmCmd, repoName, cmdArgs) {
					return
				}
				opts.Write = *write

				err := opts.rm(repoName)
				opts.notice()
				opts.bail(err)
				return
			} else if cmd == "sub" {
				err = pubsub.PubSub.Sub(&psub.Subscriber{
					ID:      uuid.NewString(),
					Name:    fmt.Sprintf("%s@%s", user.Name, repoName),
					Session: sesh,
					Chan:    make(chan error),
				})
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else {
				next(sesh)
				return
			}
		}
	}
}
