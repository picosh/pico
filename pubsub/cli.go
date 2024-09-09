package pubsub

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"text/tabwriter"

	"github.com/charmbracelet/ssh"
	"github.com/charmbracelet/wish"
	"github.com/google/uuid"
	"github.com/picosh/pico/db"
	"github.com/picosh/pico/shared"
	"github.com/picosh/pico/shared/storage"
	psub "github.com/picosh/pubsub"
	"github.com/picosh/send/send/utils"
)

func flagSet(cmdName string, sesh ssh.Session) *flag.FlagSet {
	cmd := flag.NewFlagSet(cmdName, flag.ContinueOnError)
	cmd.SetOutput(sesh)
	return cmd
}

func flagCheck(cmd *flag.FlagSet, posArg string, cmdArgs []string) bool {
	_ = cmd.Parse(cmdArgs)

	if posArg == "-h" || posArg == "--help" || posArg == "-help" {
		cmd.Usage()
		return false
	}
	return true
}

func NewTabWriter(out io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(out, 0, 0, 1, ' ', tabwriter.TabIndent)
}

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

// scope channel to user by prefixing name.
func toChannel(userName, name string) string {
	return fmt.Sprintf("%s@%s", userName, name)
}

func toPublicChannel(name string) string {
	return fmt.Sprintf("public@%s", name)
}

// extract user and scoped channel from channel.
func fromChannel(channel string) (string, string) {
	sp := strings.SplitN(channel, "@", 2)
	ln := len(sp)
	if ln == 0 {
		return "", ""
	}
	if ln == 1 {
		return "", ""
	}
	return sp[0], sp[1]
}

var helpStr = `Commands: [pub, sub, ls]

The simplest authenticated pubsub system.  Send messages through
user-defined channels.  Channels are private to the authenticated
ssh user.  The default pubsub model is multicast with bidirectional
blocking, meaning a publisher ("pub") will send its message to all
subscribers ("sub").  Further, both "pub" and "sub" will wait for
at least one event to be sent or received.`

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
	pubsub := handler.PubSub

	return func(next ssh.Handler) ssh.Handler {
		return func(sesh ssh.Session) {
			ctx := sesh.Context()
			user, err := getUser(sesh, dbpool)
			if err != nil {
				utils.ErrorHandler(sesh, err)
				return
			}

			args := sesh.Command()

			if len(args) == 0 {
				wish.Println(sesh, helpStr)
				next(sesh)
				return
			}

			cmd := strings.TrimSpace(args[0])
			if len(args) == 1 {
				if cmd == "help" {
					wish.Println(sesh, helpStr)
				} else if cmd == "ls" {
					subs := pubsub.PubSub.GetSubs()

					if len(subs) == 0 {
						wish.Println(sesh, "no subs found")
					} else {
						writer := NewTabWriter(sesh)
						fmt.Fprintln(writer, "Channel\tID")
						for _, sub := range subs {
							userName, _ := fromChannel(sub.Name)
							if userName != "public" && userName != user.Name {
								continue
							}

							fmt.Fprintf(
								writer,
								"%s\t%s\n",
								sub.Name, sub.ID,
							)
						}
						writer.Flush()
					}
				}
				next(sesh)
				return
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

			if cmd == "pub" {
				pubCmd := flagSet("pub", sesh)
				empty := pubCmd.Bool("e", false, "Send an empty message to subs")
				public := pubCmd.Bool("p", false, "Anyone can sub to this channel")
				if !flagCheck(pubCmd, repoName, cmdArgs) {
					return
				}
				channelName := repoName

				var reader io.Reader
				if *empty {
					reader = strings.NewReader("")
				} else {
					reader = sesh
				}

				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}

				wish.Println(sesh, "sending msg ...")
				msg := &psub.Msg{
					Name:   name,
					Reader: reader,
				}

				// hacky: we want to notify when no subs are found so
				// we duplicate some logic for now
				subs := pubsub.PubSub.GetSubs()
				found := false
				for _, sub := range subs {
					if pubsub.PubSub.PubMatcher(msg, sub) {
						found = true
						break
					}
				}
				if !found {
					wish.Println(sesh, "no subs found ... waiting")
				}

				go func() {
					<-ctx.Done()
					err := pubsub.PubSub.UnPub(msg)
					if err != nil {
						wish.Errorln(sesh, err)
					}
				}()

				err = pubsub.PubSub.Pub(msg)
				wish.Println(sesh, "msg sent!")
				if err != nil {
					wish.Errorln(sesh, err)
				}
			} else if cmd == "sub" {
				pubCmd := flagSet("pub", sesh)
				public := pubCmd.Bool("p", false, "Subscribe to a public channel")
				if !flagCheck(pubCmd, repoName, cmdArgs) {
					return
				}
				channelName := repoName

				name := toChannel(user.Name, channelName)
				if *public {
					name = toPublicChannel(channelName)
				}

				sub := &psub.Subscriber{
					ID:     uuid.NewString(),
					Name:   name,
					Writer: sesh,
					Chan:   make(chan error),
				}

				go func() {
					<-ctx.Done()
					err := pubsub.PubSub.UnSub(sub)
					if err != nil {
						wish.Errorln(sesh, err)
					}
				}()
				err = pubsub.PubSub.Sub(sub)
				if err != nil {
					wish.Errorln(sesh, err)
				}
			}

			next(sesh)
		}
	}
}
